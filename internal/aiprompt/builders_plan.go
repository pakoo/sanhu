package aiprompt

import (
	"fmt"
	"strings"
)

// 调仓审查与决策复盘。
//
// 这两个场景的共同点：工具已经给出了一份方案 / 已经记录了一笔决策，
// 交给 AI 的任务是**挑刺和复盘**，不是让它重出一版 ——
// 提问措辞上要把这一点写死，否则 AI 会忽略工具的方案自说自话。

// SuggestionLine 一条调仓建议
type SuggestionLine struct {
	Category   string
	CurrentPct float64
	TargetPct  float64
	GapPct     float64
	Action     string
	Amount     float64
	Candidates []string
	Note       string
}

// MonthlyLine 渐进过渡计划里的一个月
type MonthlyLine struct {
	Month int
	Buys  map[string]float64
}

// RebalanceInput 调仓审查的输入
type RebalanceInput struct {
	AsOf          string
	TotalValue    float64
	RiskLevel     string
	Suggestions   []SuggestionLine
	HasTarget     bool
	MonthlyInvest float64
	Months        int
	Plan          []MonthlyLine
	Valuations    []ValuationLine
}

// BuildRebalanceReview 生成「审我的调仓方案」提示词。
func BuildRebalanceReview(b *Builder, in RebalanceInput) string {
	opt := b.Opt()
	sec := &Numberer{}

	intro := fmt.Sprintf(`# 调仓方案审查请求

我的本地工具按我设定的目标配置算出了一份调仓建议，截至 **%s**。
**我要你做的是审查这份方案、找出它的问题**，而不是重新出一版 ——
如果你认为整体方向就是错的，请先说清错在哪，再给替代方案。

%s`, in.AsOf, opt.RedactNote())

	head := []string{}
	if line := opt.AmountLine("组合总市值", in.TotalValue); line != "" {
		head = append(head, line)
	}
	if in.RiskLevel != "" {
		head = append(head, "- 我设定的风险偏好："+riskLevelText(in.RiskLevel))
	}
	headSec := Section(sec.Next("背景"), strings.Join(head, "\n"))

	// ── 调仓建议表 ──
	headers := []string{"类别", "当前占比", "目标占比", "偏离", "动作", "候选基金", "说明"}
	if opt.Redact {
		headers = []string{"类别", "当前占比", "目标占比", "偏离", "动作", "候选基金", "说明"}
	} else {
		headers = []string{"类别", "当前占比", "目标占比", "偏离", "动作", "建议金额", "候选基金", "说明"}
	}
	rows := make([][]string, 0, len(in.Suggestions))
	for _, s := range in.Suggestions {
		base := []string{
			CategoryName(s.Category), Pct(s.CurrentPct), Pct(s.TargetPct),
			Pp(s.GapPct), actionText(s.Action),
		}
		if !opt.Redact {
			base = append(base, opt.AmountOr(s.Amount, "—"))
		}
		base = append(base, orUnknown(strings.Join(s.Candidates, "、")), s.Note)
		rows = append(rows, base)
	}
	sugSec := Section(sec.Next("工具算出的调仓建议"),
		Table(headers, rows, "（当前配置与目标一致，没有调仓建议）")+
			"\n\n⚠️ 口径说明：偏离为正表示超配、为负表示欠配。这份建议**只按类别占比**计算，"+
			"没有考虑各基金之间的持仓重叠，也没有考虑赎回费和持有期限制。")
	b.Include("suggestions", "调仓建议", fmt.Sprintf("%d 条", len(in.Suggestions)))

	if !in.HasTarget {
		b.Gap(GapGreen, "没有设定目标配置",
			"在「调仓建议」页设一次目标比例；否则这份建议是按默认模板算的，参考价值有限")
	}

	// ── 渐进过渡计划 ──
	planSec := ""
	if len(in.Plan) > 0 {
		lines := []string{fmt.Sprintf("按每月投入 %s、共 %d 个月的渐进方式过渡（不卖出、只靠新增资金摊平）：",
			opt.AmountOr(in.MonthlyInvest, "（金额已脱敏）"), in.Months)}
		for _, m := range in.Plan {
			parts := make([]string, 0, len(m.Buys))
			for _, c := range sortedKeys(m.Buys) {
				if m.Buys[c] <= 0 {
					continue
				}
				parts = append(parts, CategoryName(c)+" "+opt.AmountOr(m.Buys[c], Pct(m.Buys[c]/in.MonthlyInvest*100)))
			}
			if len(parts) == 0 {
				continue
			}
			lines = append(lines, fmt.Sprintf("- 第 %d 月：%s", m.Month, strings.Join(parts, " / ")))
		}
		planSec = Section(sec.Next("渐进过渡计划"), strings.Join(lines, "\n"))
		b.Include("plan", "渐进过渡计划", fmt.Sprintf("%d 个月", in.Months))
	}

	valSec := valuationSection(b, sec, in.Valuations)

	b.Gap(GapRed, "赎回费率与最短持有期",
		"工具不抓费率数据，卖出前请在基金详情页或支付宝确认，可能吃掉调仓收益")

	qs := Questions(
		"这份调仓方案有什么问题？请具体指出哪一条建议我不该照做，以及为什么。",
		"结合当前的市场估值，现在是执行这次调仓的好时机吗？还是应该只做一部分、等一等？",
		"「卖出超配的、买入欠配的」这个思路在当前位置成立吗？有没有更适合我的做法（比如只靠新增资金摊平、不卖出）？",
		"如果我只执行其中一条，你建议执行哪条？为什么是它？",
		"执行完这次调仓，我应该在什么条件下再次检查？给出可观测的触发条件。",
	)

	return Join(intro, headSec, sugSec, planSec, valSec,
		b.GapsSection(sec.Next("数据缺口说明")), qs, Footer())
}

// ─── 决策复盘 ─────────────────────────────────────────────────────────────────

// CurveLine 决策以来的净值点
type CurveLine struct {
	Date  string
	Value float64
}

// DecisionInput 决策复盘的输入
type DecisionInput struct {
	AsOf         string
	ID           int64
	Code         string
	Name         string
	Type         string
	Status       string
	CreatedAt    string
	ExecutedAt   string
	IsVirtual    bool
	Notes        string
	Rationale    string
	TargetAmount float64
	HasAmount    bool
	TPPct        float64
	SLPct        float64
	HasTP        bool
	HasSL        bool

	EntryNav   float64
	CurrentNav float64
	PnLPct     float64
	HasPerf    bool
	Curve      []CurveLine

	Timing     TimingLine
	Valuations []ValuationLine
}

// BuildDecisionReview 生成「决策事后复盘」提示词。
//
// 这个入口后端一直存在（/api/decisions/:id/export），但吐的是九行 key: value，
// 而且前端根本没有按钮调它 —— 等于白写。
func BuildDecisionReview(b *Builder, in DecisionInput) string {
	opt := b.Opt()
	sec := &Numberer{}

	kind := "真实决策"
	if in.IsVirtual {
		kind = "🧪 虚拟建仓（没有真花钱，用来验证判断）"
	}

	intro := fmt.Sprintf(`# 投资决策复盘请求 —— %s（%s）

我在 **%s** 做了一笔投资决策并记录在本地工具里。现在想复盘：**当时的判断对不对**。
下面是当时记下的理由和现在的结果，截至 **%s**。

请注意：我要复盘的是**决策过程的质量**，不只是结果好坏 ——
赚钱的决策也可能是运气，亏钱的决策也可能当时是对的。

%s`, in.Name, in.Code, in.CreatedAt, in.AsOf, opt.RedactNote())

	basic := []string{
		"- 决策类型：" + decisionTypeText(in.Type),
		"- 性质：" + kind,
		"- 状态：" + decisionStatusText(in.Status),
		"- 创建时间：" + in.CreatedAt,
	}
	if in.ExecutedAt != "" {
		basic = append(basic, "- 执行时间："+in.ExecutedAt)
	}
	if in.HasAmount {
		if line := opt.AmountLine("计划金额", in.TargetAmount); line != "" {
			basic = append(basic, line)
		}
	}
	if in.HasTP {
		basic = append(basic, fmt.Sprintf("- 设定的止盈线：**%s**", Pct(in.TPPct)))
	}
	if in.HasSL {
		basic = append(basic, fmt.Sprintf("- 设定的止损线：**%s**", Pct(in.SLPct)))
	}
	basicSec := Section(sec.Next("决策记录"), strings.Join(basic, "\n"))
	b.Include("decision", "决策记录", fmt.Sprintf("#%d %s", in.ID, in.Code))

	// ── 当时的理由 ──
	reason := strings.TrimSpace(in.Notes)
	if r := strings.TrimSpace(in.Rationale); r != "" {
		if reason != "" {
			reason += "\n\n"
		}
		reason += "工具记录的触发依据：" + r
	}
	reasonSec := ""
	if reason != "" {
		reasonSec = Section(sec.Next("我当时写下的理由"), reason)
		b.Include("rationale", "当时的理由", fmt.Sprintf("%d 字", len([]rune(reason))))
	} else {
		b.Gap(GapYellow, "这笔决策没有记录理由",
			"已经发生的事补不回来了；以后创建决策时填一句「为什么现在做」，复盘才有意义")
	}

	// ── 结果 ──
	perfSec := ""
	if in.HasPerf {
		lines := []string{
			fmt.Sprintf("- 建仓时净值：**%s**", Num(in.EntryNav)),
			fmt.Sprintf("- 当前净值：**%s**", Num(in.CurrentNav)),
			fmt.Sprintf("- 至今盈亏：**%s**", PctSigned(in.PnLPct)),
		}
		if n := len(in.Curve); n > 1 {
			lines = append(lines, fmt.Sprintf("- 净值走势：共 %d 个交易日，%s",
				n, curveShape(in.Curve)))
		}
		perfSec = Section(sec.Next("到目前为止的结果"), strings.Join(lines, "\n"))
		b.Include("performance", "决策以来的表现", PctSigned(in.PnLPct))
	} else {
		b.Gap(GapYellow, "还没有决策以来的净值数据",
			"需要建仓净值 + 后续净值，等工具抓够数据后自动可用")
	}

	timingSec := timingSection(b, sec, in.Timing)
	valSec := valuationSection(b, sec, in.Valuations)

	qs := Questions(
		"抛开结果不谈，**当时那个决策本身**的质量如何？理由充分吗？有没有明显的思维漏洞？",
		"如果结果是好的，有多少是判断对了、多少是运气？如果结果不好，是判断错了还是只是还没到时候？",
		"当时设的止盈/止损线合理吗？如果当时没设，现在该设在哪？",
		"这笔决策暴露出我在投资习惯上的什么问题？下次做同类决策，我应该先检查哪三件事？",
	)

	return Join(intro, basicSec, reasonSec, perfSec, timingSec, valSec,
		b.GapsSection(sec.Next("数据缺口说明")), qs, Footer())
}

func actionText(a string) string {
	switch strings.ToLower(a) {
	case "buy":
		return "买入"
	case "sell":
		return "卖出"
	case "hold":
		return "不动"
	}
	return orUnknown(a)
}

func decisionTypeText(t string) string {
	switch strings.ToLower(t) {
	case "buy", "add":
		return "买入 / 加仓"
	case "sell", "reduce":
		return "卖出 / 减仓"
	case "hold":
		return "继续持有"
	}
	return orUnknown(t)
}

func decisionStatusText(s string) string {
	switch strings.ToLower(s) {
	case "pending":
		return "待执行"
	case "executed":
		return "已执行"
	case "closed":
		return "已结束"
	case "cancelled", "canceled":
		return "已取消"
	}
	return orUnknown(s)
}

// curveShape 用一句话描述净值走势，省得把几百个点全塞进提示词。
// 提示词里塞完整曲线既费 token 又没人（包括 AI）会去逐点读。
func curveShape(curve []CurveLine) string {
	if len(curve) < 2 {
		return "数据点太少"
	}
	first, last := curve[0].Value, curve[len(curve)-1].Value
	min, max := first, first
	minAt, maxAt := curve[0].Date, curve[0].Date
	for _, p := range curve {
		if p.Value < min {
			min, minAt = p.Value, p.Date
		}
		if p.Value > max {
			max, maxAt = p.Value, p.Date
		}
	}
	drawdown := 0.0
	if max > 0 {
		drawdown = (min - max) / max * 100
	}
	return fmt.Sprintf("期间最高 %s（%s）、最低 %s（%s），期间最大回撤约 %s，最新 %s",
		Num(max), maxAt, Num(min), minAt, Pct(drawdown), Num(last))
}
