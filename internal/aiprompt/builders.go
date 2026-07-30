package aiprompt

import (
	"fmt"
	"sort"
	"strings"
)

// 组合层面的三个场景：组合体检 / 风险告警解读 / 重叠与集中度。
//
// 为什么值得这么费劲：用户直接问 AI「我该买什么基金」，AI 只能给通用建议。
// 而这里能给出穿透后的行业暴露、跨基金重叠持股、同类排名、PE 百分位 ——
// 这些是工具算出来的、别处拿不到的东西。提示词的价值全在这些数据上。
//
// Q6 = A：组合类场景带**全部**持仓和**全部**穿透行业，不做 top-N 截断。
// 尾部小仓位恰恰是「买重了没发现」的高发区，截断掉就看不见了。

// HoldingLine 组合里的一只基金
type HoldingLine struct {
	Code       string
	Name       string
	Category   string
	Value      float64 // 市值
	CostAmount float64 // 成本
	WeightPct  float64 // 占组合比例
	ProfitRate float64 // 收益率 %
	NavStale   bool
}

// IndustryLine 穿透后的行业暴露
type IndustryLine struct {
	Industry string
	Pct      float64
}

// GapLine 配置缺口
type GapLine struct {
	Name       string
	CurrentPct float64
	SuggestPct float64
}

// AlertLine 一条风险告警
type AlertLine struct {
	Level    string // high / medium / low
	Category string
	Message  string
	Detail   string
}

// DrawdownLine 一只基金的最大回撤
type DrawdownLine struct {
	Name        string
	Code        string
	MaxDrawdown float64
	PeakDate    string
	TroughDate  string
	Volatility  float64
}

// PortfolioInput 组合体检的输入
type PortfolioInput struct {
	AsOf        string
	TotalValue  float64
	TotalCost   float64
	Holdings    []HoldingLine
	Allocation  map[string]float64 // 分类 → 占比
	Target      map[string]float64 // 目标配置（可空）
	Industries  []IndustryLine
	IndCoverage float64 // 穿透覆盖了多少比例的市值
	RiskLevel   string
	RiskScore   float64
	Alerts      []AlertLine
}

// BuildPortfolioReview 生成「组合体检」提示词（总览页 + AI 中心）。
//
// 原来这个入口只输出「兴业一年持有债券A（014248）占用约 154184.53 元，分类 bond」——
// 前 5 只、分类是英文、没有任何指令，AI 收到只会反问「你想让我分析什么」。
func BuildPortfolioReview(b *Builder, in PortfolioInput) string {
	opt := b.Opt()
	sec := &Numberer{}

	intro := fmt.Sprintf(`# 基金组合体检请求

我用一个本地工具整理了自己的基金持仓，并把每只基金按季报穿透到了底层行业。
下面是结构化的数据快照，截至 **%s**。请基于这些数据回答文末的问题。

%s`, in.AsOf, opt.RedactNote())

	// ── 总览 ──
	overview := []string{}
	if line := opt.AmountLine("总市值", in.TotalValue); line != "" {
		overview = append(overview, line)
	}
	if in.TotalCost > 0 {
		profit := in.TotalValue - in.TotalCost
		rate := profit / in.TotalCost * 100
		if line := opt.AmountLine("累计收益", profit); line != "" {
			overview = append(overview, line)
		}
		overview = append(overview, fmt.Sprintf("- 累计收益率：**%s**", PctSigned(rate)))
	}
	overview = append(overview, fmt.Sprintf("- 持仓只数：**%d 只**", len(in.Holdings)))
	if in.RiskScore > 0 {
		overview = append(overview, fmt.Sprintf("- 工具算出的风险分：**%s / 100**（越高越激进）", Num(in.RiskScore)))
	}
	if in.RiskLevel != "" {
		overview = append(overview, "- 我设定的风险偏好："+riskLevelText(in.RiskLevel))
	}
	overviewSec := Section(sec.Next("组合总览"), strings.Join(overview, "\n"))
	b.Include("overview", "组合总览", fmt.Sprintf("%d 只持仓", len(in.Holdings)))

	// ── 资产配置（含目标偏离）──
	cats := sortedKeys(in.Allocation)
	allocRows := make([][]string, 0, len(cats))
	hasTarget := len(in.Target) > 0
	for _, c := range cats {
		row := []string{CategoryName(c), Pct(in.Allocation[c])}
		if hasTarget {
			t := in.Target[c]
			row = append(row, Pct(t), Pp(in.Allocation[c]-t))
		}
		allocRows = append(allocRows, row)
	}
	allocHeaders := []string{"类别", "当前占比"}
	if hasTarget {
		allocHeaders = append(allocHeaders, "目标占比", "偏离")
	}
	allocSec := Section(sec.Next("资产配置"), Table(allocHeaders, allocRows, "（无持仓）"))
	if hasTarget {
		b.Include("allocation", "资产配置与目标偏离", fmt.Sprintf("%d 个类别", len(cats)))
	} else {
		b.Include("allocation", "资产配置", fmt.Sprintf("%d 个类别", len(cats)))
		b.Gap(GapGreen, "没有设定目标配置", "在「调仓建议」页设一次目标比例，之后 AI 才能判断偏离是否需要纠正")
	}

	// ── 逐只持仓（全量，Q6=A）──
	holdHeaders := []string{"基金", "代码", "类别", "占组合", "收益率"}
	if !opt.Redact {
		holdHeaders = []string{"基金", "代码", "类别", "市值", "占组合", "成本", "收益率"}
	}
	holdRows := make([][]string, 0, len(in.Holdings))
	staleCount := 0
	for _, h := range in.Holdings {
		ret := PctSigned(h.ProfitRate)
		if h.NavStale {
			ret = "净值未更新"
			staleCount++
		}
		if opt.Redact {
			holdRows = append(holdRows, []string{
				h.Name, h.Code, CategoryName(h.Category), Pct(h.WeightPct), ret,
			})
			continue
		}
		holdRows = append(holdRows, []string{
			h.Name, h.Code, CategoryName(h.Category),
			opt.AmountOr(h.Value, "—"), Pct(h.WeightPct), opt.AmountOr(h.CostAmount, "—"), ret,
		})
	}
	holdSec := Section(sec.Next("逐只持仓（全部，按市值降序）"),
		Table(holdHeaders, holdRows, "（无持仓）"))
	b.Include("holdings", "持仓明细", fmt.Sprintf("%d 只 · 全量未截断", len(in.Holdings)))
	if staleCount > 0 {
		b.Gap(GapGreen, fmt.Sprintf("%d 只基金净值未更新", staleCount),
			"在顶部点「刷新数据」重抓一次；若刚过节假日则等下一个交易日")
	}

	// ── 风险告警 ──
	alertSec := ""
	if len(in.Alerts) > 0 {
		rows := make([][]string, 0, len(in.Alerts))
		for _, a := range in.Alerts {
			rows = append(rows, []string{alertLevelText(a.Level), a.Category, a.Message, a.Detail})
		}
		alertSec = Section(sec.Next("工具算出的风险告警"),
			Table([]string{"级别", "类型", "告警", "明细"}, rows, ""))
		b.Include("alerts", "风险告警", fmt.Sprintf("%d 条", len(in.Alerts)))
	}

	// ── 行业穿透（全量，Q6=A）──
	indSec := ""
	if len(in.Industries) > 0 {
		rows := make([][]string, 0, len(in.Industries))
		for _, x := range in.Industries {
			rows = append(rows, []string{x.Industry, Pct(x.Pct)})
		}
		note := "\n\n说明：这是把每只基金的季报持仓按其在组合中的权重加总得到的，反映实际的行业敞口，" +
			"而不是基金名字上写的主题。"
		if in.IndCoverage > 0 {
			note += fmt.Sprintf("本次穿透覆盖了组合市值的 **%s**。", Pct(in.IndCoverage))
		}
		indSec = Section(sec.Next("穿透后的行业暴露（全部）"),
			Table([]string{"行业", "占组合"}, rows, "")+note)
		b.Include("industries", "行业穿透", fmt.Sprintf("%d 个行业 · 覆盖 %s", len(in.Industries), Pct(in.IndCoverage)))
	} else {
		b.Gap(GapGreen, "没有任何季报穿透数据",
			"在「持仓透视」页点「抓取持仓数据」，约 40 秒；这是本工具最独特的数据，强烈建议补上")
	}

	qs := Questions(
		"这个组合最突出的问题是什么？请按严重程度排序，最多说三条，每条都要指明是上面哪个数据让你这么判断。",
		"资产配置与我的风险偏好是否匹配？如果不匹配，具体该调哪一类、调多少？",
		"穿透后的行业暴露里，有没有我可能没意识到的过度集中？（注意基金名字和实际持仓可能不是一回事）",
		"如果我下次追加投入，你建议优先补哪个方向？为什么？",
		"如果三个月后这个组合亏损超过 10%，最可能的原因会是什么？",
	)

	return Join(intro, overviewSec, allocSec, holdSec, alertSec, indSec,
		b.GapsSection(sec.Next("数据缺口说明")), qs, Footer())
}

// ─── 风险告警解读 ─────────────────────────────────────────────────────────────

// RiskInput 风险告警解读的输入
type RiskInput struct {
	AsOf         string
	RiskScore    float64
	RiskLevel    string
	Alerts       []AlertLine
	Drawdowns    []DrawdownLine
	PortfolioVol float64
	Allocation   map[string]float64
}

// BuildRiskReview 生成「风险告警解读」提示词。
//
// 工具能算出告警，但算不出「这条要不要现在动手」——
// 那需要结合市场位置和用户处境判断，正是该交给 AI 的部分。
func BuildRiskReview(b *Builder, in RiskInput) string {
	opt := b.Opt()
	sec := &Numberer{}

	intro := fmt.Sprintf(`# 组合风险告警解读请求

我的本地工具对基金组合跑了一轮风险检查，下面是它算出的告警和回撤数据，截至 **%s**。
工具只能按固定阈值报警，判断不了「这条现在要不要动手」—— 这正是我需要你帮忙的地方。

%s`, in.AsOf, opt.RedactNote())

	head := []string{
		fmt.Sprintf("- 组合风险分：**%s / 100**（越高越激进）", Num(in.RiskScore)),
	}
	if in.PortfolioVol > 0 {
		head = append(head, fmt.Sprintf("- 组合年化波动率：**%s**", Pct(in.PortfolioVol)))
	}
	if in.RiskLevel != "" {
		head = append(head, "- 我设定的风险偏好："+riskLevelText(in.RiskLevel))
	}
	headSec := Section(sec.Next("整体风险"), strings.Join(head, "\n"))

	alertRows := make([][]string, 0, len(in.Alerts))
	for _, a := range in.Alerts {
		alertRows = append(alertRows, []string{alertLevelText(a.Level), a.Category, a.Message, a.Detail})
	}
	alertSec := Section(sec.Next("告警明细"),
		Table([]string{"级别", "类型", "告警", "明细"}, alertRows, "（当前没有触发任何告警）")+
			"\n\n⚠️ 口径说明：这些告警是按**固定阈值**触发的（比如单只超过某个占比就报警），"+
			"不代表一定有问题，也没有考虑当前市场位置。请你判断哪些是真问题。")
	b.Include("alerts", "风险告警", fmt.Sprintf("%d 条", len(in.Alerts)))

	ddSec := ""
	if len(in.Drawdowns) > 0 {
		rows := make([][]string, 0, len(in.Drawdowns))
		for _, d := range in.Drawdowns {
			vol := "—"
			if d.Volatility > 0 {
				vol = Pct(d.Volatility)
			}
			rows = append(rows, []string{
				d.Name, d.Code, Pct(d.MaxDrawdown), d.PeakDate, d.TroughDate, vol,
			})
		}
		ddSec = Section(sec.Next("各基金最大回撤与波动"),
			Table([]string{"基金", "代码", "最大回撤", "峰值日", "谷底日", "年化波动"}, rows, ""))
		b.Include("drawdowns", "回撤与波动", fmt.Sprintf("%d 只", len(in.Drawdowns)))
	} else {
		b.Gap(GapYellow, "没有足够的净值历史算回撤",
			"需要至少数月的净值数据，工具会每天累积，无法加速")
	}

	allocSec := ""
	if len(in.Allocation) > 0 {
		parts := make([]string, 0, len(in.Allocation))
		for _, c := range sortedKeys(in.Allocation) {
			parts = append(parts, CategoryName(c)+" "+Pct(in.Allocation[c]))
		}
		allocSec = Section(sec.Next("当前配置"), strings.Join(parts, " / "))
	}

	qs := Questions(
		"上面这些告警，哪几条是**现在就该动手**的？哪几条可以忍到下个季度？请分成两组并说明依据。",
		"有没有哪条告警其实是误报 —— 即固定阈值不适用于我这种配置？",
		"回撤数据里，哪一只的表现最需要警惕？是它自身的问题，还是整个赛道的问题？",
		"如果我什么都不做，最坏情况下这个组合可能再跌多少？",
		"给我一个最小动作清单：只做一件事的话，做哪件收益最大？",
	)

	return Join(intro, headSec, alertSec, ddSec, allocSec,
		b.GapsSection(sec.Next("数据缺口说明")), qs, Footer())
}

// ─── 选基缺口 ─────────────────────────────────────────────────────────────────

// SelectorInput 选基诊断提示词的输入
type SelectorInput struct {
	AsOf       string
	Answers    map[string]string // 问卷：题干 → 答案文案
	Gaps       []GapLine
	Allocation map[string]float64
	Candidates []CandidateLine
}

// CandidateLine 工具算出的候选基金
type CandidateLine struct {
	Code, Name  string
	Score       float64
	Reasons     []string
	SourceLabel string
}

// BuildSelectorReview 生成「补什么基金」提示词（选基页）。
//
// 原来这个入口输出的是 "# Selector Session xxx" + 一坨原始 JSON，没有任何指令。
func BuildSelectorReview(b *Builder, in SelectorInput) string {
	opt := b.Opt()
	sec := &Numberer{}

	intro := fmt.Sprintf(`# 基金配置缺口分析请求

我用一个本地工具把基金持仓穿透到了底层行业，并做了一次配置诊断。
下面是数据快照，截至 **%s**。请基于这些数据回答文末的问题。

%s`, in.AsOf, opt.RedactNote())

	allocSec := ""
	if len(in.Allocation) > 0 {
		parts := make([]string, 0, len(in.Allocation))
		for _, c := range sortedKeys(in.Allocation) {
			parts = append(parts, CategoryName(c)+" "+Pct(in.Allocation[c]))
		}
		allocSec = Section(sec.Next("我当前的配置"), strings.Join(parts, " / "))
	}

	// 缺口表。这里要如实说明口径 —— 否则 AI 会以为「建议 10%」是经过论证的目标。
	gapRows := make([][]string, 0, len(in.Gaps))
	for _, g := range in.Gaps {
		gapRows = append(gapRows, []string{g.Name, Pct(g.CurrentPct), Pct(g.SuggestPct)})
	}
	gapSec := Section(sec.Next("工具诊断出的「配置缺口」"),
		Table([]string{"行业", "当前暴露", "距 10% 基准"}, gapRows, "（无缺口）")+
			"\n\n⚠️ 口径说明：工具的算法是「凡穿透后暴露低于 10% 的行业都算缺口」。"+
			"这个基准是固定的 10%，并非经过论证的目标配置，"+
			fmt.Sprintf("所以列出的 %d 个缺口不可能同时补齐（合计会远超 100%%）。", len(in.Gaps))+
			"请把它当作「我在哪些行业买得少」的清单，而不是待办事项。")
	b.Include("gaps", "配置缺口", fmt.Sprintf("%d 个行业", len(in.Gaps)))

	ansSec := ""
	if len(in.Answers) > 0 {
		keys := make([]string, 0, len(in.Answers))
		for k := range in.Answers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		lines := make([]string, 0, len(keys))
		for _, k := range keys {
			lines = append(lines, "- "+k+"："+in.Answers[k])
		}
		ansSec = Section(sec.Next("我的偏好（问卷答案）"), strings.Join(lines, "\n"))
		b.Include("answers", "问卷答案", fmt.Sprintf("%d 题", len(in.Answers)))
	}

	candSec := ""
	if len(in.Candidates) > 0 {
		rows := make([][]string, 0, len(in.Candidates))
		for _, c := range in.Candidates {
			rows = append(rows, []string{
				c.Name, c.Code, Num(c.Score), strings.Join(c.Reasons, "、"), c.SourceLabel,
			})
		}
		candSec = Section(sec.Next("工具初筛出的候选"),
			Table([]string{"基金", "代码", "评分", "入选理由", "数据来源"}, rows, ""))
		b.Include("candidates", "候选基金", fmt.Sprintf("%d 只", len(in.Candidates)))
	} else {
		candSec = Section(sec.Next("工具初筛出的候选"),
			"工具这次没能筛出候选基金（本地基金库的行业标签与上面的申万行业名对不上）。"+
				"所以下面的问题请基于我的配置结构本身来回答，不必局限于某几只具体基金。")
		b.Gap(GapGreen, "没能筛出候选基金",
			"把想考察的基金加入「关注列表」，工具抓到季报持仓后就能参与打分")
	}

	qs := Questions(
		"上面这些「缺口」里，哪 2-3 个是真正值得补的？哪些可以忽略？请说明判断依据。",
		"结合我的偏好，补这些方向应该用什么类型的基金（宽基指数 / 行业主题 / 主动混合）？",
		"我当前的配置结构有什么风险是这份缺口清单没体现出来的？",
		"如果分批建仓，你建议分几次、间隔多久？每次的触发条件是什么？",
	)

	return Join(intro, allocSec, gapSec, ansSec, candSec,
		b.GapsSection(sec.Next("数据缺口说明")), qs, Footer())
}

// ─── 持仓穿透 ─────────────────────────────────────────────────────────────────

// LookThroughInput 持仓穿透提示词的输入
type LookThroughInput struct {
	AsOf           string
	ReportDate     string
	EffectiveFunds float64 // 等效独立基金数
	AnalyzedFunds  int
	TotalFunds     int
	CoveragePct    float64
	OverlapStocks  []OverlapLine
	Industries     []IndustryLine
}

// OverlapLine 跨基金重叠持股
type OverlapLine struct {
	StockName string
	StockCode string
	FundCount int
	ExposePct float64
	Funds     []string
}

// BuildLookThroughReview 生成「重叠与集中度」提示词（持仓透视页）。
// 穿透数据是工具最独特的产出，之前反而没有 AI 入口。
func BuildLookThroughReview(b *Builder, in LookThroughInput) string {
	opt := b.Opt()
	sec := &Numberer{}

	intro := fmt.Sprintf(`# 基金持仓穿透分析请求

我用一个本地工具把持有的基金穿透到了底层股票（数据来自基金季报，报告期 **%s**），
下面是穿透结果。请基于这些数据回答文末的问题。

%s`, orUnknown(in.ReportDate), opt.RedactNote())

	sumParts := []string{
		fmt.Sprintf("- 已穿透分析的权益类基金：**%d 只**", in.AnalyzedFunds),
		fmt.Sprintf("- 等效独立基金数：**%s**（若为 2.5，意味着手里的基金实际只相当于 2.5 只互不重复的基金）", Num(in.EffectiveFunds)),
	}
	if in.CoveragePct > 0 {
		sumParts = append(sumParts, fmt.Sprintf("- 穿透覆盖了组合市值的 **%s**", Pct(in.CoveragePct)))
	}
	sumSec := Section(sec.Next("分散度"), strings.Join(sumParts, "\n"))
	b.Include("diversification", "分散度", fmt.Sprintf("已穿透 %d 只", in.AnalyzedFunds))

	ovRows := make([][]string, 0, len(in.OverlapStocks))
	for _, s := range in.OverlapStocks {
		ovRows = append(ovRows, []string{
			s.StockName, s.StockCode, fmt.Sprintf("%d 只", s.FundCount), Pct(s.ExposePct),
			strings.Join(s.Funds, "、"),
		})
	}
	ovSec := Section(sec.Next("被多只基金同时持有的股票（全部）"),
		Table([]string{"股票", "代码", "被几只基金持有", "对组合的实质暴露", "分别是"}, ovRows,
			"（没有跨基金重叠的股票）"))
	b.Include("overlap", "重叠持股", fmt.Sprintf("%d 只股票", len(in.OverlapStocks)))

	indSec := ""
	if len(in.Industries) > 0 {
		rows := make([][]string, 0, len(in.Industries))
		for _, x := range in.Industries {
			rows = append(rows, []string{x.Industry, Pct(x.Pct)})
		}
		indSec = Section(sec.Next("穿透后的行业分布（全部）"),
			Table([]string{"行业", "占组合"}, rows, ""))
		b.Include("industries", "行业分布", fmt.Sprintf("%d 个行业", len(in.Industries)))
	}

	if miss := in.TotalFunds - in.AnalyzedFunds; miss > 0 && in.TotalFunds > 0 {
		b.Gap(GapGreen, fmt.Sprintf("%d 只基金没有季报持仓数据", miss),
			"在「持仓透视」页点「抓取持仓数据」，或把它们加入关注列表后等 24 小时")
	}
	b.Gap(GapYellow, "季报持仓有滞后",
		fmt.Sprintf("报告期是 %s，基金实际调仓后要等下一季报才可见，无法加速", orUnknown(in.ReportDate)))

	qs := Questions(
		"我的等效独立基金数说明了什么？这个分散度对个人投资者算够还是不够？",
		"重叠持股里有没有需要警惕的情况？（比如某只股票通过多只基金累积了过高暴露）",
		"从行业分布看，这个组合实际押注的是什么？与「买了多只不同基金」的直觉是否一致？",
		"如果我想真正提高分散度，应该减掉哪只、补什么方向？请给出具体的基金代码。",
	)

	return Join(intro, sumSec, ovSec, indSec,
		b.GapsSection(sec.Next("数据缺口说明")), qs, Footer())
}

// ─── 小工具 ───────────────────────────────────────────────────────────────────

func riskLevelText(level string) string {
	switch strings.ToLower(level) {
	case "low", "conservative":
		return "保守型 —— 尽量避免亏损，能接受较低收益"
	case "high", "aggressive":
		return "进取型 —— 愿意承受较大波动换取更高收益"
	default:
		return "稳健型 —— 能接受一定回撤"
	}
}

func alertLevelText(level string) string {
	switch strings.ToLower(level) {
	case "high":
		return "高"
	case "medium":
		return "中"
	case "low":
		return "低"
	}
	return orUnknown(level)
}

// sortedKeys 按值降序排 key，让占比大的排前面
func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] == m[keys[j]] {
			return keys[i] < keys[j]
		}
		return m[keys[i]] > m[keys[j]]
	})
	return keys
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "未知"
	}
	return s
}
