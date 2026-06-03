package fetcher

import (
	"testing"
)

var sampleHoldingsHTML = `
var apidata={ content:"
<div>截止至：<font color='red'>2024-09-30</font></div>
<table class='w782 comm tzxq'>
<thead><tr><th>序号</th><th>股票名称</th><th>股票代码</th><th>占净值比例</th></tr></thead>
<tbody>
<tr><td>1</td><td><a href='...'>贵州茅台<em>600519</em></a></td><td>600519</td><td>9.50%</td></tr>
<tr><td>2</td><td><a href='...'>宁德时代<em>300750</em></a></td><td>300750</td><td>7.20%</td></tr>
<tr><td>3</td><td><a href='...'>五粮液<em>000858</em></a></td><td>000858</td><td>5.80%</td></tr>
</tbody>
</table>
",arryear:["2024"],
`

func TestParseHoldingsHTML_ExtractsDate(t *testing.T) {
	date, stocks := parseHoldingsHTML(sampleHoldingsHTML)
	if date != "2024-09-30" {
		t.Errorf("expected date 2024-09-30, got %q", date)
	}
	if len(stocks) == 0 {
		t.Fatal("expected stocks to be parsed, got 0")
	}
}

func TestParseHoldingsHTML_ExtractsStocks(t *testing.T) {
	_, stocks := parseHoldingsHTML(sampleHoldingsHTML)

	codeSet := map[string]bool{}
	for _, s := range stocks {
		codeSet[s.stockCode] = true
	}

	for _, expected := range []string{"600519", "300750", "000858"} {
		if !codeSet[expected] {
			t.Errorf("expected stock code %s not found in result %v", expected, codeSet)
		}
	}
}

func TestParseHoldingsHTML_ParsesWeight(t *testing.T) {
	_, stocks := parseHoldingsHTML(sampleHoldingsHTML)
	if len(stocks) == 0 {
		t.Fatal("no stocks parsed")
	}
	// Find 贵州茅台 / 600519, verify weight
	for _, s := range stocks {
		if s.stockCode == "600519" {
			if s.weight != 9.50 {
				t.Errorf("expected weight 9.50 for 600519, got %f", s.weight)
			}
			return
		}
	}
	t.Error("600519 not found in stocks")
}

func TestParseHoldingsHTML_EmptyOnMissingDate(t *testing.T) {
	html := `<table><tr><td>1</td><td>贵州茅台<em>600519</em></td><td>9.50%</td></tr></table>`
	date, stocks := parseHoldingsHTML(html)
	if date != "" {
		t.Errorf("expected empty date, got %q", date)
	}
	if len(stocks) != 0 {
		t.Errorf("expected no stocks when date missing, got %d", len(stocks))
	}
}

func TestExtractCells(t *testing.T) {
	rowHTML := `<td>1</td><td>贵州茅台</td><td>600519</td><td>9.50%</td>`
	cells := extractCells(rowHTML)
	if len(cells) != 4 {
		t.Fatalf("expected 4 cells, got %d", len(cells))
	}
	if cells[0] != "1" {
		t.Errorf("expected cells[0]='1', got %q", cells[0])
	}
	if cells[2] != "600519" {
		t.Errorf("expected cells[2]='600519', got %q", cells[2])
	}
}

func TestParseHoldingsHTML_StockCodeRegex(t *testing.T) {
	// Valid codes: start with 0, 3, or 6, 6 digits total
	cases := []struct {
		html     string
		expected string
	}{
		{`<a>贵州茅台<em>600519</em></a>`, "600519"},
		{`<a>宁德时代<em>300750</em></a>`, "300750"},
		{`<a>五粮液<em>000858</em></a>`, "000858"},
		{`<a>中国平安<em>601318</em></a>`, "601318"}, // 6开头是沪市主板，合法代码
		{`<a>只有名字没代码</a>`, ""},
	}
	for _, c := range cases {
		got := reStockCode.FindString(c.html)
		if got != c.expected {
			t.Errorf("reStockCode.FindString(%q) = %q, want %q", c.html, got, c.expected)
		}
	}
}
