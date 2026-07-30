package aiprompt

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// 场景注册表与统一返回结构。
//
// 产品稿：docs/product-specs/ai-everywhere.html
//
//	Q1 = B  修断裂 + 中枢 + P0/P1 共 8 个场景
//	Q4 = A  只复制，不开任何外链 —— 本包不产出任何 URL
//	Q5 = C  提示词全部写在 Go 源码里，不用模板文件
//	Q6 = A  组合类场景带全部持仓 + 全部穿透，不做 top-N 截断
//	Q7 = B  默认带真实金额，Options.Redact 为 true 时才脱敏
//	Q8 = C  结论可回填存档（ai_notes），但不与决策模块联动
//
// 为什么要有注册表：八个场景如果各写各的，「哪些数据必须齐才算 ready」
// 和「缺了怎么补」这两件事就会散在八个 handler 里，改一次漏一处。

// Group 场景分组，对应 AI 中心页面上的三张大卡
type Group string

const (
	GroupPortfolio Group = "组合层面"
	GroupDecision  Group = "决策层面"
	GroupReview    Group = "回溯层面"
)

// Scene 一个 AI 分析场景的静态定义
type Scene struct {
	ID    string
	Title string
	Icon  string
	Group Group
	Desc  string

	// NeedsCode / NeedsID：这个场景要不要先选一只基金 / 一条记录。
	// 前端据此决定场景卡上是「生成 →」还是「选择基金」。
	NeedsCode bool
	NeedsID   bool
}

// Scenes 八个场景。顺序即 AI 中心里的展示顺序。
var Scenes = []Scene{
	{
		ID: "portfolio_checkup", Title: "组合体检", Icon: "🩺", Group: GroupPortfolio,
		Desc: "全部持仓明细 + 收益 + 风险告警 + 行业穿透，让 AI 通读一遍给整体判断",
	},
	{
		ID: "risk_alerts", Title: "风险告警解读", Icon: "⚠️", Group: GroupPortfolio,
		Desc: "告警里哪几条真要动手、哪几条可以忍到下季度",
	},
	{
		ID: "holdings_overlap", Title: "重叠与集中度", Icon: "🔍", Group: GroupPortfolio,
		Desc: "跨基金重复持股的实质暴露、行业集中度 —— 我是不是买了几只一样的",
	},
	{
		ID: "fund_addposition", Title: "该不该加仓这只", Icon: "📈", Group: GroupDecision,
		Desc: "带上评分明细、三层择时、建议仓位和你的持有成本", NeedsCode: true,
	},
	{
		ID: "theme_analysis", Title: "赛道横评", Icon: "🛰️", Group: GroupDecision,
		Desc: "种子基金前十大持股 + 同主题 peer 排名 + 指数 PE 十年分位", NeedsCode: true,
	},
	{
		ID: "rebalance_review", Title: "审我的调仓方案", Icon: "⚖️", Group: GroupDecision,
		Desc: "把工具算出的调仓建议交给 AI 挑刺，而不是让它重出一版",
	},
	{
		ID: "selector_gap", Title: "补什么基金", Icon: "🧭", Group: GroupReview,
		Desc: "选基问卷答案 + 诊断出的配置缺口 + 候选打分明细", NeedsID: true,
	},
	{
		ID: "decision_review", Title: "决策事后复盘", Icon: "📒", Group: GroupReview,
		Desc: "一笔已记录的决策，当时的理由 vs 现在的结果", NeedsID: true,
	},
}

// SceneByID 查场景定义
func SceneByID(id string) (Scene, bool) {
	for _, s := range Scenes {
		if s.ID == id {
			return s, true
		}
	}
	return Scene{}, false
}

// ─── 数据清单与缺口 ───────────────────────────────────────────────────────────

// Included 「本次带给 AI 的数据」清单里的一行
type Included struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Detail string `json:"detail"`
}

// 缺口等级。这三类是产品里一以贯之的口径：
// 笼统说一句「建议补充数据」对用户没用，得说清是能自己补、只能等、还是根本不支持。
const (
	GapGreen  = "green"  // 可自助补，附操作步骤
	GapYellow = "yellow" // 等时间，说明原因
	GapRed    = "red"    // 工具不支持，给外部来源
)

// Gap 一条数据缺口
type Gap struct {
	Level string `json:"level"`
	Label string `json:"label"`
	Howto string `json:"howto"`
}

// Result 统一返回给前端的结构。
//
// included / gaps 单独返回而不是让前端去解析 prompt 正文 ——
// 否则每加一个场景，前端都得跟着改解析逻辑。
type Result struct {
	Scene     string     `json:"scene"`
	Title     string     `json:"title"`
	Prompt    string     `json:"prompt"`
	AsOf      string     `json:"as_of"`
	Chars     int        `json:"chars"`
	Included  []Included `json:"included"`
	Gaps      []Gap      `json:"gaps"`
	Ready     bool       `json:"ready"`
	ReadyNote string     `json:"ready_note"`
	Redacted  bool       `json:"redacted"`
}

// Builder 收集数据 + 拼提示词。每个场景一个。
type Builder struct {
	included []Included
	gaps     []Gap
	opt      Options
}

// NewBuilder 建一个 builder
func NewBuilder(opt Options) *Builder { return &Builder{opt: opt} }

// Opt 返回渲染选项（金额是否脱敏）
func (b *Builder) Opt() Options { return b.opt }

// Include 记一条「带上了什么数据」
func (b *Builder) Include(key, label, detail string) {
	b.included = append(b.included, Included{Key: key, Label: label, Detail: detail})
}

// Gap 记一条缺口
func (b *Builder) Gap(level, label, howto string) {
	b.gaps = append(b.gaps, Gap{Level: level, Label: label, Howto: howto})
}

// Finish 组装最终结果
func (b *Builder) Finish(scene Scene, asOf, prompt string, ready bool, readyNote string) Result {
	if b.included == nil {
		b.included = []Included{}
	}
	if b.gaps == nil {
		b.gaps = []Gap{}
	}
	return Result{
		Scene:     scene.ID,
		Title:     scene.Title,
		Prompt:    prompt,
		AsOf:      asOf,
		Chars:     utf8.RuneCountInString(prompt),
		Included:  b.included,
		Gaps:      b.gaps,
		Ready:     ready,
		ReadyNote: readyNote,
		Redacted:  b.opt.Redact,
	}
}

// GapsSection 把缺口清单写进提示词本身。
//
// 不只是给用户看的：AI 知道哪些空白是「工具查不到」而不是「用户没提供」，
// 才不会把「建议补充更多数据」当成一条主要结论敷衍过去。
func (b *Builder) GapsSection(title string) string {
	if len(b.gaps) == 0 {
		return ""
	}
	label := map[string]string{
		GapGreen:  "🟢 我可以自己补",
		GapYellow: "🟡 只能等时间",
		GapRed:    "🔴 工具拿不到",
	}
	lines := make([]string, 0, len(b.gaps)+3)
	lines = append(lines, "下面这些数据本次没能提供，已按「能不能补」分好类。请在回答里**不要**笼统建议我“补充更多数据”，")
	lines = append(lines, "对 🔴 类直接按缺失来推理并说明这会让结论弱在哪。", "")
	for _, g := range b.gaps {
		line := fmt.Sprintf("- **%s** —— %s", g.Label, label[g.Level])
		if g.Howto != "" {
			line += "：" + g.Howto
		}
		lines = append(lines, line)
	}
	return Section(title, strings.Join(lines, "\n"))
}
