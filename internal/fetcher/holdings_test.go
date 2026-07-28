package fetcher

import (
	"os"
	"path/filepath"
	"testing"
)

// 用东财的真实响应做 fixture（testdata/jjcc_006195.txt，2026-07-28 抓取）。
//
// 这里之前是一段手写的假 HTML，表头写成「序号|股票名称|股票代码|占净值比例」，
// 而真实响应是「序号|股票代码|股票名称|最新价|涨跌幅|相关资讯|占净值比例|持股数|持仓市值」——
// 名称与代码的列序是反的，权重列位置也不同。解析器对着假 HTML 一直是"对"的，
// 对着真实数据每一行都错，测试却始终绿着。
// 结论：抓取类的 fixture 必须来自真实响应，手写的等于给自己发假证。
func realHoldingsHTML(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "jjcc_006195.txt"))
	if err != nil {
		t.Fatalf("读取 fixture 失败: %v", err)
	}
	return string(b)
}

func TestParseHoldingsHTML_ExtractsDate(t *testing.T) {
	date, stocks := parseHoldingsHTML(realHoldingsHTML(t))
	if date == "" {
		t.Fatal("没有解析出报告期")
	}
	if len(stocks) == 0 {
		t.Fatal("没有解析出任何持仓股")
	}
}

// 名称必须是名称，不能是代码——这正是线上那批坏数据的样子
func TestParseHoldingsHTML_NameIsNotCode(t *testing.T) {
	_, stocks := parseHoldingsHTML(realHoldingsHTML(t))
	for _, s := range stocks {
		if s.stockName == "" {
			t.Errorf("%s 没有解析出名称", s.stockCode)
		}
		if s.stockName == s.stockCode {
			t.Errorf("%s 的名称落成了代码——列取错了", s.stockCode)
		}
	}
}

// 权重必须是「占净值比例」，不能是「持仓市值」
func TestParseHoldingsHTML_WeightIsPercent(t *testing.T) {
	_, stocks := parseHoldingsHTML(realHoldingsHTML(t))

	nonZero := 0
	for _, s := range stocks {
		if s.weight > 0 {
			nonZero++
		}
		// 单只股票占基金净值不可能超过 100%（更不可能是 876% 那种市值数）
		if s.weight > 100 {
			t.Errorf("%s(%s) 权重 %.2f 超过 100%%——取到的多半是持仓市值列",
				s.stockName, s.stockCode, s.weight)
		}
		if s.weight < 0 {
			t.Errorf("%s 权重为负: %.2f", s.stockCode, s.weight)
		}
	}
	if nonZero == 0 {
		t.Fatal("所有权重都是 0——权重列没取到")
	}

	// 前十大重仓合计应当是个合理的仓位区间，不该接近 0 也不该爆表
	var sum float64
	for _, s := range stocks {
		sum += s.weight
	}
	if sum <= 1 || sum > 100 {
		t.Errorf("前十大持仓合计 %.2f%%，不是合理区间", sum)
	}
}

func TestParseHoldingsHTML_CodeFormat(t *testing.T) {
	_, stocks := parseHoldingsHTML(realHoldingsHTML(t))
	for _, s := range stocks {
		if len(s.stockCode) < 5 || len(s.stockCode) > 6 {
			t.Errorf("股票代码格式可疑: %q", s.stockCode)
		}
	}
}

// 东财若调整列顺序，认表头的解析器应当照样取对
func TestParseHoldingsHTML_SurvivesColumnReorder(t *testing.T) {
	reordered := `var apidata={ content:"
<div>截止至：<font class='px12'>2026-06-30</font></div>
<table class='w782 comm tzxq'>
<thead><tr><th>序号</th><th>占净值<br />比例</th><th>股票名称</th><th>持仓市值（万元）</th><th>股票代码</th></tr></thead>
<tbody>
<tr><td>1</td><td class='tor'>1.78%</td><td class='tol'><a href='#'>京东方A</a></td><td class='tor'>14,871.15</td><td><a href='#'>000725</a></td></tr>
</tbody>
</table>
",arryear:["2026"],`

	_, stocks := parseHoldingsHTML(reordered)
	if len(stocks) != 1 {
		t.Fatalf("期望 1 条，得到 %d 条", len(stocks))
	}
	s := stocks[0]
	if s.stockCode != "000725" {
		t.Errorf("代码 = %q，期望 000725", s.stockCode)
	}
	if s.stockName != "京东方A" {
		t.Errorf("名称 = %q，期望 京东方A", s.stockName)
	}
	if s.weight != 1.78 {
		t.Errorf("权重 = %v，期望 1.78", s.weight)
	}
}

// 千分位逗号不能让权重解析失败落 0
func TestParseWeight(t *testing.T) {
	cases := map[string]float64{
		"1.78%":     1.78,
		" 9.50% ":   9.50,
		"14,871.15": 14871.15,
		"":          0,
		"--":        0,
	}
	for in, want := range cases {
		if got := parseWeight(in); got != want {
			t.Errorf("parseWeight(%q) = %v, want %v", in, got, want)
		}
	}
}
