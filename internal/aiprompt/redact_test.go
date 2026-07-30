package aiprompt

import (
	"strings"
	"testing"
)

// 金额脱敏是隐私开关（产品稿 Q7=B：默认带金额，可一键脱敏）。
// 这组测试的意义在于：只要哪个 builder 忘了走 Options.Amount() 而直接 fmt 了金额，
// 开关就形同虚设 —— 靠人眼 review 迟早会漏，所以钉在测试里。

func portfolioFixture() PortfolioInput {
	return PortfolioInput{
		AsOf:       "2026-07-30",
		TotalValue: 213261.70,
		TotalCost:  211000,
		Allocation: map[string]float64{"bond": 72.3, "equity": 27.7},
		Holdings: []HoldingLine{
			{Code: "014248", Name: "兴业一年持有债券A", Category: "bond",
				Value: 154184.53, CostAmount: 150000, WeightPct: 72.3, ProfitRate: 2.79},
			{Code: "006195", Name: "国金量化多因子股票A", Category: "equity",
				Value: 18815.86, CostAmount: 21000, WeightPct: 8.82, ProfitRate: -10.4},
		},
		Industries: []IndustryLine{{Industry: "通信设备", Pct: 7.78}},
	}
}

// 开启脱敏后，提示词里不该出现任何金额数字
func TestRedactStripsAllAmounts(t *testing.T) {
	b := NewBuilder(Options{Redact: true})
	out := BuildPortfolioReview(b, portfolioFixture())

	for _, forbidden := range []string{"¥", "213,261", "154,184", "150000", "21000"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("脱敏后不该出现 %q，实际输出:\n%s", forbidden, out)
		}
	}
	// 占比和收益率要保留 —— 脱敏不是把信息删光，是换一种表述
	if !strings.Contains(out, "72.3%") {
		t.Error("脱敏后仍应保留占比")
	}
	if !strings.Contains(out, "-10.4%") {
		t.Error("脱敏后仍应保留收益率")
	}
	if !strings.Contains(out, "不含任何持仓金额") {
		t.Error("脱敏时要在开头告诉 AI 当前口径，否则它会按金额来给建议")
	}
}

// 默认（Q7=B）要带真实金额，且带千分位
func TestDefaultKeepsAmounts(t *testing.T) {
	b := NewBuilder(Options{})
	out := BuildPortfolioReview(b, portfolioFixture())

	if !strings.Contains(out, "¥213,261.70") {
		t.Errorf("默认应带真实金额且有千分位，实际输出:\n%s", out)
	}
	if !strings.Contains(out, "含我的真实持仓金额") {
		t.Error("带金额时要如实告诉 AI，让它知道可以用金额表述建议")
	}
}

func TestThousands(t *testing.T) {
	cases := map[float64]string{
		213261.70: "213,261.70",
		150000:    "150,000",
		999:       "999",
		1000:      "1,000",
		-2184.14:  "-2,184.14",
		0:         "0",
	}
	for in, want := range cases {
		if got := thousands(in); got != want {
			t.Errorf("thousands(%v) = %q, want %q", in, got, want)
		}
	}
}

// Pct 自带 % 号，拼 "pp" 会得到 "+22.3%pp" 这种既不是百分比也不是百分点的东西
func TestPpHasNoPercentSign(t *testing.T) {
	if got := Pp(22.3); got != "+22.3pp" {
		t.Errorf("Pp(22.3) = %q, want %q", got, "+22.3pp")
	}
	if got := Pp(-16.18); got != "-16.18pp" {
		t.Errorf("Pp(-16.18) = %q, want %q", got, "-16.18pp")
	}
}

// 缺口要写进提示词本身：AI 得知道哪些空白是「工具查不到」而不是「用户没给」
func TestGapsSectionClassifies(t *testing.T) {
	b := NewBuilder(Options{})
	b.Gap(GapGreen, "3 只没有季报持仓", "加入关注列表，24 小时内抓取")
	b.Gap(GapRed, "子行业 PE 分位", "建议查 lixinger.com")

	out := b.GapsSection("数据缺口说明")
	if !strings.Contains(out, "🟢 我可以自己补") || !strings.Contains(out, "🔴 工具拿不到") {
		t.Errorf("缺口要按三色分类，实际输出:\n%s", out)
	}
	if !strings.Contains(out, "加入关注列表") {
		t.Error("🟢 类缺口必须带上怎么补")
	}
}

// 每份提示词都要挂 Footer，否则 AI 会用一堆行话回答、且不给工具内的操作路径
func TestAllScenesCarryFooter(t *testing.T) {
	b := NewBuilder(Options{})
	checks := map[string]string{
		"组合体检": BuildPortfolioReview(b, portfolioFixture()),
		"风险解读": BuildRiskReview(NewBuilder(Options{}), RiskInput{AsOf: "2026-07-30", RiskScore: 62}),
		"重叠穿透": BuildLookThroughReview(NewBuilder(Options{}), LookThroughInput{AsOf: "2026-07-30"}),
		"加仓判断": BuildAddPositionReview(NewBuilder(Options{}), FundInput{AsOf: "2026-07-30", Code: "006195", Name: "某基金"}),
		"赛道横评": BuildThemeReview(NewBuilder(Options{}), FundInput{AsOf: "2026-07-30", Code: "006195", Name: "某基金"}),
		"调仓审查": BuildRebalanceReview(NewBuilder(Options{}), RebalanceInput{AsOf: "2026-07-30"}),
		"选基缺口": BuildSelectorReview(NewBuilder(Options{}), SelectorInput{AsOf: "2026-07-30"}),
		"决策复盘": BuildDecisionReview(NewBuilder(Options{}), DecisionInput{AsOf: "2026-07-30", Code: "006195", Name: "某基金"}),
	}
	for name, out := range checks {
		if !strings.Contains(out, "工具内操作地图") {
			t.Errorf("%s 缺少 Footer 的操作地图", name)
		}
		if !strings.Contains(out, "## 请回答") {
			t.Errorf("%s 缺少提问块 —— 光给数据 AI 只会反问「你想让我分析什么」", name)
		}
	}
}

// 每个场景都要能被 SceneByID 查到，否则统一端点会 404
func TestScenesRegistryComplete(t *testing.T) {
	if len(Scenes) != 8 {
		t.Errorf("产品稿 Q1=B 定的是 8 个场景，现在有 %d 个", len(Scenes))
	}
	for _, s := range Scenes {
		got, found := SceneByID(s.ID)
		if !found || got.Title == "" {
			t.Errorf("场景 %q 查不到或没有标题", s.ID)
		}
	}
	if _, found := SceneByID("不存在的场景"); found {
		t.Error("未知场景不该被查到")
	}
}
