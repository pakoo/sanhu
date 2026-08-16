package sectorflow

import (
	"errors"
	"testing"
	"time"

	"github.com/pakoo/sanhu/internal/fetcher"
)

// 降级链条是限流时才跑到的代码，靠手工验证等于要先真把自己封掉。
// 这里用桩把「东财挂了」造出来，逐级验证。
func stubFetch(t *testing.T, rows []fetcher.SectorFlow, err error) {
	t.Helper()
	orig := fetchRank
	fetchRank = func(fetcher.SectorKind) ([]fetcher.SectorFlow, error) { return rows, err }
	t.Cleanup(func() { fetchRank = orig })
}

var throttled = errors.New("板块榜单返回空 —— 多半是被限流了")

func TestBuildBoardLiveSuccess(t *testing.T) {
	ResetCache()
	stubFetch(t, sample(), nil)

	b := BuildBoard(nil, fetcher.KindIndustry, true)
	if b.Stale || b.Source != "live" {
		t.Errorf("抓取成功时不该标 stale，得到 stale=%v source=%q", b.Stale, b.Source)
	}
	if len(b.Inflow) == 0 {
		t.Error("应有流入榜")
	}
	if len(b.Outflow) != 1 || b.Outflow[0].Code != "BK9999" {
		t.Errorf("流出榜应含 BK9999，得到 %+v", b.Outflow)
	}
	if b.Notice != "" {
		t.Errorf("正常情况不该有提示语，得到 %q", b.Notice)
	}
}

func TestBuildBoardFallsBackToMemoryCache(t *testing.T) {
	ResetCache()
	stubFetch(t, sample(), nil)
	_ = BuildBoard(nil, fetcher.KindIndustry, true) // 先填缓存
	expireCache()                                   // 让 TTL 过期，才走得到降级分支

	stubFetch(t, nil, throttled) // 之后东财开始限流
	b := BuildBoard(nil, fetcher.KindIndustry, true)

	if !b.Stale || b.Source != "cache" {
		t.Fatalf("应降级到内存缓存，得到 stale=%v source=%q", b.Stale, b.Source)
	}
	if len(b.Inflow) == 0 {
		t.Error("降级后仍应有数据可看，这正是降级的意义")
	}
	if b.Notice == "" {
		t.Error("降级必须给用户一句说明，否则他会以为看的是实时数据")
	}
}

func TestBuildBoardFallsBackToDBWhenCacheEmpty(t *testing.T) {
	// 场景：程序刚重启（内存缓存空），同时东财正在限流。
	ResetCache()
	db := testDB(t)
	if err := SaveDaily(db, "2026-08-14", sample()); err != nil {
		t.Fatalf("预置库数据: %v", err)
	}
	stubFetch(t, nil, throttled)

	b := BuildBoard(db, fetcher.KindIndustry, true)
	if !b.Stale || b.Source != "db" {
		t.Fatalf("应降级到数据库，得到 stale=%v source=%q", b.Stale, b.Source)
	}
	if b.DataDate != "2026-08-14" {
		t.Errorf("应标出数据对应的交易日，得到 %q", b.DataDate)
	}
	if len(b.Inflow) == 0 {
		t.Error("库里有数据就该渲染出来")
	}
}

func TestBuildBoardEmptyEverythingStillRenders(t *testing.T) {
	// 最坏情况：首次启动 + 正好被限流。必须给出可渲染的空结果而不是崩掉，
	// 否则整个「市场」页会跟着一起白屏。
	ResetCache()
	db := testDB(t)
	stubFetch(t, nil, throttled)

	b := BuildBoard(db, fetcher.KindIndustry, true)
	if b == nil {
		t.Fatal("任何情况下都不该返回 nil")
	}
	if b.Source != "none" || !b.Stale {
		t.Errorf("应标记为无数据，得到 source=%q stale=%v", b.Source, b.Stale)
	}
	if b.Notice == "" {
		t.Error("空结果更需要解释原因，否则用户只看到一片空白")
	}
	if len(b.Inflow) != 0 || len(b.Outflow) != 0 {
		t.Error("没有数据时两侧都应为空")
	}
}

func TestBuildBoardCacheFallbackDoesNotCorruptCache(t *testing.T) {
	// 降级时给缓存对象打 stale 标记，如果改的是缓存里那份对象本身，
	// 等东财恢复后缓存会一直带着 stale=true。
	ResetCache()
	stubFetch(t, sample(), nil)
	_ = BuildBoard(nil, fetcher.KindIndustry, true)
	expireCache()

	stubFetch(t, nil, throttled)
	_ = BuildBoard(nil, fetcher.KindIndustry, true) // 走降级，标 stale

	c, ok := getCache(cacheKey(fetcher.KindIndustry, true))
	if !ok || c.board == nil {
		t.Fatal("缓存不该被降级流程清掉")
	}
	if c.board.Stale {
		t.Error("降级只应改副本，缓存里那份必须保持 stale=false")
	}
}

func TestBuildBoardWithoutDedup(t *testing.T) {
	ResetCache()
	rows := realRows(t) // 8 条，其中 6 条是通信系
	stubFetch(t, rows, nil)

	on := BuildBoard(nil, fetcher.KindIndustry, true)
	ResetCache()
	stubFetch(t, rows, nil)
	off := BuildBoard(nil, fetcher.KindIndustry, false)

	if len(off.All) != 8 {
		t.Errorf("关掉去重应保留全部 8 条，得到 %d", len(off.All))
	}
	if len(on.All) >= len(off.All) {
		t.Errorf("开去重应当更少：去重后 %d，未去重 %d", len(on.All), len(off.All))
	}
}

func TestBuildBoardReportsDataOwnDate(t *testing.T) {
	// 周末/节假日打接口，东财返回上一个交易日的数据。
	// 界面上标成「今天」就是在骗用户 —— 他没法从任何地方看出这是三天前的行情。
	ResetCache()
	rows := sample()
	for i := range rows {
		rows[i].DataDate = "2026-08-14" // 数据属于周五
	}
	stubFetch(t, rows, nil)

	b := BuildBoard(nil, fetcher.KindIndustry, true)
	if b.DataDate != "2026-08-14" {
		t.Errorf("应显示数据自身的交易日 2026-08-14，得到 %q —— 用「今天」会误导用户", b.DataDate)
	}
	if b.Source != "live" {
		t.Errorf("这仍是实时抓取成功的路径，source 应为 live，得到 %q", b.Source)
	}
}

func TestBuildBoardFallsBackToTodayWhenNoTimestamp(t *testing.T) {
	ResetCache()
	stubFetch(t, sample(), nil) // DataDate 全为空
	b := BuildBoard(nil, fetcher.KindIndustry, true)
	if b.DataDate == "" {
		t.Error("拿不到数据时间戳时也该给个日期（退回今天），不能留空")
	}
}

// ── TTL 缓存 ────────────────────────────────────────────────────────────────
// 这是自动刷新能成立的前提：前端 60 秒轮询一次，绝大多数请求必须落在缓存上，
// 不然就是每小时 60 个东财请求（限流量级）。所以「到底回没回源」要能被验证。

// countingFetch 统计回源次数
func countingFetch(t *testing.T, rows []fetcher.SectorFlow) *int {
	t.Helper()
	n := 0
	orig := fetchRank
	fetchRank = func(fetcher.SectorKind) ([]fetcher.SectorFlow, error) { n++; return rows, nil }
	t.Cleanup(func() { fetchRank = orig })
	return &n
}

func TestBuildBoardServesFromCacheWithinTTL(t *testing.T) {
	ResetCache()
	n := countingFetch(t, sample())

	for i := 0; i < 20; i++ { // 模拟 20 分钟的 60 秒轮询
		b := BuildBoard(nil, fetcher.KindIndustry, true)
		if len(b.Inflow) == 0 {
			t.Fatalf("第 %d 次调用没数据", i+1)
		}
	}
	if *n != 1 {
		t.Errorf("TTL 内 20 次调用应只回源 1 次，实际 %d 次 —— 自动刷新会把东财打爆", *n)
	}
}

func TestBuildBoardRefetchesAfterTTL(t *testing.T) {
	ResetCache()
	n := countingFetch(t, sample())

	_ = BuildBoard(nil, fetcher.KindIndustry, true)
	expireCache()
	_ = BuildBoard(nil, fetcher.KindIndustry, true)

	if *n != 2 {
		t.Errorf("过了 TTL 应重新回源，期望 2 次，实际 %d 次 —— 数据会永远停在第一次", *n)
	}
}

func TestBuildBoardCachesDedupVariantsSeparately(t *testing.T) {
	// 去重开/关结果不同，共用一份缓存的话，用户切开关会看到上一个开关的结果
	ResetCache()
	rows := realRows(t) // 8 条，其中 6 条通信系
	n := countingFetch(t, rows)

	on := BuildBoard(nil, fetcher.KindIndustry, true)
	off := BuildBoard(nil, fetcher.KindIndustry, false)

	if *n != 2 {
		t.Errorf("两种去重模式应各自回源一次，实际 %d 次", *n)
	}
	if len(on.All) == len(off.All) {
		t.Fatalf("两种模式结果应不同，却都是 %d 组 —— 缓存串了", len(on.All))
	}
	// 再各调一次，都应命中各自的缓存
	on2 := BuildBoard(nil, fetcher.KindIndustry, true)
	off2 := BuildBoard(nil, fetcher.KindIndustry, false)
	if *n != 2 {
		t.Errorf("第二轮应全部命中缓存，实际回源 %d 次", *n)
	}
	if len(on2.All) != len(on.All) || len(off2.All) != len(off.All) {
		t.Error("缓存返回的结果与首次不一致 —— 两种模式的缓存互相覆盖了")
	}
}

func TestBuildBoardCacheHitReturnsCopy(t *testing.T) {
	// 命中缓存时返回副本：调用方改了字段不该污染缓存里那份
	ResetCache()
	countingFetch(t, sample())

	b1 := BuildBoard(nil, fetcher.KindIndustry, true)
	b1.Notice = "调用方乱改的"
	b1.Stale = true

	b2 := BuildBoard(nil, fetcher.KindIndustry, true)
	if b2.Notice != "" || b2.Stale {
		t.Error("缓存被上一次调用污染了 —— 命中时必须返回副本")
	}
}

func TestCacheTTLIsSaneForPolling(t *testing.T) {
	// 产品稿 Q1=B：前端 60 秒轮询 + 后端 TTL 5 分钟 → 上限 12 次/小时。
	// 有人把 TTL 调小时，这条会提醒他重新算一遍请求量。
	perHour := int(time.Hour / CacheTTL)
	if perHour > 20 {
		t.Errorf("TTL=%v 意味着每小时最多 %d 次回源，已接近限流量级；"+
			"改小之前请重新核算（项目记录：200 个请求会被限流）", CacheTTL, perHour)
	}
}
