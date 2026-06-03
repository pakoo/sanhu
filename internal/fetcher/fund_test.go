package fetcher

import (
	"regexp"
	"testing"
)

func TestInferCategory(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"股票型", "equity"},
		{"混合型", "mixed"},
		{"债券型", "bond"},
		{"货币市场型", "money"},
		{"QDII-股票", "qdii"},
		{"指数型", "equity"},
		{"未知类型", "mixed"}, // fallback
		{"", "mixed"},       // empty fallback
	}

	for _, c := range cases {
		got := inferCategory(c.input)
		if got != c.expected {
			t.Errorf("inferCategory(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestExtractRegex(t *testing.T) {
	cases := []struct {
		pattern  string
		input    string
		expected string
	}{
		{`var\s+fS_name\s*=\s*"([^"]*)"`, `var fS_name = "华泰柏瑞沪深300ETF"`, "华泰柏瑞沪深300ETF"},
		{`var\s+fS_type\s*=\s*"([^"]*)"`, `var fS_type = "股票指数型"`, "股票指数型"},
		{`([0-9.]+)亿元`, `基金规模：123.45亿元`, "123.45"},
		{`([0-9.]+)亿元`, `无规模信息`, ""},
	}

	for _, c := range cases {
		re := regexp.MustCompile(c.pattern)
		got := extractRegex(re, c.input)
		if got != c.expected {
			t.Errorf("extractRegex(%q, %q) = %q, want %q", c.pattern, c.input, got, c.expected)
		}
	}
}
