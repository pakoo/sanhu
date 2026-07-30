package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/aiprompt"
	"github.com/pakoo/sanhu/internal/analysis"
	"github.com/pakoo/sanhu/internal/decisions"
	"github.com/pakoo/sanhu/internal/position"
	"github.com/pakoo/sanhu/internal/rebalance"
	"github.com/pakoo/sanhu/internal/risk"
	"github.com/pakoo/sanhu/internal/scoring"
	"github.com/pakoo/sanhu/internal/selector"
	"github.com/pakoo/sanhu/internal/timing"
)

// AI 提示词的统一入口。
//
// 一个端点覆盖全部场景：GET /api/ai/prompts/:scene?code=&id=&redact=
// 旧的四个端点（/api/ai/context、/funds/:code/ai-prompts/theme-analysis、
// /selector/export/:id、/decisions/:id/export）保留并内部转发，
// 免得已经发出去的前端和用户书签一起断掉。
//
// 每个场景做三件事：装配数据 → 判断够不够 ready → 渲染。
// ready 判定必须留在这里而不是散进各 builder：前端要在**生成之前**就知道
// 这张场景卡该不该点亮（AI 中心的数据齐备度徽章）。

// GetAIPrompt 统一提示词端点
func (h *Handler) GetAIPrompt(c *gin.Context) {
	sceneID := c.Param("scene")
	scene, found := aiprompt.SceneByID(sceneID)
	if !found {
		fail(c, http.StatusNotFound, "未知的分析场景："+sceneID)
		return
	}

	opt := aiprompt.Options{Redact: c.Query("redact") == "1"}
	b := aiprompt.NewBuilder(opt)
	asOf := time.Now().Format("2006-01-02 15:04")

	var res aiprompt.Result
	var err error

	switch scene.ID {
	case "portfolio_checkup":
		res, err = h.scenePortfolioCheckup(b, scene, asOf)
	case "risk_alerts":
		res, err = h.sceneRiskAlerts(b, scene, asOf)
	case "holdings_overlap":
		res, err = h.sceneHoldingsOverlap(b, scene, asOf)
	case "fund_addposition":
		res, err = h.sceneFund(b, scene, asOf, c.Query("code"), false)
	case "theme_analysis":
		res, err = h.sceneFund(b, scene, asOf, c.Query("code"), true)
	case "rebalance_review":
		res, err = h.sceneRebalance(b, scene, asOf)
	case "selector_gap":
		res, err = h.sceneSelector(b, scene, asOf, c.Query("id"))
	case "decision_review":
		res, err = h.sceneDecision(b, scene, asOf, c.Query("id"))
	default:
		fail(c, http.StatusNotFound, "场景尚未实现："+scene.ID)
		return
	}

	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, res)
}

// GetAIScenes 返回场景列表 + 每个场景当前的数据齐备度，供 AI 中心渲染场景网格。
func (h *Handler) GetAIScenes(c *gin.Context) {
	holdings, _, _ := loadPortfolioHoldings(h.DB)
	held := 0
	for _, x := range holdings {
		if x.Value > 0 {
			held++
		}
	}

	analyzed, totalEquity := 0, 0
	if ov, err := analysis.GetHoldingsOverlap(h.DB); err == nil && ov != nil {
		analyzed = ov.TotalFundsAnalyzed
	}
	totalEquity = held

	var sessionCount, decisionCount int
	_ = h.DB.Get(&sessionCount, `SELECT COUNT(*) FROM selector_cache`)
	_ = h.DB.Get(&decisionCount, `SELECT COUNT(*) FROM decisions`)

	out := make([]gin.H, 0, len(aiprompt.Scenes))
	for _, s := range aiprompt.Scenes {
		ready, note := true, ""
		switch s.ID {
		case "portfolio_checkup", "risk_alerts", "rebalance_review":
			if held == 0 {
				ready, note = false, "还没有持仓数据"
			}
		case "holdings_overlap":
			switch {
			case held == 0:
				ready, note = false, "还没有持仓数据"
			case analyzed == 0:
				ready, note = false, "还没抓到任何季报持仓"
			case totalEquity > 0 && analyzed < totalEquity:
				note = formatCoverage(analyzed, totalEquity)
			}
		case "fund_addposition", "theme_analysis":
			// 需要先选一只基金，前端负责选，这里不判 ready
		case "selector_gap":
			if sessionCount == 0 {
				ready, note = false, "还没答过选基问卷"
			}
		case "decision_review":
			if decisionCount == 0 {
				ready, note = false, "还没有记录过决策"
			} else {
				note = formatCount(decisionCount, "笔可选")
			}
		}
		out = append(out, gin.H{
			"id": s.ID, "title": s.Title, "icon": s.Icon,
			"group": string(s.Group), "desc": s.Desc,
			"needs_code": s.NeedsCode, "needs_id": s.NeedsID,
			"ready": ready, "note": note,
		})
	}
	ok(c, gin.H{"scenes": out, "held_funds": held, "generated_at": time.Now().Format("2006-01-02 15:04")})
}

// ─── 各场景的数据装配 ─────────────────────────────────────────────────────────

func (h *Handler) scenePortfolioCheckup(b *aiprompt.Builder, scene aiprompt.Scene, asOf string) (aiprompt.Result, error) {
	holdings, total, err := loadPortfolioHoldings(h.DB)
	if err != nil {
		return aiprompt.Result{}, err
	}
	if len(holdings) == 0 || total <= 0 {
		return b.Finish(scene, asOf, "", false,
			"还没有持仓数据。先在「总览」录入持仓，这个场景的价值全在你的真实持仓上。"), nil
	}

	in := aiprompt.PortfolioInput{
		AsOf:       asOf,
		TotalValue: total,
		Allocation: map[string]float64{},
		Target:     map[string]float64{},
		RiskLevel:  h.currentRiskLevel(),
	}
	for _, item := range holdings {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = item.Code
		}
		weight := item.Value / total * 100
		profitRate := 0.0
		if item.CostAmount > 0 {
			profitRate = (item.Value - item.CostAmount) / item.CostAmount * 100
		}
		in.TotalCost += item.CostAmount
		in.Holdings = append(in.Holdings, aiprompt.HoldingLine{
			Code: item.Code, Name: name, Category: item.Category,
			Value: item.Value, CostAmount: item.CostAmount,
			WeightPct: weight, ProfitRate: profitRate,
			NavStale: item.CostAmount > 0 && item.Value == item.CostAmount,
		})
		in.Allocation[item.Category] += weight
	}

	sort.Slice(in.Holdings, func(i, j int) bool { return in.Holdings[i].Value > in.Holdings[j].Value })

	if targets, err := rebalance.GetTargetAllocation(h.DB); err == nil {
		for _, t := range targets {
			in.Target[t.Category] = t.TargetPct
		}
	}

	if ra, err := risk.RiskAnalysisAll(h.DB, toRiskInputs(holdings), total); err == nil && ra != nil {
		in.RiskScore = ra.Score
		for _, a := range ra.Alerts {
			in.Alerts = append(in.Alerts, aiprompt.AlertLine{
				Level: a.Level, Category: a.Category, Message: a.Message, Detail: a.Detail,
			})
		}
	}

	if bd, err := analysis.GetIndustryBreakdown(h.DB); err == nil && bd != nil {
		for _, x := range bd.Industries {
			in.Industries = append(in.Industries, aiprompt.IndustryLine{
				Industry: x.Industry, Pct: x.TotalWeight,
			})
			in.IndCoverage += x.TotalWeight
		}
	}

	if in.IndCoverage > 0 && in.IndCoverage < 60 {
		b.Gap(aiprompt.GapGreen,
			fmt.Sprintf("行业穿透只覆盖了组合的 %.1f%%", in.IndCoverage),
			"多为债券/货币类基金没有股票持仓（正常），其余可在「持仓透视」页点「抓取持仓数据」补齐")
	}

	return b.Finish(scene, asOf, aiprompt.BuildPortfolioReview(b, in), true, ""), nil
}

func (h *Handler) sceneRiskAlerts(b *aiprompt.Builder, scene aiprompt.Scene, asOf string) (aiprompt.Result, error) {
	holdings, total, err := loadPortfolioHoldings(h.DB)
	if err != nil {
		return aiprompt.Result{}, err
	}
	if len(holdings) == 0 || total <= 0 {
		return b.Finish(scene, asOf, "", false, "还没有持仓数据，算不出风险告警。"), nil
	}

	ra, err := risk.RiskAnalysisAll(h.DB, toRiskInputs(holdings), total)
	if err != nil {
		return aiprompt.Result{}, err
	}

	in := aiprompt.RiskInput{
		AsOf: asOf, RiskLevel: h.currentRiskLevel(),
		Allocation: map[string]float64{},
	}
	nameOf := map[string]string{}
	for _, item := range holdings {
		in.Allocation[item.Category] += item.Value / total * 100
		nameOf[item.Code] = strings.TrimSpace(item.Name)
	}
	if ra != nil {
		in.RiskScore = ra.Score
		in.PortfolioVol = ra.Volatility.PortfolioVol
		for _, a := range ra.Alerts {
			in.Alerts = append(in.Alerts, aiprompt.AlertLine{
				Level: a.Level, Category: a.Category, Message: a.Message, Detail: a.Detail,
			})
		}
		for code, d := range ra.Drawdowns {
			name := nameOf[code]
			if name == "" {
				name = code
			}
			in.Drawdowns = append(in.Drawdowns, aiprompt.DrawdownLine{
				Name: name, Code: code, MaxDrawdown: d.MaxDrawdown,
				PeakDate: d.PeakDate, TroughDate: d.TroughDate,
				Volatility: ra.Volatility.FundVols[code],
			})
		}
	}
	sortDrawdowns(in.Drawdowns)

	return b.Finish(scene, asOf, aiprompt.BuildRiskReview(b, in), true, ""), nil
}

func (h *Handler) sceneHoldingsOverlap(b *aiprompt.Builder, scene aiprompt.Scene, asOf string) (aiprompt.Result, error) {
	in := aiprompt.LookThroughInput{AsOf: asOf}

	holdings, total, _ := loadPortfolioHoldings(h.DB)
	for _, x := range holdings {
		if x.Value > 0 {
			in.TotalFunds++
		}
	}

	if ov, err := analysis.GetHoldingsOverlap(h.DB); err == nil && ov != nil {
		in.EffectiveFunds = ov.DiversificationScore
		in.AnalyzedFunds = ov.TotalFundsAnalyzed
		in.ReportDate = ov.ReportDate
		for _, s := range ov.OverlapStocks {
			in.OverlapStocks = append(in.OverlapStocks, aiprompt.OverlapLine{
				StockName: s.StockName, StockCode: s.StockCode,
				FundCount: len(s.AppearIn), ExposePct: s.TotalExposurePct,
				Funds: h.fundNamesOf(s.AppearIn),
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
			in.CoveragePct += x.TotalWeight
		}
	}
	_ = total

	if in.AnalyzedFunds == 0 && len(in.Industries) == 0 {
		return b.Finish(scene, asOf, "", false,
			"还没有任何季报穿透数据。这个场景的价值全在穿透上，"+
				"请先在「持仓透视」页点「抓取持仓数据」（约 40 秒）。"), nil
	}

	return b.Finish(scene, asOf, aiprompt.BuildLookThroughReview(b, in), true, ""), nil
}

func (h *Handler) sceneFund(b *aiprompt.Builder, scene aiprompt.Scene, asOf, code string, theme bool) (aiprompt.Result, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return b.Finish(scene, asOf, "", false, "这个场景需要先选一只基金。"), nil
	}

	in := h.fundPromptInput(asOf, code)
	if in.Name == "" {
		return b.Finish(scene, asOf, "", false,
			"本地基金库里没有 "+code+" 这只基金。先在「总览 → 关注列表」把它加进来。"), nil
	}

	if theme {
		in.Peers = h.peerLinesOf(code)
		return b.Finish(scene, asOf, aiprompt.BuildThemeReview(b, in), true, ""), nil
	}
	if sug, err := position.SuggestPosition(h.DB, code, h.currentRiskLevel()); err == nil && sug != nil {
		in.HasSuggest = true
		in.SuggestAmount = float64(sug.SuggestedAmount)
		in.SuggestMin, in.SuggestMax = float64(sug.RangeMin), float64(sug.RangeMax)
		if _, total, err := loadPortfolioHoldings(h.DB); err == nil && total > 0 {
			in.SuggestPct = in.SuggestAmount / total * 100
		}
		for _, s := range sug.Steps {
			step := s.Label
			if strings.TrimSpace(s.Detail) != "" {
				step += "（" + s.Detail + "）"
			}
			in.SuggestStep = append(in.SuggestStep, step)
		}
		in.SuggestWarn = sug.Warnings
	}
	return b.Finish(scene, asOf, aiprompt.BuildAddPositionReview(b, in), true, ""), nil
}

func (h *Handler) sceneRebalance(b *aiprompt.Builder, scene aiprompt.Scene, asOf string) (aiprompt.Result, error) {
	holdings, total, err := loadPortfolioHoldings(h.DB)
	if err != nil {
		return aiprompt.Result{}, err
	}
	if len(holdings) == 0 || total <= 0 {
		return b.Finish(scene, asOf, "", false, "还没有持仓数据，算不出调仓建议。"), nil
	}

	sugs, err := rebalance.GetRebalanceSuggestions(h.DB, toRebalanceInputs(holdings), total)
	if err != nil {
		return aiprompt.Result{}, err
	}

	in := aiprompt.RebalanceInput{
		AsOf: asOf, TotalValue: total, RiskLevel: h.currentRiskLevel(),
		Valuations: h.valuationLines(),
	}
	if targets, err := rebalance.GetTargetAllocation(h.DB); err == nil && len(targets) > 0 {
		in.HasTarget = true
	}
	for _, s := range sugs {
		in.Suggestions = append(in.Suggestions, aiprompt.SuggestionLine{
			Category: s.Category, CurrentPct: s.CurrentPct, TargetPct: s.TargetPct,
			GapPct: s.GapPct, Action: s.Action, Amount: s.AmountSuggest,
			Candidates: s.Candidates, Note: s.Note,
		})
	}

	// 渐进过渡计划：用组合市值的 5% 当月供估算，只是为了让 AI 看到「不卖出」这条路径长什么样
	monthly := total * 0.05
	if plan, err := rebalance.GradualTransitionPlan(h.DB, toRebalanceInputs(holdings), total, monthly, 6); err == nil && plan != nil {
		in.MonthlyInvest = plan.MonthlyInvest
		in.Months = plan.Months
		for _, p := range plan.Plans {
			in.Plan = append(in.Plan, aiprompt.MonthlyLine{Month: p.Month, Buys: p.BuyAmount})
		}
	}

	return b.Finish(scene, asOf, aiprompt.BuildRebalanceReview(b, in), true, ""), nil
}

func (h *Handler) sceneSelector(b *aiprompt.Builder, scene aiprompt.Scene, asOf, sessionID string) (aiprompt.Result, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		// 没指定就用最近一次问卷
		_ = h.DB.Get(&sessionID, `SELECT session_id FROM selector_cache ORDER BY created_at DESC LIMIT 1`)
	}
	if sessionID == "" {
		return b.Finish(scene, asOf, "", false,
			"还没答过选基问卷。先去「选基」页答一遍（约 1 分钟），才有缺口和候选可以分析。"), nil
	}

	row, err := loadSelectorSession(h, sessionID)
	if err == sql.ErrNoRows {
		return b.Finish(scene, asOf, "", false, "找不到这次问卷记录，请重新答一遍。"), nil
	}
	if err != nil {
		return aiprompt.Result{}, err
	}

	var answers selector.UserAnswers
	_ = json.Unmarshal([]byte(orEmptyJSON(row.AnswersJSON)), &answers)
	var gaps []selector.Gap
	_ = json.Unmarshal([]byte(orEmptyJSON(row.GapsJSON)), &gaps)
	var cands []selector.ScoredCandidate
	_ = json.Unmarshal([]byte(orEmptyJSON(row.CandidatesJSON)), &cands)

	in := aiprompt.SelectorInput{
		AsOf:       orFallback(row.CreatedAt, asOf),
		Allocation: map[string]float64{},
		Answers:    answerLabels(answers),
	}
	for _, g := range gaps {
		in.Gaps = append(in.Gaps, aiprompt.GapLine{
			Name: g.Theme, CurrentPct: g.CurrentPct, SuggestPct: g.DeficitPct,
		})
	}
	for _, cd := range cands {
		in.Candidates = append(in.Candidates, aiprompt.CandidateLine{
			Code: cd.Code, Name: cd.Name, Score: cd.Score,
			Reasons:     candidateReasons(cd),
			SourceLabel: sourceLabel(h.profileSource(cd.Code)),
		})
	}
	if holdings, total, err := loadPortfolioHoldings(h.DB); err == nil && total > 0 {
		for _, item := range holdings {
			in.Allocation[item.Category] += item.Value / total * 100
		}
	}

	return b.Finish(scene, asOf, aiprompt.BuildSelectorReview(b, in), true, ""), nil
}

func (h *Handler) sceneDecision(b *aiprompt.Builder, scene aiprompt.Scene, asOf, rawID string) (aiprompt.Result, error) {
	id, err := parseInt64Param(strings.TrimSpace(rawID))
	if err != nil {
		return b.Finish(scene, asOf, "", false, "这个场景需要先选一笔决策。"), nil
	}
	d, err := decisions.GetDecision(h.DB, id)
	if err != nil || d == nil {
		return b.Finish(scene, asOf, "", false, "找不到这笔决策记录。"), nil
	}

	in := aiprompt.DecisionInput{
		AsOf: asOf, ID: d.ID, Code: d.Code, Type: d.DecisionType,
		Status: d.Status, CreatedAt: d.CreatedAt, IsVirtual: d.IsVirtual == 1,
		Notes: strings.TrimSpace(d.Notes.String), Rationale: cleanRationale(d.RationaleJSON.String),
		ExecutedAt: d.ExecutedAt.String,
		Valuations: h.valuationLines(),
	}
	_ = h.DB.Get(&in.Name, `SELECT COALESCE(name,'') FROM funds WHERE code=?`, d.Code)
	if in.Name == "" {
		in.Name = d.Code
	}
	if d.TargetAmount.Valid {
		in.HasAmount, in.TargetAmount = true, d.TargetAmount.Float64
	}
	if d.TargetTPPct.Valid {
		in.HasTP, in.TPPct = true, d.TargetTPPct.Float64
	}
	if d.TargetSLPct.Valid {
		in.HasSL, in.SLPct = true, d.TargetSLPct.Float64
	}
	if perf, err := decisions.GetDecisionPerformance(h.DB, id); err == nil && perf != nil && perf.EntryNav > 0 {
		in.HasPerf = true
		in.EntryNav, in.CurrentNav, in.PnLPct = perf.EntryNav, perf.CurrentNav, perf.PnLPct
		for _, p := range perf.Curve {
			in.Curve = append(in.Curve, aiprompt.CurveLine{Date: p.Date, Value: p.Value})
		}
	}
	in.Timing = h.timingLine(d.Code)

	return b.Finish(scene, asOf, aiprompt.BuildDecisionReview(b, in), true, ""), nil
}

// ─── 共用取数 ─────────────────────────────────────────────────────────────────

// fundPromptInput 装配单只基金的全部数据（加仓判断与赛道横评共用）
func (h *Handler) fundPromptInput(asOf, code string) aiprompt.FundInput {
	in := aiprompt.FundInput{AsOf: asOf, Code: code, Valuations: h.valuationLines()}

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
		in.Name, in.Category = row.Name, row.Category
		in.Themes, in.Inferred = cleanTags(row.ThemeTags), row.TopIndustry
	}

	// 我的持有情况
	if holdings, total, err := loadPortfolioHoldings(h.DB); err == nil && total > 0 {
		for _, item := range holdings {
			if item.Code != code || item.Value <= 0 {
				continue
			}
			in.Held = true
			in.Value, in.CostAmount = item.Value, item.CostAmount
			in.WeightPct = item.Value / total * 100
			if item.CostAmount > 0 {
				in.ProfitRate = (item.Value - item.CostAmount) / item.CostAmount * 100
			}
		}
	}

	if s, err := scoring.GetFundScore(h.DB, code); err == nil && s != nil {
		in.HasScore, in.Score = true, s.TotalScore
	}
	in.Timing = h.timingLine(code)

	// 季报前十大持股
	var reportDate string
	_ = h.DB.Get(&reportDate, `SELECT COALESCE(MAX(report_date),'') FROM fund_holdings WHERE code=?`, code)
	in.ReportDate = reportDate
	if reportDate != "" {
		var stocks []struct {
			StockName string  `db:"stock_name"`
			StockCode string  `db:"stock_code"`
			Weight    float64 `db:"weight"`
			Industry  string  `db:"industry"`
		}
		_ = h.DB.Select(&stocks, `
			SELECT stock_name, stock_code, COALESCE(weight,0) AS weight, COALESCE(industry,'') AS industry
			FROM fund_holdings WHERE code=? AND report_date=?
			ORDER BY weight DESC`, code, reportDate)
		for _, s := range stocks {
			in.TopStocks = append(in.TopStocks, aiprompt.StockLine{
				Name: s.StockName, Code: s.StockCode, Weight: s.Weight, Industry: s.Industry,
			})
		}
	}
	return in
}

func (h *Handler) peerLinesOf(code string) []aiprompt.PeerLine {
	peerCodes, _ := selector.FindPeerFundsByTheme(h.DB, code, 8)
	out := make([]aiprompt.PeerLine, 0, len(peerCodes))
	for _, pc := range peerCodes {
		var p struct {
			Name     string          `db:"name"`
			Category string          `db:"category"`
			Pct1y    sql.NullFloat64 `db:"pct_1y"`
		}
		if err := h.DB.Get(&p, `
			SELECT COALESCE(f.name,'') AS name, COALESCE(f.category,'') AS category,
			       (SELECT pct_1y FROM fund_peer_ranks WHERE code=f.code LIMIT 1) AS pct_1y
			FROM funds f WHERE f.code=?`, pc); err != nil {
			continue
		}
		line := aiprompt.PeerLine{Code: pc, Name: orDash(p.Name), Category: p.Category}
		if p.Pct1y.Valid {
			line.HasRank, line.Pct1y = true, p.Pct1y.Float64
		}
		out = append(out, line)
	}
	return out
}

// valuationLines 沪深300 / 中证500 的 PE 分位
func (h *Handler) valuationLines() []aiprompt.ValuationLine {
	specs := []struct{ code, name string }{
		{"hs300", "沪深300"}, {"csi500", "中证500"},
	}
	out := make([]aiprompt.ValuationLine, 0, len(specs))
	for _, s := range specs {
		v := aiprompt.ValuationLine{Name: s.name}
		res := pePercentile(h.DB, s.code)
		if pe, okv := res["current_pe"].(float64); okv {
			v.PE, v.HasData = pe, true
		}
		if pct, okv := res["percentile"].(float64); okv {
			v.PctRank = pct
		}
		if sig, okv := res["signal"].(string); okv {
			v.Signal = sig
		}
		out = append(out, v)
	}
	return out
}

func (h *Handler) timingLine(code string) aiprompt.TimingLine {
	t, err := timing.GetTimingSignal(h.DB, code)
	if err != nil || t == nil {
		return aiprompt.TimingLine{}
	}
	return aiprompt.TimingLine{
		HasData: true, Level: t.Level, CompositeScore: t.CompositeScore,
		Market: t.MarketSignal.Level, MarketPEPct: t.MarketSignal.PEPct,
		Quality: t.QualitySignal.Level, QualityScore: t.QualitySignal.TotalScore,
		Price: t.PriceSignal.Level, PriceDeviation: t.PriceSignal.DeviationPct,
		Fragments: t.Fragments,
	}
}

// currentRiskLevel 读用户设的风险偏好，读不到按稳健处理
func (h *Handler) currentRiskLevel() string {
	values, err := getSettingsCompat(h.DB)
	if err != nil {
		return "moderate"
	}
	if v := values["risk_level"]; v != "" {
		return v
	}
	return "moderate"
}

// fundNamesOf 把基金代码换成名字 —— AI 看一串 6 位数字认不出是哪只基金
func (h *Handler) fundNamesOf(codes []string) []string {
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		var name string
		_ = h.DB.Get(&name, `SELECT COALESCE(name,'') FROM funds WHERE code=?`, c)
		if strings.TrimSpace(name) == "" {
			name = c
		}
		out = append(out, name)
	}
	return out
}

// sortDrawdowns 按回撤深浅排，最惨的排最前 —— map 遍历顺序随机，
// 不排的话同一份数据每次生成的提示词顺序都不一样，用户会以为数据变了
func sortDrawdowns(list []aiprompt.DrawdownLine) {
	sort.Slice(list, func(i, j int) bool { return list[i].MaxDrawdown < list[j].MaxDrawdown })
}

// formatCoverage / formatCount 场景卡徽章上的一句话
func formatCoverage(done, total int) string {
	return fmt.Sprintf("%d/%d 有季报", done, total)
}

func formatCount(n int, suffix string) string {
	return fmt.Sprintf("%d %s", n, suffix)
}

// ─── 旧端点转发（保持既有前端与书签可用）─────────────────────────────────────

// GetAIContext 旧的「AI持仓解读」端点，转发到 portfolio_checkup 场景
func (h *Handler) GetAIContext(c *gin.Context) {
	h.forwardScene(c, "portfolio_checkup", nil)
}

// GetFundAIThemePrompt 旧的赛道分析端点，转发到 theme_analysis 场景
func (h *Handler) GetFundAIThemePrompt(c *gin.Context) {
	h.forwardScene(c, "theme_analysis", gin.H{"code": c.Param("code")})
}

// GetLookThroughPrompt 持仓穿透端点，转发到 holdings_overlap 场景
func (h *Handler) GetLookThroughPrompt(c *gin.Context) {
	h.forwardScene(c, "holdings_overlap", nil)
}

// forwardScene 用旧端点的字段名包一层，避免已发布的前端断掉。
// context 是「AI持仓解读」用的旧字段，prompt 是新字段，两个都给。
func (h *Handler) forwardScene(c *gin.Context, sceneID string, extra gin.H) {
	scene, _ := aiprompt.SceneByID(sceneID)
	opt := aiprompt.Options{Redact: c.Query("redact") == "1"}
	b := aiprompt.NewBuilder(opt)
	asOf := time.Now().Format("2006-01-02 15:04")

	var res aiprompt.Result
	var err error
	switch sceneID {
	case "portfolio_checkup":
		res, err = h.scenePortfolioCheckup(b, scene, asOf)
	case "theme_analysis":
		code, _ := extra["code"].(string)
		res, err = h.sceneFund(b, scene, asOf, code, true)
	case "holdings_overlap":
		res, err = h.sceneHoldingsOverlap(b, scene, asOf)
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if !res.Ready {
		fail(c, http.StatusBadRequest, res.ReadyNote)
		return
	}
	ok(c, gin.H{
		"context":      res.Prompt, // 旧字段名，前端契约不变
		"prompt":       res.Prompt,
		"title":        res.Title,
		"included":     res.Included,
		"gaps":         res.Gaps,
		"chars":        res.Chars,
		"generated_at": res.AsOf,
	})
}

// ─── 小工具 ───────────────────────────────────────────────────────────────────

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func orFallback(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
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

// cleanRationale 决策的 rationale_json 是工具生成的触发依据，
// 直接把 JSON 塞给 AI 不如翻成一行人话
func cleanRationale(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		bs, err := json.Marshal(v)
		if err != nil {
			continue
		}
		parts = append(parts, k+"="+strings.Trim(string(bs), `"`))
	}
	sort.Strings(parts)
	return strings.Join(parts, "，")
}
