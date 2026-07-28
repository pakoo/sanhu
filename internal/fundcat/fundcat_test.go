package fundcat

import "testing"

// 这些 case 就是 Go 重写踩过的坑：子串规则里「指数」排在前面，
// 会把债基和 QDII 一起判成 equity。锁死与 Python 原版一致的口径。
func TestInfer(t *testing.T) {
	cases := []struct {
		fundType string
		want     string
	}{
		{"指数型-固收", "bond"},    // 曾被误判为 equity
		{"指数型-海外股票", "qdii"},  // 曾被误判为 equity
		{"指数型-股票", "equity"},
		{"债券型-混合一级", "bond"},
		{"纯债型", "bond"},
		{"股票型", "equity"},
		{"混合型-偏股", "mixed"},
		{"QDII", "qdii"},

		// exactMap 未覆盖的类型走关键字兜底
		{"QDII-股票", "qdii"},
		{"债券型-可转债", "bond"},
		{"货币型", "money"},
		{"", "mixed"},
		{"没见过的类型", "mixed"},
	}

	for _, c := range cases {
		if got := Infer(c.fundType); got != c.want {
			t.Errorf("Infer(%q) = %q, want %q", c.fundType, got, c.want)
		}
	}
}
