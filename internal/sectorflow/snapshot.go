package sectorflow

import (
	"fmt"
	"log"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/fetcher"
)

// 定时快照。被 internal/scheduler 调用，也可以手动触发。

// Snapshot 抓一轮全部板块并落库，返回写入的行数。
//
// ── 为什么盘中每次刷新都写库，而不是只在 19:00 写一次 ──────────────────────
// 产品稿 Q2 定的是「盘中每 30 分钟刷新 + 19:00 落库」。但 sanhu 是**桌面程序**，
// 用户想开才开 —— 如果只在 19:00 写，那么「今天用户 14:00 开过一次、19:00 没开」
// 这一天就永久没有数据，而东财的历史只有 120 天、补不回来。
// 所以改成每次刷新都 upsert 同一天那行：程序只要在当天开过一次，这天就不会丢。
// 主键是 (date, bk_code)，重复写是覆盖不是累积，越晚的数据越接近收盘真值。
//
// ── 为什么用 DataDate 而不是 time.Now() ────────────────────────────────────
// 周末和节假日请求，东财返回的是**上一个交易日**的数据。用「今天」当日期落库，
// 会把周五的数字记到周六名下，历史图表整体错位。所以日期一律取数据自己的
// 时间戳（f124），拿不到时间戳就跳过写入 —— 宁可少一天，不可错一天。
func Snapshot(db *sqlx.DB, kind fetcher.SectorKind) (int, error) {
	rows, err := fetchRank(kind)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, fmt.Errorf("%s 板块返回空", kind)
	}

	date := rows[0].DataDate
	if date == "" {
		return 0, fmt.Errorf("%s 板块数据没有时间戳，无法判定交易日，跳过落库", kind)
	}
	if err := SaveDaily(db, date, rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// SnapshotMinuteTop 存当日榜单前 MinuteKeepTop 个板块的分时曲线。
//
// 为什么只存 Top20：全量 1000 个板块 × 240 点 × 250 交易日 ≈ 6000 万行/年，
// 存不起也没人看。热门板块才是用户会点开的（产品稿 Q4）。
//
// 为什么必须存：东财的 klt=1 只返回**当日**分时，收盘后就查不到昨天的了。
// 今天没存，这一天的分钟曲线就永久丢失，没有任何补救办法。
//
// 每个板块一个请求，所以这里**串行 + 间隔**，绝不并发 —— 20 个并发请求正是
// 会触发限流的模式（见 fetcher/fundflow.go 文件头，以及 holdings_fetcher.go:109
// 里「200 个请求把自己打限流」的既有记录）。
func SnapshotMinuteTop(db *sqlx.DB, kind fetcher.SectorKind) (int, error) {
	rows, err := fetchRank(kind)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, fmt.Errorf("%s 板块返回空", kind)
	}
	date := rows[0].DataDate
	if date == "" {
		return 0, fmt.Errorf("%s 板块数据没有时间戳，跳过分时落库", kind)
	}

	// 按 |主力净额| 取最热的 —— 流出最猛的同样值得留档
	groups := Dedup(rows)
	picked := make([]string, 0, MinuteKeepTop)
	for _, g := range groups {
		if len(picked) >= MinuteKeepTop {
			break
		}
		picked = append(picked, g.Code)
	}

	saved := 0
	for i, code := range picked {
		if i > 0 {
			// 请求之间留白。宁可这轮慢一点，也不要把整个行情接口打到限流 ——
			// 一旦触发，「市场」页的 A 股行情会跟着一起哑掉（同走 push2）。
			time.Sleep(1500 * time.Millisecond)
		}
		pts, err := fetcher.FetchSectorMinute(code)
		if err != nil {
			// 单个板块失败不影响其它：分时是锦上添花，不该因为一个失败就整轮回滚
			log.Printf("[sectorflow] 抓分时 %s 失败，跳过: %v", code, err)
			continue
		}
		if err := SaveMinute(db, date, code, pts); err != nil {
			log.Printf("[sectorflow] 存分时 %s 失败，跳过: %v", code, err)
			continue
		}
		saved++
	}
	if saved == 0 {
		return 0, fmt.Errorf("%d 个板块的分时全部抓取失败（多半是限流）", len(picked))
	}
	return saved, nil
}

// InTradingSession 判断此刻是否在 A 股交易时段内（含尾部宽限）。
//
// 只按星期和时间判断，**不含节假日日历** —— 项目里没有交易日历，
// 硬编码一份要每年维护、且一定会过期。节假日误跑的代价很小（多一次请求，
// 拿回上一交易日的数据，而 Snapshot 用 DataDate 落库不会写错日期）；
// 漏跑的代价才大，所以这里选择宁可多跑。
func InTradingSession(t time.Time) bool {
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	const (
		amOpen  = 9*60 + 45 // 09:45，开盘前 15 分钟噪音大且集合竞价未计入，故意错开
		amClose = 11*60 + 35
		pmOpen  = 13 * 60
		pmClose = 15*60 + 5 // 收盘后多留 5 分钟，接住 15:00 那一轮
	)
	return (m >= amOpen && m <= amClose) || (m >= pmOpen && m <= pmClose)
}
