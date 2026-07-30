package aiprompt

import (
	"fmt"
	"strings"
)

// 决策层面的三个场景：该不该加仓这只 / 赛道横评 / 审我的调仓方案。
//
// 赛道横评原来走的是 prompts/theme_analysis.md + 占位符替换，
// 按 Q5=C 整份搬进 Go —— 那份 md 的内容（三层择时、peer 对比、四个提问）
// 在这里逐段保留，只是不再需要 21 个占位符对得上号。

// StockLine 一只股票在基金里的权重
type StockLine struct {
	Name     string
	Code     string
	Weight   float64
	Industry string
}

// PeerLine 同主题的对比基金
type PeerLine struct {
	Code     string
	Name     string
	Category string
	Pct1y    float64
	HasRank  bool
}

// ValuationLine 一个宽基指数的估值
type ValuationLine struct {
	Name    string
	PE      float64
	PctRank float64
	Signal  string
	HasData bool
}

// TimingLine 三层择时信号
type TimingLine struct {
	Level          string
	CompositeScore int
	Market         string
	MarketPEPct    *float64
	Quality        string
	QualityScore   *float64
	Price          string
	PriceDeviation *float64
	Fragments      []string
	HasData        bool
}

// FundInput 单只基金相关场景（加仓判断 / 赛道横评）共用的输入
type FundInput struct {
	AsOf     string
	Code     string
	Name     string
	Category string
	Themes   string
	Inferred string

	// 我的持有情况（没持有则 Held=false）
	Held       bool
	Value      float64
	CostAmount float64
	WeightPct  float64
	ProfitRate float64

	// 工具算出的东西
	Score         float64
	ScoreRank     float64
	HasScore      bool
	Timing        TimingLine
	Valuations    []ValuationLine
	ReportDate    string
	TopStocks     []StockLine
	Peers         []PeerLine
	SuggestPct    float64
	SuggestAmount float64
	SuggestMin    float64
	SuggestMax    float64
	SuggestStep   []string
	SuggestWarn   []string
	HasSuggest    bool
}

// BuildAddPositionReview 生成「该不该加仓这只」提示词。
//
// 这是筛查里判断最高频的真实问题，之前工具完全没有对应入口 ——
// 用户只能自己把评分、择时、仓位建议一项项抄给 AI。
func BuildAddPositionReview(b *Builder, in FundInput) string {
	opt := b.Opt()
	sec := &Numberer{}

	intro := fmt.Sprintf(`# 加仓决策请求 —— %s（%s）

我在考虑要不要加仓这只基金。下面是我的本地工具整理的数据，截至 **%s**，
包含它的评分、三层择时信号、当前市场估值，以及我自己的持有情况。

%s`, in.Name, in.Code, in.AsOf, opt.RedactNote())

	// ── 基本信息 + 我的持仓 ──
	basic := []string{
		"- 代码：" + in.Code,
		"- 名称：" + in.Name,
		"- 类别：" + CategoryName(in.Category),
	}
	if in.Themes != "" {
		basic = append(basic, "- 主题标签："+in.Themes)
	}
	if in.Held {
		basic = append(basic, fmt.Sprintf("- 我已持有：**是**，占组合 **%s**，收益率 **%s**",
			Pct(in.WeightPct), PctSigned(in.ProfitRate)))
		if line := opt.AmountLine("持有市值", in.Value); line != "" {
			basic = append(basic, line)
		}
		if line := opt.AmountLine("持有成本", in.CostAmount); line != "" {
			basic = append(basic, line)
		}
	} else {
		basic = append(basic, "- 我已持有：**否**（这会是一笔新建仓）")
	}
	basicSec := Section(sec.Next("基金与我的持仓"), strings.Join(basic, "\n"))
	b.Include("fund", "基金基本信息", in.Code+" "+in.Name)

	// ── 评分 ──
	scoreSec := ""
	if in.HasScore {
		lines := []string{fmt.Sprintf("- 综合评分：**%s / 100**", Num(in.Score))}
		if in.ScoreRank > 0 {
			lines = append(lines, fmt.Sprintf("- 同类百分位：**%s**（数值越小越靠前）", Pct(in.ScoreRank)))
		}
		scoreSec = Section(sec.Next("工具算出的评分"), strings.Join(lines, "\n"))
		b.Include("score", "综合评分", Num(in.Score))
	} else {
		b.Gap(GapYellow, "这只基金没有质量评分",
			"多半是成立不足 1 年、净值数据量不够，只能等，无法加速")
	}

	// ── 三层择时 ──
	timingSec := timingSection(b, sec, in.Timing)

	// ── 市场估值 ──
	valSec := valuationSection(b, sec, in.Valuations)

	// ── 建议仓位 ──
	posSec := ""
	if in.HasSuggest {
		lines := []string{}
		if line := opt.AmountLine("工具建议的买入金额", in.SuggestAmount); line != "" {
			lines = append(lines, line)
			if in.SuggestMin > 0 || in.SuggestMax > 0 {
				lines = append(lines, fmt.Sprintf("- 建议区间：%s ~ %s",
					opt.AmountOr(in.SuggestMin, "—"), opt.AmountOr(in.SuggestMax, "—")))
			}
		}
		if in.SuggestPct > 0 {
			lines = append(lines, fmt.Sprintf("- 折合占组合：**%s**", Pct(in.SuggestPct)))
		}
		if len(in.SuggestStep) > 0 {
			lines = append(lines, "- 工具的推算过程（每一步都是透明的）：")
			for _, s := range in.SuggestStep {
				lines = append(lines, "  - "+s)
			}
		}
		for _, w := range in.SuggestWarn {
			lines = append(lines, "- ⚠️ "+w)
		}
		if len(lines) > 0 {
			posSec = Section(sec.Next("工具建议的仓位"), strings.Join(lines, "\n")+
				"\n\n⚠️ 口径说明：这个建议由类别缺口、分批系数、择时折扣和重叠折扣逐步推出，"+
				"是个机械算法，没有考虑当下的具体消息面。")
			b.Include("position", "建议仓位", Pct(in.SuggestPct))
		}
	}

	// ── 前十大持股 ──
	stockSec := stockSection(b, sec, in.ReportDate, in.TopStocks)

	qs := Questions(
		"现在加仓这只，你的判断是「可以」「再等等」还是「别加」？请直接给结论，然后说明是上面哪个数据支撑的。",
		"如果建议再等，具体等什么？请给可观测的触发条件（比如某个指标到什么数值），不要说「等市场企稳」这类没法验证的话。",
		"如果建议加，加多少合适？分几次？工具给的仓位建议你认同吗？",
		"这只基金现在最大的风险点是什么？如果加仓后三个月亏损超过 15%，最可能的原因会是什么？",
	)

	return Join(intro, basicSec, scoreSec, timingSec, valSec, posSec, stockSec,
		b.GapsSection(sec.Next("数据缺口说明")), qs, Footer())
}

// BuildThemeReview 生成「赛道横评」提示词。
//
// 原来的实现是一行 ReadFile 原样返回模板文件，于是用户复制到的是：
// 开头 484 字给开发者看的注释，加一堆没被替换的 {seed_name} {holdings_table} 占位符。
// Python 版有完整的 render_theme_analysis_prompt，Go 重写时整块漏掉了。
func BuildThemeReview(b *Builder, in FundInput) string {
	opt := b.Opt()
	sec := &Numberer{}

	intro := fmt.Sprintf(`# 赛道分析请求 —— %s（%s）

以下数据全部来自我的本地工具（基金季报 + 天天基金 API + 指数 PE 历史）整理成的结构化快照，
截至 **%s**。请基于这些数据回答最后的问题。

%s`, in.Name, in.Code, in.AsOf, opt.RedactNote())

	basic := []string{
		"- 代码：" + in.Code,
		"- 名称：" + in.Name,
		"- 类别：" + CategoryName(in.Category),
		"- 主题标签：" + orUnknown(in.Themes),
	}
	if in.Inferred != "" {
		basic = append(basic, "- 推断主题（无官方标签时按季报第一大行业推断）："+in.Inferred)
	}
	if in.Held {
		basic = append(basic, fmt.Sprintf("- 我已持有这只，占组合 **%s**", Pct(in.WeightPct)))
	}
	basicSec := Section(sec.Next("种子基金基本信息"), strings.Join(basic, "\n"))
	b.Include("fund", "种子基金", in.Code+" "+in.Name)

	stockSec := stockSection(b, sec, in.ReportDate, in.TopStocks)

	// ── peer 对比 ──
	peerRows := make([][]string, 0, len(in.Peers))
	rankMissing := 0
	for _, p := range in.Peers {
		rank := "—"
		if p.HasRank {
			rank = Pct(p.Pct1y)
		} else {
			rankMissing++
		}
		peerRows = append(peerRows, []string{p.Name, p.Code, CategoryName(p.Category), rank})
	}
	peerSec := Section(sec.Next("同主题 Peer 基金横向对比"),
		Table([]string{"基金", "代码", "类别", "近1年同类百分位"}, peerRows,
			"（未找到同主题的对比基金）")+
			"\n\n说明：匹配维度是行业与主题标签。百分位越小表示在同类基金里排名越靠前。")
	if len(in.Peers) > 0 {
		b.Include("peers", "同主题 peer", fmt.Sprintf("%d 只", len(in.Peers)))
	} else {
		b.Gap(GapGreen, "没找到同主题的对比基金",
			"把想对比的基金加入「关注列表」，工具抓到季报后就能参与匹配")
	}
	if rankMissing > 0 {
		b.Gap(GapGreen, fmt.Sprintf("%d 只 peer 没有同类排名", rankMissing),
			"在「设置」页点一次「刷新同类排名」")
	}

	valSec := valuationSection(b, sec, in.Valuations)
	timingSec := timingSection(b, sec, in.Timing)

	b.Gap(GapRed, "子行业 PE 分位（如光模块、创新药）",
		"工具只有宽基指数的 PE 历史，子行业估值建议查 lixinger.com 或东方财富行业估值页")
	b.Gap(GapRed, "基金经理任职履历与历史业绩",
		"工具不抓这块，建议查天天基金的基金经理详情页")

	held := ""
	if in.Held {
		held = fmt.Sprintf("注意我已经持有种子基金 %s，所以重叠度是一个考虑因素。", in.Code)
	}
	qs := Questions(
		"这个赛道现在处于哪个周期位置？（左侧、上涨中、顶部、下跌中段、底部）给出判断依据，要具体到上面的哪个数据，不要用「可能」「大概」这类对冲词。",
		"如果我现在想进场这个赛道，应该满足哪些先决条件？至少列 3 条，每条都要带可观测指标。",
		"第 3 节列出的 peer 基金里，如果只能选 1-2 只做进场标的，你会选哪只？理由是什么？"+held,
		"如果你的推荐 3 个月后亏损超过 10%，最可能的原因会是什么？帮我预先识别风险。",
	)

	return Join(intro, basicSec, stockSec, peerSec, valSec, timingSec,
		b.GapsSection(sec.Next("数据缺口说明")), qs, Footer())
}

// ─── 共用段落 ─────────────────────────────────────────────────────────────────

func stockSection(b *Builder, sec *Numberer, reportDate string, stocks []StockLine) string {
	if len(stocks) == 0 {
		b.Gap(GapGreen, "这只基金没有季报持仓数据",
			"把它加入「关注列表」，工具 24 小时内自动抓取；或在「持仓透视」页点「抓取持仓数据」")
		return Section(sec.Next("前十大持股"), "（本地暂无该基金的季报持仓数据）")
	}
	rows := make([][]string, 0, len(stocks))
	for _, s := range stocks {
		rows = append(rows, []string{s.Name, s.Code, Pct(s.Weight), orUnknown(s.Industry)})
	}
	b.Include("stocks", "季报前十大持股", fmt.Sprintf("%d 只 · 报告期 %s", len(stocks), orUnknown(reportDate)))
	return Section(sec.Next(fmt.Sprintf("前十大持股（报告期 %s）", orUnknown(reportDate))),
		Table([]string{"股票", "代码", "占净值", "行业"}, rows, ""))
}

func valuationSection(b *Builder, sec *Numberer, vals []ValuationLine) string {
	lines := make([]string, 0, len(vals))
	got := 0
	for _, v := range vals {
		if !v.HasData {
			lines = append(lines, fmt.Sprintf("- **%s**：本地暂无 PE 历史", v.Name))
			continue
		}
		got++
		lines = append(lines, fmt.Sprintf("- **%s**：PE %s，历史百分位 **%s**（近 10 年）%s",
			v.Name, Num(v.PE), Pct(v.PctRank), signalSuffix(v.Signal)))
	}
	if len(lines) == 0 {
		return ""
	}
	if got == 0 {
		b.Gap(GapGreen, "指数 PE 历史为空",
			"在「调仓建议」页点「刷新估值数据」抓一次")
	} else {
		b.Include("valuation", "指数估值分位", fmt.Sprintf("%d 个宽基", got))
	}
	return Section(sec.Next("当前市场估值"), strings.Join(lines, "\n"))
}

func timingSection(b *Builder, sec *Numberer, t TimingLine) string {
	if !t.HasData {
		b.Gap(GapYellow, "三层择时信号算不出来",
			"缺基金评分或净值历史，等数据积累后自动可用")
		return ""
	}
	lines := []string{
		fmt.Sprintf("- 综合结论：**%s**（综合分 %d）", signalText(t.Level), t.CompositeScore),
		"- 市场层（沪深300 PE 十年分位）：" + signalText(t.Market) + pctSuffix(t.MarketPEPct, "分位 "),
		"- 质量层（基金综合评分）：" + signalText(t.Quality) + numSuffix(t.QualityScore, "评分 "),
		"- 价格层（相对 20 日均线）：" + signalText(t.Price) + pctSuffix(t.PriceDeviation, "偏离 "),
	}
	for _, f := range t.Fragments {
		if strings.TrimSpace(f) != "" {
			lines = append(lines, "- 补充："+f)
		}
	}
	b.Include("timing", "三层择时信号", signalText(t.Level))
	return Section(sec.Next("三层择时信号"), strings.Join(lines, "\n")+
		"\n\n说明：三层分别看市场估值、基金自身质量、短期价格位置，"+
		"任一层「不利」都不代表必然下跌，只是胜率上的偏向。")
}

func signalText(level string) string {
	switch strings.ToLower(level) {
	case "favorable":
		return "有利"
	case "unfavorable":
		return "不利"
	case "neutral":
		return "中性"
	}
	return orUnknown(level)
}

func signalSuffix(sig string) string {
	if strings.TrimSpace(sig) == "" {
		return ""
	}
	return "，信号：" + signalText(sig)
}

func pctSuffix(v *float64, prefix string) string {
	if v == nil {
		return "（数据缺失）"
	}
	return "（" + prefix + Pct(*v) + "）"
}

func numSuffix(v *float64, prefix string) string {
	if v == nil {
		return "（数据缺失）"
	}
	return "（" + prefix + Num(*v) + "）"
}
