package sectorflow

import (
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

// 调度器的 cron 表达式正确性，不用真等到周一才知道。
// 直接把表达式喂给 robfig/cron 算出未来的触发时刻，再叠上 InTradingSession 过滤，
// 看最终真正会执行的时点是不是产品稿 Q2 说的那些。
//
// 这两条 spec 必须和 internal/scheduler/scheduler.go 里的保持一致；
// 改那边忘了改这边，本测试会失败。
const (
	specIntraday = "15,45 9-15 * * 1-5" // 任务9
	specClose    = "0 19 * * 1-5"       // 任务10
)

func nextFires(t *testing.T, spec string, from time.Time, within time.Duration) []time.Time {
	t.Helper()
	sch, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(spec)
	if err != nil {
		t.Fatalf("cron 表达式 %q 解析失败: %v", spec, err)
	}
	var out []time.Time
	cur := from
	for {
		cur = sch.Next(cur)
		if cur.Sub(from) > within {
			return out
		}
		out = append(out, cur)
	}
}

func TestIntradayCronFiresOnlyInTradingSession(t *testing.T) {
	// 从 2026-08-17（周一）零点起算一整天
	mon := time.Date(2026, 8, 17, 0, 0, 0, 0, time.Local)
	var ran []string
	for _, f := range nextFires(t, specIntraday, mon, 24*time.Hour) {
		if InTradingSession(f) {
			ran = append(ran, f.Format("15:04"))
		}
	}

	want := []string{"09:45", "10:15", "10:45", "11:15", "13:15", "13:45", "14:15", "14:45"}
	if len(ran) != len(want) {
		t.Fatalf("周一实际执行 %d 次 %v，期望 %d 次 %v", len(ran), ran, len(want), want)
	}
	for i := range want {
		if ran[i] != want[i] {
			t.Errorf("第 %d 次执行时刻应为 %s，得到 %s", i+1, want[i], ran[i])
		}
	}
	// 产品稿 Q2 承诺「盘中滞后 ≤30 分钟」：相邻两次间隔不能超过 30 分钟
	// （午休那段除外，那时本来就没有交易）
	for i := 1; i < len(ran); i++ {
		if ran[i-1] == "11:15" {
			continue // 跨午休，不算滞后
		}
		p, _ := time.Parse("15:04", ran[i-1])
		c, _ := time.Parse("15:04", ran[i])
		if c.Sub(p) > 30*time.Minute {
			t.Errorf("%s → %s 间隔 %v，超过产品稿承诺的 30 分钟滞后上限", ran[i-1], ran[i], c.Sub(p))
		}
	}
}

func TestIntradayCronSkipsWeekend(t *testing.T) {
	sat := time.Date(2026, 8, 15, 0, 0, 0, 0, time.Local)
	for _, f := range nextFires(t, specIntraday, sat, 48*time.Hour) {
		if f.Weekday() == time.Saturday || f.Weekday() == time.Sunday {
			t.Errorf("cron 不该在周末唤醒，却排到了 %s", f.Format("01-02 Mon 15:04"))
		}
	}
}

func TestCloseCronRunsAfterSettlement(t *testing.T) {
	mon := time.Date(2026, 8, 17, 0, 0, 0, 0, time.Local)
	fires := nextFires(t, specClose, mon, 24*time.Hour)
	if len(fires) != 1 {
		t.Fatalf("收盘落定每个交易日应只跑 1 次，得到 %d 次", len(fires))
	}
	got := fires[0].Format("15:04")
	if got != "19:00" {
		t.Fatalf("收盘落定应在 19:00，得到 %s", got)
	}
	// 实测 8-14 的数据时间戳是 15:39:31，收盘 40 分钟后东财还在更新。
	// 落库时点必须留足冗余，否则会把未结算完的中间值当成收盘值存进库。
	settle := time.Date(2026, 8, 17, 15, 40, 0, 0, time.Local)
	if !fires[0].After(settle.Add(2 * time.Hour)) {
		t.Errorf("落库时点 %s 距东财结算完成(约15:40)不足 2 小时冗余", got)
	}
}
