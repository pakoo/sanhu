package handler

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	sanhugo "github.com/pakoo/sanhu"
	"github.com/pakoo/sanhu/internal/aiprompt"
	"github.com/pakoo/sanhu/internal/analysis"
	"github.com/pakoo/sanhu/internal/selector"
	"github.com/pakoo/sanhu/internal/timing"
)

// 赛道分析与持仓穿透两个提示词入口。
//
// 赛道分析原来的实现是一行 ReadFile 原样返回模板文件，于是用户复制到的是：
//   - 开头 484 字给开发者看的注释（version / data_dependencies / philosophy_check）
//   - 以及一堆没被替换的 {seed_name} {seed_code} {holdings_table} 占位符
// Python 版有完整的 render_theme_analysis_prompt，Go 重写时整块漏掉了。

// GetFundAIThemePrompt 渲染单只基金的赛道分析提示词
func (h *Handler) GetFundAIThemePrompt(c *gin.Context) {
	code := c.Param("code")

	raw, err := fs.ReadFile(sanhugo.PromptsFS, "prompts/theme_analysis.md")
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	vars := h.themePromptVars(code)
	rendered, missing := aiprompt.Render(string(raw), vars)
	// 仍有占位符说明这只基金确实缺该项数据（比如没有季报持仓），
	// 如实写「暂无数据」比把 {holdings_table} 发给用户强
	if len(missing) > 0 {
		rendered = aiprompt.FillMissing(rendered, "（本地暂无该项数据）")
	}
	rendered += "\n\n" + aiprompt.PrivacyNote

	ok(c, gin.H{
		"prompt":       rendered,
		"title":        "赛道分析",
		"code":         code,
		"missing":      missing, // 前端可选择提示「部分数据缺失」
		"generated_at": time.Now().Format("2006-01-02 15:04"),
	})
}

// themePromptVars 收集模板需要的 21 个占位符的值
func (h *Handler) themePromptVars(code string) map[string]string {
	vars := map[string]string{
		"{as_of_date}": time.Now().Format("2006-01-02"),
	}

	// 基本信息
	var row struct {
		Name        string `db:"name"`
		Category    string `db:"category"`
		ThemeTags   string `db:"theme_tags"`
		TopIndustry string `db:"top_industry"`
	}
	if err := h.DB.Get(&row, `
		SELECT COALESCE(f.name,'') AS name,
		       COALESCE(f.category,'') AS category,
		       COALESCE(p.theme_tags,'') AS theme_tags,
		       COALESCE(p.top_industry,'') AS top_industry
		FROM funds f LEFT JOIN fund_profile p ON p.code=f.code
		WHERE f.code=?`, code); err == nil {
		vars["{seed_code}"] = code
		vars["{seed_name}"] = orDash(row.Name)
		vars["{seed_category}"] = aiprompt.CategoryName(row.Category)
		vars["{seed_theme_tags}"] = orDash(cleanTags(row.ThemeTags))
		vars["{inferred_theme}"] = orDash(row.TopIndustry)
	}

	// 前十大持股
	type stockRow struct {
		StockName string  `db:"stock_name"`
		StockCode string  `db:"stock_code"`
		Weight    float64 `db:"weight"`
		Industry  string  `db:"industry"`
	}
	var stocks []stockRow
	var reportDate string
	_ = h.DB.Get(&reportDate, `SELECT COALESCE(MAX(report_date),'') FROM fund_holdings WHERE code=?`, code)
	if reportDate != "" {
		_ = h.DB.Select(&stocks, `
			SELECT stock_name, stock_code, COALESCE(weight,0) AS weight, COALESCE(industry,'') AS industry
			FROM fund_holdings WHERE code=? AND report_date=?
			ORDER BY weight DESC`, code, reportDate)
	}
	rows := make([][]string, 0, len(stocks))
	for _, s := range stocks {
		rows = append(rows, []string{s.StockName, s.StockCode, aiprompt.Pct(s.Weight), orDash(s.Industry)})
	}
	vars["{holdings_report_date}"] = orDash(reportDate)
	vars["{holdings_table}"] = aiprompt.Table(
		[]string{"股票", "代码", "占净值", "行业"}, rows, "（本地暂无该基金的季报持仓数据）")

	// 同主题 peer 基金
	peerCodes, _ := selector.FindPeerFundsByTheme(h.DB, code, 8)
	peerRows := make([][]string, 0, len(peerCodes))
	for _, pc := range peerCodes {
		var p struct {
			Name     string  `db:"name"`
			Category string  `db:"category"`
			Pct1y    float64 `db:"pct_1y"`
		}
		if err := h.DB.Get(&p, `
			SELECT COALESCE(f.name,'') AS name, COALESCE(f.category,'') AS category,
			       COALESCE((SELECT pct_1y FROM fund_peer_ranks WHERE code=f.code LIMIT 1),0) AS pct_1y
			FROM funds f WHERE f.code=?`, pc); err != nil {
			continue
		}
		peerRows = append(peerRows, []string{
			orDash(p.Name), pc, aiprompt.CategoryName(p.Category), aiprompt.Pct(p.Pct1y),
		})
	}
	vars["{peers_table}"] = aiprompt.Table(
		[]string{"基金", "代码", "类别", "近1年同类百分位"}, peerRows, "（未找到同主题的对比基金）")
	vars["{matched_on}"] = "行业与主题标签"
	vars["{matched_tags}"] = orDash(vars["{seed_theme_tags}"])

	// 市场估值：沪深300 / 中证500 PE 百分位。
	// 复用 pePercentile（它返回 gin.H，字段可能是 nil，逐个断言取值）
	setValuation := func(indexCode, peKey, pctKey, sigKey string) {
		vars[peKey], vars[pctKey], vars[sigKey] = "—", "—", "—"
		res := pePercentile(h.DB, indexCode)
		if pe, okv := res["current_pe"].(float64); okv {
			vars[peKey] = aiprompt.Num(pe)
		}
		if pct, okv := res["percentile"].(float64); okv {
			// 模板里占位符后面已经写了 %（"{hs300_pe_pct}%"），
			// 这里再用 Pct() 会输出 81.3%% —— 所以只给数字
			vars[pctKey] = aiprompt.Num(pct)
		}
		if sig, okv := res["signal"].(string); okv && sig != "" {
			vars[sigKey] = sig
		}
	}
	setValuation("hs300", "{hs300_pe}", "{hs300_pe_pct}", "{hs300_signal}")
	setValuation("csi500", "{csi500_pe}", "{csi500_pe_pct}", "{csi500_signal}")

	// 三层择时信号
	if t, err := timing.GetTimingSignal(h.DB, code); err == nil && t != nil {
		vars["{seed_timing_level}"] = orDash(t.Level)
		vars["{seed_market_signal}"] = orDash(t.MarketSignal.Level)
		vars["{seed_quality_signal}"] = orDash(t.QualitySignal.Level)
		vars["{seed_price_signal}"] = orDash(t.PriceSignal.Level)
	}

	return vars
}

// GetLookThroughPrompt 持仓穿透分析提示词。
// 新增的第四个入口 —— 穿透数据是工具最独特的产出，之前反而没有 AI 入口。
func (h *Handler) GetLookThroughPrompt(c *gin.Context) {
	in := aiprompt.LookThroughInput{AsOf: time.Now().Format("2006-01-02")}

	if ov, err := analysis.GetHoldingsOverlap(h.DB); err == nil && ov != nil {
		in.EffectiveFunds = ov.DiversificationScore
		in.AnalyzedFunds = ov.TotalFundsAnalyzed
		in.ReportDate = ov.ReportDate
		for _, s := range ov.OverlapStocks {
			in.OverlapStocks = append(in.OverlapStocks, aiprompt.OverlapLine{
				StockName: s.StockName, StockCode: s.StockCode,
				FundCount: len(s.AppearIn), ExposePct: s.TotalExposurePct,
			})
		}
	}
	if bd, err := analysis.GetIndustryBreakdown(h.DB); err == nil && bd != nil {
		if in.ReportDate == "" {
			in.ReportDate = bd.ReportDate
		}
		for _, x := range bd.Industries {
			in.Industries = append(in.Industries, aiprompt.IndustryLine{
				Industry: x.Industry, Pct: x.TotalWeight,
			})
		}
	}

	if in.AnalyzedFunds == 0 && len(in.Industries) == 0 {
		fail(c, http.StatusBadRequest, "还没有穿透数据，请先在「持仓透视」页点「抓取持仓数据」")
		return
	}

	ok(c, gin.H{
		"prompt":       aiprompt.BuildLookThroughReview(in),
		"title":        "持仓穿透分析",
		"generated_at": time.Now().Format("2006-01-02 15:04"),
	})
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// cleanTags 把 theme_tags 的 JSON 数组字符串转成可读文本。
// 库里存的是 ["新能源"] 这种形式，空数组 [] 直接显示出来很怪。
func cleanTags(raw string) string {
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return strings.TrimSpace(raw)
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, "、")
}
