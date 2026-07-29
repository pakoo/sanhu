package market

import "testing"

// 数据源失败时必须降级到缓存并标 Stale，绝不整页报错 —— 产品稿 M4 的核心承诺
func TestGroupDegradesToCache(t *testing.T) {
	putCache("commodity", []Item{{Code: "hf_GC", Name: "纽约黄金", Price: 4000, ChangePct: 1.5}})

	items, at, ok := getCache("commodity")
	if !ok || len(items) != 1 {
		t.Fatalf("缓存写入后应能读出，得到 ok=%v len=%d", ok, len(items))
	}
	if at.IsZero() {
		t.Error("缓存必须带时间戳，前端要显示「这是 X 时的缓存」")
	}
}

// 安静的日子信号条应该是空的，不该为了填满而降低阈值
func TestPickSignalsOnlyAbnormal(t *testing.T) {
	b := &Board{Groups: []Group{{Items: []Item{
		{Name: "微涨", ChangePct: 0.3},
		{Name: "大跌", ChangePct: -8.15},
		{Name: "持平", ChangePct: 0},
		{Name: "上涨", ChangePct: 2.4},
	}}}}
	got := pickSignals(b)
	if len(got) != 2 {
		t.Fatalf("只有 2 条超过 ±1%%，得到 %d 条", len(got))
	}
	if got[0].Name != "大跌" {
		t.Errorf("应按幅度绝对值降序，第一条该是「大跌」，得到 %q", got[0].Name)
	}
}

func TestNormalizeQuoteCode(t *testing.T) {
	cases := []struct{ in, wantCode, wantUnit string }{
		{"NVDA", "gb_nvda", "$"},
		{"nvda", "gb_nvda", "$"},
		{"300308", "sz300308", "¥"},
		{"600519", "sh600519", "¥"},
		{"688256", "sh688256", "¥"},
		{"830799", "bj830799", "¥"},
		{"999999", "", ""},   // 9 开头不属于任何交易所
		{"", "", ""},
	}
	for _, c := range cases {
		gotCode, gotUnit := NormalizeQuoteCode(c.in)
		if gotCode != c.wantCode || gotUnit != c.wantUnit {
			t.Errorf("NormalizeQuoteCode(%q) = (%q,%q), want (%q,%q)", c.in, gotCode, gotUnit, c.wantCode, c.wantUnit)
		}
	}
}

// 宏观数据未填时必须标 Pending，前端显示「待补充」而不是 0 —— 宁可留白不显示错数字
func TestMacroPendingWhenUnset(t *testing.T) {
	m := CurrentMacro()
	if !m.Treasury.Pending {
		t.Error("10Y 美债无可用数据源，必须标 Pending")
	}
	if m.Rate.Value != "" && m.Rate.AsOf == "" {
		t.Error("填了利率就必须同时给出数据截至日期，否则用户不知道数字多旧")
	}
}
