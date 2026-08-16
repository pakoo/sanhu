package sectorflow

import (
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/fetcher"
)

// 榜单组装与降级。
//
// 降级链条（照抄 internal/market 已验证有效的 Stale 模式，再加一层库兜底）：
//
//	实时抓取 → 失败退进程内存缓存 → 再失败退数据库最近一天 → 都没有才返回空
//
// 为什么值得铺这么长：实测东财对 push2 的封禁**按 IP 生效且可达数小时**
//（见 fundflow.go 文件头）。这意味着「抓不到」不是几秒钟的抖动，而可能是
// 一整个下午 —— 降级链条因此从锦上添花变成必需品。给用户昨天的数据 +
// 一句「数据截至 X」，比一张空白页有用得多。

// Board 一次榜单请求的完整结果
type Board struct {
	Kind    string  `json:"kind"`
	Inflow  []Group `json:"inflow"`  // 净流入 TopN
	Outflow []Group `json:"outflow"` // 净流出 TopN（最负的在前）
	All     []Group `json:"all"`     // 去重后的全量，热力图用

	FetchAt  string `json:"fetch_at"`  // 这批数据的获取时间
	DataDate string `json:"data_date"` // 数据对应的交易日
	Stale    bool   `json:"stale"`     // true = 实时抓取失败，这是降级数据
	Source   string `json:"source"`    // live / cache / db —— 前端据此决定提示语
	Notice   string `json:"notice"`    // 给用户看的一句话说明，Stale 时必填
	// Detail 原始错误，只给排查用。不并进 Notice 是因为那串东西里有完整请求 URL，
	// 直接怼到界面上既吓人又没信息量；但项目习惯是「报错要可复制」，所以留一个字段。
	Detail string `json:"detail,omitempty"`
}

// TopN 榜单每侧取几条
const boardTopN = 10

// CacheTTL 榜单的新鲜期（产品稿 fundflow-replay.html · Q1=B）。
//
// 这个常量的意义是**把「页面多久刷一次」和「多久打一次东财」解耦**。
// 在它之前，BuildBoard 每次被调用都无条件回源 —— 那样前端一旦开启 60 秒轮询，
// 就是每小时 60 个东财请求，而项目记录里 200 个请求就会被限流
// （holdings_fetcher.go:109）。加了 TTL 之后，前端爱怎么轮询都行，
// 东财请求量只由这个值决定。
//
// 取 5 分钟：板块资金流是慢变量，5 分钟的滞后在「今天谁被买」这个问题上看不出来；
// 而它把上限压到 12 次/小时，离限流量级很远。
//
// 它同时还是一道保险 —— 防用户狂点「刷新」按钮把自己打限流。
const CacheTTL = 5 * time.Minute

// fetchRank 抓取入口做成变量，好让测试能在不联网的情况下模拟
// 「东财限流」这一分支 —— 而降级链条恰恰是限流时才跑到的代码，
// 靠手工验证几乎不可能覆盖（得先真把自己封了）。
var fetchRank = fetcher.FetchSectorFlowRank

type cacheEntry struct {
	board   *Board
	fetchAt time.Time
}

// cacheKey 去重开关不同，组装结果就不同，必须分开缓存 ——
// 混用的话，用户切一下「合并同源板块」会看到上一个开关的结果。
func cacheKey(kind fetcher.SectorKind, dedup bool) string {
	if dedup {
		return string(kind) + ":dedup"
	}
	return string(kind) + ":raw"
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cacheEntry{}
)

// BuildBoard 组装某一类板块的榜单。
//
// dedup=false 时跳过层级折叠，给「我就要看原始榜」的用户用。
// db 可以为 nil（不落库、也不查库），方便测试和纯内存场景。
func BuildBoard(db *sqlx.DB, kind fetcher.SectorKind, dedup bool) *Board {
	now := time.Now()
	today := now.Format("2006-01-02")
	key := cacheKey(kind, dedup)

	// ── 命中 TTL 就直接返回，不回源 ──
	// 这是自动刷新能成立的前提：前端每 60 秒问一次，绝大多数都落在这里，
	// 打的是本地内存，一个外部请求都不产生。
	if c, ok := getCache(key); ok && c.board != nil && now.Sub(c.fetchAt) < CacheTTL {
		b := *c.board // 返回副本：调用方若改了字段，不该污染缓存
		return &b
	}

	rows, err := fetchRank(kind)
	if err == nil && len(rows) > 0 {
		b := assemble(kind, rows, dedup)
		b.FetchAt = now.Format("2006-01-02 15:04:05")
		// 日期取**数据自己的**时间戳，不是「今天」。周末和节假日打过去，
		// 东财返回的是上一个交易日的数据 —— 标成今天就是在骗用户，
		// 而且他没法从界面上看出这是三天前的行情。拿不到才退回今天。
		b.DataDate = today
		if rows[0].DataDate != "" {
			b.DataDate = rows[0].DataDate
		}
		b.Source = "live"
		putCache(key, b)
		return b
	}

	// ── 降级一：进程内存缓存 ──
	// 盘中每 30 分钟刷一次，缓存里通常是几十分钟前的数据，对「今天谁被买」够用。
	if c, ok := getCache(key); ok && c.board != nil {
		b := *c.board // 复制一份再改，别把缓存里的对象标成 stale
		b.Stale = true
		b.Source = "cache"
		b.Notice = "东财接口暂时取不到数据（多半是限流），下面是 " +
			c.fetchAt.Format("15:04") + " 抓到的结果"
		return &b
	}

	// ── 降级二：数据库里最近一个交易日 ──
	// 刚启动、内存缓存还是空的时候走这条。
	if db != nil {
		if dbRows, date, dbErr := LoadRank(db, kind, ""); dbErr == nil && len(dbRows) > 0 {
			b := assemble(kind, dbRows, dedup)
			b.DataDate = date
			b.FetchAt = ""
			b.Stale = true
			b.Source = "db"
			b.Notice = "实时数据取不到，下面是库里最近一次的结果（" + date + "）"
			return b
		}
	}

	// 全都没有：首次启动 + 正好被限流。给空结果和明确说明，不报 500 ——
	// 让整页崩掉不如让这张卡片自己说清楚发生了什么。
	// 切片给空数组而不是 nil：序列化成 [] 前端就不必到处判空。
	b := &Board{
		Kind: string(kind), Stale: true, Source: "none",
		Inflow: []Group{}, Outflow: []Group{}, All: []Group{},
		// 说清楚「可能要等挺久」。实测东财对 push2 的封禁按 IP 生效、可持续数小时，
		// 只说「稍后自动恢复」会让用户反复点刷新（而反复请求只会加重限流）。
		Notice: "暂时拿不到板块资金流数据。东财的行情接口对短时间内的密集请求限流较严，" +
			"一旦触发可能要等几个小时才恢复 —— 期间反复刷新只会更慢，等它自己好即可。" +
			"净值、基金档案等其它功能不受影响。",
	}
	if err != nil {
		b.Detail = err.Error()
	}
	return b
}

func assemble(kind fetcher.SectorKind, rows []fetcher.SectorFlow, dedup bool) *Board {
	var groups []Group
	if dedup {
		groups = Dedup(rows)
	} else {
		groups = make([]Group, 0, len(rows))
		for _, r := range rows {
			groups = append(groups, Group{SectorFlow: r})
		}
	}
	in, out := TopN(groups, boardTopN)
	return &Board{Kind: string(kind), Inflow: in, Outflow: out, All: groups}
}

// putCache 存的是**副本**。
//
// 直接存调用方那个指针的话，抓取成功的那条路径会把同一个对象既放进缓存又返回出去，
// 调用方之后改任何字段都会污染缓存 —— 表现是「某次请求给 Board 打了 stale 标记，
// 之后所有命中缓存的请求都带着这个标记」，很难查。
func putCache(key string, b *Board) {
	cp := *b
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cache[key] = cacheEntry{board: &cp, fetchAt: time.Now()}
}

func getCache(key string) (cacheEntry, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	e, ok := cache[key]
	return e, ok
}

// ResetCache 清空内存缓存，只给测试用
func ResetCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cache = map[string]cacheEntry{}
}

// expireCache 把所有缓存条目的时间戳往前拨，让它们越过 TTL。只给测试用。
//
// 有了 TTL 之后，「抓取失败时降级到缓存」这条路径必须先让缓存过期才走得到 ——
// 否则 TTL 命中会直接返回，测试根本测不到降级逻辑。
func expireCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	for k, e := range cache {
		e.fetchAt = e.fetchAt.Add(-CacheTTL - time.Minute)
		cache[k] = e
	}
}
