package aiprompt

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// 四个入口的提示词构建。每份都是「背景 → 结构化数据 → 具体提问」三段式（Q2）。
//
// 为什么值得这么费劲：用户直接问 AI「我该买什么基金」，AI 只能给通用建议。
// 而这里能给出穿透后的行业暴露、跨基金重叠持股、同类排名、PE 百分位 ——
// 这些是工具算出来的、别处拿不到的东西。提示词的价值全在这些数据上。

// HoldingLine 组合里的一只基金（只带比例，不带金额）
type HoldingLine struct {
	Code       string
	Name       string
	Category   string
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

// PortfolioInput 持仓体检提示词的输入
type PortfolioInput struct {
	AsOf       string
	Holdings   []HoldingLine
	Allocation map[string]float64 // 分类 → 占比
	Industries []IndustryLine
	RiskLevel  string // 用户设的风险偏好
}

// BuildPortfolioReview 生成「持仓体检」提示词（总览页的 AI 持仓解读）。
//
// 原来这个入口只输出「兴业一年持有债券A（014248）占用约 154184.53 元，分类 bond」——
// 有金额（不该发）、分类是英文、没有指令。
func BuildPortfolioReview(in PortfolioInput) string {
	if in.AsOf == "" {
		in.AsOf = time.Now().Format("2006-01-02")
	}

	intro := fmt.Sprintf(`# 基金持仓体检请求

我用一个本地工具整理了自己的基金持仓，下面是结构化的数据快照，截至 **%s**。
请基于这些数据回答文末的问题。数据里只有比例，没有金额。`, in.AsOf)

	// 分类配置
	allocRows := make([][]string, 0, len(in.Allocation))
	cats := make([]string, 0, len(in.Allocation))
	for c := range in.Allocation {
		cats = append(cats, c)
	}
	sort.Slice(cats, func(i, j int) bool { return in.Allocation[cats[i]] > in.Allocation[cats[j]] })
	for _, c := range cats {
		allocRows = append(allocRows, []string{CategoryName(c), Pct(in.Allocation[c])})
	}
	sec := &Numberer{}
	allocSec := Section(sec.Next("资产配置"), Table([]string{"类别", "占比"}, allocRows, "（无持仓）"))

	// 逐只持仓
	holdRows := make([][]string, 0, len(in.Holdings))
	for _, h := range in.Holdings {
		ret := PctSigned(h.ProfitRate)
		if h.NavStale {
			ret = "净值未更新"
		}
		holdRows = append(holdRows, []string{
			h.Name, h.Code, CategoryName(h.Category), Pct(h.WeightPct), ret,
		})
	}
	holdSec := Section(sec.Next("逐只持仓"),
		Table([]string{"基金", "代码", "类别", "占组合", "收益率"}, holdRows, "（无持仓）"))

	// 行业穿透
	indSec := ""
	if len(in.Industries) > 0 {
		rows := make([][]string, 0, len(in.Industries))
		for i, x := range in.Industries {
			if i >= 12 {
				break
			}
			rows = append(rows, []string{x.Industry, Pct(x.Pct)})
		}
		indSec = Section(sec.Next("穿透后的行业暴露（前 12）"),
			Table([]string{"行业", "占组合"}, rows, "")+
				"\n\n说明：这是把每只基金的季报持仓按其在组合中的权重加总得到的，反映实际的行业敞口。")
	}

	prefSec := ""
	if in.RiskLevel != "" {
		prefSec = Section(sec.Next("我的风险偏好"), riskLevelText(in.RiskLevel))
	}

	qs := Questions(
		"这个组合最突出的问题是什么？请按严重程度排序，最多说三条。",
		"资产配置是否与我的风险偏好匹配？如果不匹配，具体该怎么调？",
		"穿透后的行业暴露里，有没有我可能没意识到的过度集中？",
		"如果我下次要追加投入，你建议优先补哪个方向？为什么？",
	)

	return Join(intro, allocSec, holdSec, indSec, prefSec, qs, PrivacyNote)
}

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

// BuildSelectorReview 生成「配置缺口分析」提示词（选基页）。
//
// 原来这个入口输出的是 "# Selector Session xxx" + 一坨原始 JSON，没有任何指令。
func BuildSelectorReview(in SelectorInput) string {
	if in.AsOf == "" {
		in.AsOf = time.Now().Format("2006-01-02")
	}

	intro := fmt.Sprintf(`# 基金配置缺口分析请求

我用一个本地工具把基金持仓穿透到了底层行业，并做了一次配置诊断。
下面是数据快照，截至 **%s**。请基于这些数据回答文末的问题。`, in.AsOf)

	sec := &Numberer{}

	allocSec := ""
	if len(in.Allocation) > 0 {
		cats := make([]string, 0, len(in.Allocation))
		for c := range in.Allocation {
			cats = append(cats, c)
		}
		sort.Slice(cats, func(i, j int) bool { return in.Allocation[cats[i]] > in.Allocation[cats[j]] })
		parts := make([]string, 0, len(cats))
		for _, c := range cats {
			parts = append(parts, CategoryName(c)+" "+Pct(in.Allocation[c]))
		}
		allocSec = Section(sec.Next("我当前的配置"), strings.Join(parts, " / "))
	}

	// 缺口表。这里要如实说明口径 —— 否则 AI 会以为「建议 10%」是经过论证的目标。
	gapRows := make([][]string, 0, len(in.Gaps))
	for i, g := range in.Gaps {
		if i >= 15 {
			break
		}
		gapRows = append(gapRows, []string{g.Name, Pct(g.CurrentPct), Pct(g.SuggestPct)})
	}
	gapSec := Section(sec.Next("工具诊断出的「配置缺口」"),
		Table([]string{"行业", "当前暴露", "距 10% 基准"}, gapRows, "（无缺口）")+
			"\n\n⚠️ 口径说明：工具的算法是「凡穿透后暴露低于 10% 的行业都算缺口」。"+
			"这个基准是固定的 10%，并非经过论证的目标配置，"+
			fmt.Sprintf("所以列出的 %d 个缺口不可能同时补齐（合计会远超 100%%）。", len(in.Gaps))+
			"请把它当作「我在哪些行业买得少」的清单，而不是待办事项。")

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
	} else {
		candSec = Section(sec.Next("工具初筛出的候选"),
			"工具这次没能筛出候选基金（本地基金库的行业标签与上面的申万行业名对不上）。"+
				"所以下面的问题请基于我的配置结构本身来回答，不必局限于某几只具体基金。")
	}

	qs := Questions(
		"上面这些「缺口」里，哪 2-3 个是真正值得补的？哪些可以忽略？请说明判断依据。",
		"结合我的偏好，补这些方向应该用什么类型的基金（宽基指数 / 行业主题 / 主动混合）？",
		"我当前的配置结构有什么风险是这份缺口清单没体现出来的？",
		"如果分批建仓，你建议分几次、间隔多久？",
	)

	return Join(intro, allocSec, gapSec, ansSec, candSec, qs, PrivacyNote)
}

// LookThroughInput 持仓穿透提示词的输入
type LookThroughInput struct {
	AsOf           string
	ReportDate     string
	EffectiveFunds float64 // 等效独立基金数
	AnalyzedFunds  int
	OverlapStocks  []OverlapLine
	Industries     []IndustryLine
}

// OverlapLine 跨基金重叠持股
type OverlapLine struct {
	StockName string
	StockCode string
	FundCount int
	ExposePct float64
}

// BuildLookThroughReview 生成「持仓穿透分析」提示词（持仓透视页）。
// 这是新增的第四个入口 —— 穿透数据是工具最独特的产出，之前反而没有 AI 入口。
func BuildLookThroughReview(in LookThroughInput) string {
	if in.AsOf == "" {
		in.AsOf = time.Now().Format("2006-01-02")
	}

	intro := fmt.Sprintf(`# 基金持仓穿透分析请求

我用一个本地工具把持有的基金穿透到了底层股票（数据来自基金季报，报告期 **%s**），
下面是穿透结果。请基于这些数据回答文末的问题。`, in.ReportDate)

	sumParts := []string{
		fmt.Sprintf("- 已穿透分析的权益类基金：**%d 只**", in.AnalyzedFunds),
		fmt.Sprintf("- 等效独立基金数：**%s**（若为 2.5，意味着手里的基金实际只相当于 2.5 只互不重复的基金）", Num(in.EffectiveFunds)),
	}
	lsec := &Numberer{}
	sumSec := Section(lsec.Next("分散度"), strings.Join(sumParts, "\n"))

	ovRows := make([][]string, 0, len(in.OverlapStocks))
	for i, s := range in.OverlapStocks {
		if i >= 15 {
			break
		}
		ovRows = append(ovRows, []string{
			s.StockName, s.StockCode, fmt.Sprintf("%d 只", s.FundCount), Pct(s.ExposePct),
		})
	}
	ovSec := Section(lsec.Next("被多只基金同时持有的股票"),
		Table([]string{"股票", "代码", "被几只基金持有", "对组合的实质暴露"}, ovRows,
			"（没有跨基金重叠的股票）"))

	indSec := ""
	if len(in.Industries) > 0 {
		rows := make([][]string, 0, len(in.Industries))
		for i, x := range in.Industries {
			if i >= 12 {
				break
			}
			rows = append(rows, []string{x.Industry, Pct(x.Pct)})
		}
		indSec = Section(lsec.Next("穿透后的行业分布（前 12）"),
			Table([]string{"行业", "占组合"}, rows, ""))
	}

	qs := Questions(
		"我的等效独立基金数说明了什么？这个分散度对个人投资者算够还是不够？",
		"重叠持股里有没有需要警惕的情况？（比如某只股票通过多只基金累积了过高暴露）",
		"从行业分布看，这个组合实际押注的是什么？与「买了多只不同基金」的直觉是否一致？",
		"如果我想真正提高分散度，应该减掉哪只、补什么方向？",
	)

	return Join(intro, sumSec, ovSec, indSec, qs, PrivacyNote)
}

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
