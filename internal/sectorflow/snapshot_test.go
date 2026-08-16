package sectorflow

import (
	"errors"
	"testing"
	"time"

	"github.com/pakoo/sanhu/internal/fetcher"
)

func TestInTradingSession(t *testing.T) {
	// 2026-08-17 是周一，2026-08-15 是周六
	mon := func(hh, mm int) time.Time { return time.Date(2026, 8, 17, hh, mm, 0, 0, time.Local) }
	sat := func(hh, mm int) time.Time { return time.Date(2026, 8, 15, hh, mm, 0, 0, time.Local) }

	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"周六不跑", sat(10, 30), false},
		{"开盘前", mon(9, 20), false},
		{"09:45 起跑（前 15 分钟故意错开）", mon(9, 45), true},
		{"09:44 还不跑", mon(9, 44), false},
		{"上午盘中", mon(10, 45), true},
		{"午休不跑", mon(12, 0), false},
		{"下午开盘", mon(13, 15), true},
		{"尾盘", mon(14, 45), true},
		{"15:05 宽限内（接住 15:00 那轮）", mon(15, 5), true},
		{"收盘后不跑", mon(15, 30), false},
		{"深夜不跑", mon(23, 0), false},
	}
	for _, c := range cases {
		if got := InTradingSession(c.at); got != c.want {
			t.Errorf("%s：InTradingSession(%s) = %v，期望 %v",
				c.name, c.at.Format("Mon 15:04"), got, c.want)
		}
	}
}

func TestSnapshotUsesDataDateNotToday(t *testing.T) {
	// 核心保护：周末抓到的是上一交易日的数据，必须按数据自己的日期落库。
	// 若用 time.Now()，周六抓到的周五数据会被记成周六，历史图表整体错位。
	db := testDB(t)
	rows := sample()
	for i := range rows {
		rows[i].DataDate = "2026-08-14" // 数据属于周五
	}
	stubFetch(t, rows, nil)

	n, err := Snapshot(db, fetcher.KindIndustry)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if n != len(rows) {
		t.Errorf("应写入 %d 行，得到 %d", len(rows), n)
	}

	_, date, err := LoadRank(db, fetcher.KindIndustry, "")
	if err != nil {
		t.Fatalf("LoadRank: %v", err)
	}
	if date != "2026-08-14" {
		t.Errorf("应按数据自身日期 2026-08-14 落库，得到 %q —— 用 time.Now() 会把周五记成周六", date)
	}
}

func TestSnapshotRefusesWhenNoTimestamp(t *testing.T) {
	// 拿不到时间戳就宁可不写：少一天可以后补（东财还有 120 天），
	// 写错一天则会污染历史且难以察觉。
	db := testDB(t)
	rows := sample() // DataDate 全为空
	stubFetch(t, rows, nil)

	if _, err := Snapshot(db, fetcher.KindIndustry); err == nil {
		t.Fatal("没有时间戳时应拒绝落库并报错")
	}
	if got, _, _ := LoadRank(db, fetcher.KindIndustry, ""); len(got) != 0 {
		t.Errorf("拒绝落库后库里不该有数据，得到 %d 行", len(got))
	}
}

func TestSnapshotIsIdempotentAcrossIntradayRuns(t *testing.T) {
	// 盘中每 30 分钟会跑一次同一天，必须覆盖而不是累积。
	db := testDB(t)
	rows := sample()
	for i := range rows {
		rows[i].DataDate = "2026-08-14"
	}
	stubFetch(t, rows, nil)
	if _, err := Snapshot(db, fetcher.KindIndustry); err != nil {
		t.Fatalf("首轮: %v", err)
	}

	rows[0].MainNet = 42
	stubFetch(t, rows, nil)
	if _, err := Snapshot(db, fetcher.KindIndustry); err != nil {
		t.Fatalf("次轮: %v", err)
	}

	got, _, _ := LoadRank(db, fetcher.KindIndustry, "2026-08-14")
	if len(got) != len(rows) {
		t.Fatalf("多轮快照不该累积行数，应为 %d，得到 %d", len(rows), len(got))
	}
	for _, r := range got {
		if r.Code == rows[0].Code && r.MainNet != 42 {
			t.Errorf("后一轮应覆盖前一轮，期望 42，得到 %v", r.MainNet)
		}
	}
}

func TestSnapshotPropagatesThrottleError(t *testing.T) {
	db := testDB(t)
	stubFetch(t, nil, errors.New("被限流"))
	if _, err := Snapshot(db, fetcher.KindIndustry); err == nil {
		t.Error("抓取失败应把错误抛给调度器，由它记日志并等下个周期")
	}
}
