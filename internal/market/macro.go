package market

import (
	"fmt"
	"sort"
	"time"
)

// 宏观「关键少数」（产品稿 Q5 = 甲：内置数据随版本更新，卡上标注数据日期）。
//
// 为什么内置而不是抓：利率/CPI/非农都是月频或更低频（议息一年 8 次），
// 且找不到可靠的免费 API —— 实测东财 push2 的 US10Y 系列 secid 全部返回 null，
// 新浪 globalbd_* / bond_* 频道返回空串。对低频数据，「随版本更新 + 明确标注截至日期」
// 比接一个随时会挂的第三方源更稳。
//
// ⚠️ 维护约定（已写进 docs/release.md 的发版 checklist）：
// 每次发版前核对下面的数字，改完把对应的 AsOf 一起改掉。
// 值为空（Pending）时前端显示「待补充」而不是 0 —— 宁可留白，绝不显示错的金融数据。

// MacroField 一个宏观指标。Pending=true 表示尚未填入真实数据。
type MacroField struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	AsOf    string `json:"as_of"`   // 数据截至日期，直接显示给用户
	Pending bool   `json:"pending"` // true → 前端显示「待补充」
}

// Macro 宏观卡的完整内容
type Macro struct {
	Rate      MacroField `json:"rate"`       // 联邦基金利率区间
	NextFOMC  MacroField `json:"next_fomc"`  // 下次议息 + 倒计时
	CPI       MacroField `json:"cpi"`        // 最新 CPI 同比
	NFP       MacroField `json:"nfp"`        // 最新非农新增
	Treasury  MacroField `json:"treasury"`   // 10Y 美债收益率
	AllPending bool      `json:"all_pending"`
}

// fomcDates 全年议息日期（美联储官网年初公布，格式 2006-01-02）。
// 填入后 NextFOMC 会自动算倒计时，无需每次改。
//
// 暂为空：2026 年的 FOMC 日历需要你从 federalreserve.gov 核对后填入，
// 我不会凭印象写日期 —— 写错会让「还有 N 天」这个数字变成误导。
var fomcDates = []string{}

// macroSnapshot 手工维护的宏观数字。
// 留空表示待补充；填的时候把 AsOf 一并写上（如 "2026-06"）。
var macroSnapshot = struct {
	RateRange, RateAsOf string
	CPI, CPIAsOf        string
	NFP, NFPAsOf        string
}{
	RateRange: "", RateAsOf: "",
	CPI: "", CPIAsOf: "",
	NFP: "", NFPAsOf: "",
}

// CurrentMacro 组装宏观卡。任何一项没填就标 Pending，前端显示「待补充」。
func CurrentMacro() Macro {
	m := Macro{
		Rate:     field("联邦基金利率", macroSnapshot.RateRange, macroSnapshot.RateAsOf),
		CPI:      field("CPI 同比", macroSnapshot.CPI, macroSnapshot.CPIAsOf),
		NFP:      field("非农新增", macroSnapshot.NFP, macroSnapshot.NFPAsOf),
		NextFOMC: nextFOMC(),
		// 10Y 美债：没有可用免费源（多个候选实测均返回空），
		// 不硬编码一个每天都在变的数字，直接如实标注
		Treasury: MacroField{Label: "10年期美债收益率", Pending: true, AsOf: "暂无可用数据源"},
	}
	m.AllPending = m.Rate.Pending && m.CPI.Pending && m.NFP.Pending && m.NextFOMC.Pending
	return m
}

func field(label, value, asOf string) MacroField {
	if value == "" {
		return MacroField{Label: label, Pending: true}
	}
	return MacroField{Label: label, Value: value, AsOf: asOf}
}

// nextFOMC 从 fomcDates 里找出下一个未过的日期并算倒计时
func nextFOMC() MacroField {
	f := MacroField{Label: "下次议息"}
	if len(fomcDates) == 0 {
		f.Pending = true
		return f
	}

	dates := append([]string(nil), fomcDates...)
	sort.Strings(dates)
	today := time.Now().Truncate(24 * time.Hour)

	for _, d := range dates {
		t, err := time.ParseInLocation("2006-01-02", d, time.Local)
		if err != nil {
			continue
		}
		if t.Before(today) {
			continue
		}
		days := int(t.Sub(today).Hours() / 24)
		switch {
		case days == 0:
			f.Value = fmt.Sprintf("%s · 就是今天", t.Format("1月2日"))
		default:
			f.Value = fmt.Sprintf("%s · 还有 %d 天", t.Format("1月2日"), days)
		}
		f.AsOf = t.Format("2006-01-02")
		return f
	}

	// 日历用完了（到了年底还没更新），如实说，别显示一个过期日期
	f.Pending = true
	f.AsOf = "日历需更新"
	return f
}

// ── AI 事件日历 ──────────────────────────────────────────────────────────────

// Event AI 相关的时间点（财报、发布会、议息）
type Event struct {
	Date  string `json:"date"`  // 2006-01-02
	Title string `json:"title"`
	Days  int    `json:"days"`  // 距今天数，负数表示已过
	Soon  bool   `json:"soon"`  // ≤3 天，会被带进「今日信号」
}

// aiEvents 手工维护的事件日历（产品稿已说明：无稳定免费源，随版本更新）。
//
// 同样留空：财报日期需要从公司 IR 页面核对，我不凭印象写。
// 填的格式：{"2026-08-27", "英伟达 FY27Q2 财报（美股盘后）"}
var aiEvents = []struct{ date, title string }{}

// UpcomingEvents 返回最近的 n 条未过期事件
func UpcomingEvents(n int) []Event {
	today := time.Now().Truncate(24 * time.Hour)
	out := make([]Event, 0, n)

	for _, e := range aiEvents {
		t, err := time.ParseInLocation("2006-01-02", e.date, time.Local)
		if err != nil {
			continue
		}
		days := int(t.Sub(today).Hours() / 24)
		if days < 0 {
			continue
		}
		out = append(out, Event{Date: e.date, Title: e.title, Days: days, Soon: days <= 3})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	if len(out) > n {
		out = out[:n]
	}
	return out
}
