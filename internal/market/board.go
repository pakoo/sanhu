// Package market 组装「市场」板块的数据：全球指标 + AI 关注区 + 宏观关键少数。
//
// 设计要点（来自 docs/product-specs/market.html）：
//   - 用户每天只看 1-2 次 → 打开时抓一次，不做自动轮询
//   - 单个数据源失败只影响对应分组，返回上次缓存 + 标注，绝不整页报错
//   - 不落库：行情是易失数据，没有历史图表需求，缓存放进程内存
package market

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pakoo/sanhu/internal/fetcher"
)

// Item 前端渲染用的一条指标
type Item struct {
	Code      string  `json:"code"`   // 数据源代码，前端不显示
	Name      string  `json:"name"`   // 显示名（可被本地映射覆盖）
	Sub       string  `json:"sub"`    // 副标，如 COMEX / 300308 · 光模块
	Price     float64 `json:"price"`
	Change    float64 `json:"change"`
	ChangePct float64 `json:"change_pct"`
	Unit      string  `json:"unit"` // 价格前缀，如 $ ¥
	Time      string  `json:"time"`
}

// Group 一组指标。Stale=true 表示这组用的是缓存（源刚才失败了）。
type Group struct {
	Key     string `json:"key"`
	Title   string `json:"title"`
	Items   []Item `json:"items"`
	Stale   bool   `json:"stale"`    // 本次抓取失败，展示的是缓存
	Error   string `json:"error"`    // 失败原因，前端显示在卡头
	FetchAt string `json:"fetch_at"` // 这组数据的成功抓取时间
}

// Board 市场板块的完整数据
type Board struct {
	Signals  []Item   `json:"signals"`  // 今日信号：异动挑出来的
	Macro    Macro    `json:"macro"`    // 宏观关键少数
	Groups   []Group  `json:"groups"`   // 全球指标分组
	AIUS     Group    `json:"ai_us"`    // AI · 美股
	AICN     Group    `json:"ai_cn"`    // AI · A股
	AIEvents []Event  `json:"ai_events"`// AI 事件日历
	FetchAt  string   `json:"fetch_at"`
	Warnings []string `json:"warnings"`
}

// ── 指标定义 ────────────────────────────────────────────────────────────────
// 名称用本地定义而非数据源返回值：新浪对同一品种偶尔换叫法，
// 且「纽约黄金」比「GC」对普通用户友好。

type spec struct{ code, name, sub, unit string }

var (
	commodities = []spec{
		{"hf_GC", "纽约黄金", "COMEX", "$"},
		{"hf_CL", "纽约原油", "WTI", "$"},
		{"hf_SI", "纽约白银", "COMEX", "$"},
		{"hf_HG", "纽约铜", "COMEX · 美分/磅", ""},
	}
	indices = []spec{
		{"gb_$dji", "道琼斯", "美股", ""},
		{"gb_ixic", "纳斯达克", "美股", ""},
		{"gb_$inx", "标普500", "美股", ""},
		{"int_hangseng", "恒生指数", "港股", ""},
		{"int_nikkei", "日经225", "日本", ""},
		{"sh000001", "上证指数", "A股", ""},
	}
	currencies = []spec{
		{"DINIW", "美元指数", "DXY", ""},
		{"fx_susdcny", "美元/人民币", "在岸", ""},
		{"fx_susdjpy", "美元/日元", "", ""},
		{"fx_seurusd", "欧元/美元", "", ""},
	}
	sentiment = []spec{
		{"znb_VIX", "VIX 恐慌指数", "美股波动率", ""},
	}
	cryptos = []spec{
		{"BTC-USDT", "比特币", "OKX", "$"},
		{"ETH-USDT", "以太坊", "OKX", "$"},
	}

	// DefaultAIUS / DefaultAICN 是 AI 关注区的预置名单（产品稿 Q4 = 预置 + 可增删）。
	// 用户改过之后存进 user_settings.ai_watchlist，这里只作为初始值和「恢复默认」的依据。
	DefaultAIUS = []spec{
		{"gb_nvda", "英伟达", "NVDA · GPU", "$"},
		{"gb_msft", "微软", "MSFT · 云与模型", "$"},
		{"gb_googl", "谷歌", "GOOGL · 模型与TPU", "$"},
		{"gb_tsm", "台积电", "TSM · 代工", "$"},
		{"gb_amd", "AMD", "AMD · GPU", "$"},
		{"gb_meta", "Meta", "META · 开源模型", "$"},
	}
	DefaultAICN = []spec{
		{"sz300308", "中际旭创", "300308 · 光模块", "¥"},
		{"sh688256", "寒武纪", "688256 · 算力芯片", "¥"},
		{"sz300502", "新易盛", "300502 · 光模块", "¥"},
		{"sh688041", "海光信息", "688041 · CPU/DCU", "¥"},
		{"sh601138", "工业富联", "601138 · AI服务器", "¥"},
	}
)

// ── 缓存 ────────────────────────────────────────────────────────────────────

type cacheEntry struct {
	items   []Item
	fetchAt time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cacheEntry{}
)

func putCache(key string, items []Item) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cache[key] = cacheEntry{items: items, fetchAt: time.Now()}
}

func getCache(key string) ([]Item, time.Time, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	e, ok := cache[key]
	return e.items, e.fetchAt, ok
}

// ── 组装 ────────────────────────────────────────────────────────────────────

// signalThresholdPct 进「今日信号」条的门槛：涨跌幅绝对值超过它才算异动。
// 安静的日子信号条就短甚至为空 —— 这正是想要的效果，不用凑数。
const signalThresholdPct = 1.0

// BuildBoard 抓取并组装整个市场板块。
//
// aiUS / aiCN 传 nil 时用预置名单。永远返回可渲染的 Board：
// 某组抓取失败就用缓存并标 Stale，全失败也只是各组都为空 + warnings 有说明。
func BuildBoard(aiUS, aiCN []spec) *Board {
	if len(aiUS) == 0 {
		aiUS = DefaultAIUS
	}
	if len(aiCN) == 0 {
		aiCN = DefaultAICN
	}

	// 新浪的都能一次批量拿：省请求数，也避免被限流（push2 的教训）
	sinaSpecs := map[string][]spec{
		"commodity": commodities,
		"index":     indices,
		"currency":  currencies,
		"sentiment": sentiment,
		"ai_us":     aiUS,
		"ai_cn":     aiCN,
	}
	var allCodes []string
	for _, list := range sinaSpecs {
		for _, s := range list {
			allCodes = append(allCodes, s.code)
		}
	}

	board := &Board{FetchAt: time.Now().Format("2006-01-02 15:04:05")}

	quotes, sinaErr := fetcher.FetchSinaQuotes(allCodes)
	build := func(key, title string, list []spec) Group {
		g := Group{Key: key, Title: title}
		if sinaErr == nil {
			items := toItems(list, quotes)
			// 一条都没解析出来也按失败处理，否则会显示一张空卡片让人以为没数据
			if len(items) > 0 {
				putCache(key, items)
				g.Items = items
				g.FetchAt = board.FetchAt
				return g
			}
		}
		g.Stale = true
		g.Error = "行情源暂时不可用"
		if sinaErr != nil {
			g.Error = "行情源请求失败"
		}
		if cached, at, ok := getCache(key); ok {
			g.Items = cached
			g.FetchAt = at.Format("2006-01-02 15:04:05")
		}
		return g
	}

	board.Groups = []Group{
		build("commodity", "商品", commodities),
		build("index", "全球股指", indices),
		build("currency", "汇率", currencies),
	}

	// 加密与情绪合成一组：VIX 来自新浪、币价来自 OKX，两个源要分别降级
	sentGroup := build("sentiment", "加密与情绪", sentiment)
	cryptoItems, okxErr := fetchCrypto()
	if okxErr == nil && len(cryptoItems) > 0 {
		putCache("crypto", cryptoItems)
		sentGroup.Items = append(cryptoItems, sentGroup.Items...)
	} else {
		if cached, _, ok := getCache("crypto"); ok {
			sentGroup.Items = append(cached, sentGroup.Items...)
		}
		sentGroup.Stale = true
		if sentGroup.Error == "" {
			sentGroup.Error = "加密行情源不可用"
		}
		board.Warnings = append(board.Warnings, "加密货币行情获取失败，显示的是上次缓存")
	}
	board.Groups = append(board.Groups, sentGroup)

	board.AIUS = build("ai_us", "AI · 美股核心", aiUS)
	board.AICN = build("ai_cn", "AI · A股产业链", aiCN)
	board.AIEvents = UpcomingEvents(6)
	board.Macro = CurrentMacro()

	if sinaErr != nil {
		board.Warnings = append(board.Warnings, "行情源请求失败，页面显示的是上次缓存数据")
	}

	board.Signals = pickSignals(board)
	return board
}

func fetchCrypto() ([]Item, error) {
	ids := make([]string, 0, len(cryptos))
	for _, c := range cryptos {
		ids = append(ids, c.code)
	}
	quotes, err := fetcher.FetchOKXTickers(ids)
	if err != nil {
		return nil, err
	}
	return toItems(cryptos, quotes), nil
}

func toItems(list []spec, quotes map[string]fetcher.Quote) []Item {
	out := make([]Item, 0, len(list))
	for _, s := range list {
		q, ok := quotes[s.code]
		if !ok {
			continue // 单个代码取不到就跳过，不影响同组其他条目
		}
		out = append(out, Item{
			Code:      s.code,
			Name:      s.name,
			Sub:       s.sub,
			Price:     q.Price,
			Change:    q.Change,
			ChangePct: q.ChangePct,
			Unit:      s.unit,
			Time:      q.Time,
		})
	}
	return out
}

// pickSignals 挑出值得看一眼的异动，按涨跌幅绝对值降序，最多 6 条。
// 挑不出来就是空的 —— 说明今天市场很安静，这本身就是有用的信息。
func pickSignals(b *Board) []Item {
	var pool []Item
	for _, g := range b.Groups {
		if !g.Stale {
			pool = append(pool, g.Items...)
		}
	}
	if !b.AIUS.Stale {
		pool = append(pool, b.AIUS.Items...)
	}
	if !b.AICN.Stale {
		pool = append(pool, b.AICN.Items...)
	}

	var out []Item
	for _, it := range pool {
		if abs(it.ChangePct) >= signalThresholdPct {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return abs(out[i].ChangePct) > abs(out[j].ChangePct)
	})
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// ── AI 名单的持久化格式 ──────────────────────────────────────────────────────

// WatchItem 用户自定义的 AI 关注项，存进 user_settings.ai_watchlist
type WatchItem struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Sub  string `json:"sub"`
	Unit string `json:"unit"`
}

func SpecsToWatch(list []spec) []WatchItem {
	out := make([]WatchItem, 0, len(list))
	for _, s := range list {
		out = append(out, WatchItem{Code: s.code, Name: s.name, Sub: s.sub, Unit: s.unit})
	}
	return out
}

func WatchToSpecs(list []WatchItem) []spec {
	out := make([]spec, 0, len(list))
	for _, w := range list {
		if strings.TrimSpace(w.Code) == "" {
			continue
		}
		out = append(out, spec{code: w.Code, name: w.Name, sub: w.Sub, unit: w.Unit})
	}
	return out
}

// DefaultWatchlists 供 handler 返回「恢复默认」用
func DefaultWatchlists() (us, cn []WatchItem) {
	return SpecsToWatch(DefaultAIUS), SpecsToWatch(DefaultAICN)
}
