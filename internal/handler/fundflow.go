package handler

import (
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/fetcher"
	"github.com/pakoo/sanhu/internal/sectorflow"
)

// 板块资金流 API（产品稿 docs/product-specs/sector-fund-flow.html）。
//
// 三条路由对应产品稿里的三种视图：
//   rank   → 首屏双向排行榜 + 热力图
//   minute → 点开某板块的当日分时曲线
//   daily  → 该板块的历史日线
//
// 所有接口在东财限流时都**不报错**，而是返回降级数据 + stale 标记 + notice 文案，
// 由前端显示「数据截至 X」。理由见 sectorflow/board.go 的降级链条说明。

// bkCodePattern 板块代码形如 BK1215。做白名单校验而不是直接拼进 URL：
// 这个值会进东财的请求串，不校验等于把参数拼接权交给调用方。
var bkCodePattern = regexp.MustCompile(`^BK\d{4}$`)

// dateOnlyPattern 日期参数只允许 YYYY-MM-DD。它会进 SQL 的参数位（不是拼接），
// 但仍然校验：格式不对时早点报错，比让 SQL 返回空结果、用户对着空图猜半天好。
var dateOnlyPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func parseKind(c *gin.Context) (fetcher.SectorKind, bool) {
	switch c.DefaultQuery("kind", "industry") {
	case "industry":
		return fetcher.KindIndustry, true
	case "concept":
		return fetcher.KindConcept, true
	}
	return "", false
}

// GetSectorFlowRank 板块资金流榜单。
//
// GET /api/fundflow/rank?kind=industry&dedup=1
//
// dedup 默认开：不开的话一个热点就能刷屏整张榜（实测 8-14 行业榜前七有六个是
// 通信系）。留出关闭开关是给「我想看原始榜」的场景。
func (h *Handler) GetSectorFlowRank(c *gin.Context) {
	kind, okKind := parseKind(c)
	if !okKind {
		fail(c, http.StatusBadRequest, "kind 只能是 industry 或 concept")
		return
	}
	dedup := c.DefaultQuery("dedup", "1") != "0"

	board := sectorflow.BuildBoard(h.DB, kind, dedup)
	ok(c, board)
}

// GetSectorFlowMinute 某板块的分时资金流曲线。
//
// GET /api/fundflow/minute?bk=BK1215&date=2026-08-13
//
// date 省略 = 当天，走实时抓取（东财的 klt=1 只提供当日）。
// 指定历史日期则查库 —— 查得到全靠收盘后落过盘，落库策略见产品稿 Q4。
func (h *Handler) GetSectorFlowMinute(c *gin.Context) {
	bk := c.Query("bk")
	if !bkCodePattern.MatchString(bk) {
		fail(c, http.StatusBadRequest, "bk 必须是形如 BK1215 的板块代码")
		return
	}
	date := c.Query("date")
	today := time.Now().Format("2006-01-02")

	// 历史日期：只能查库，东财不提供
	if date != "" && date != today {
		pts, err := sectorflow.LoadMinute(h.DB, bk, date)
		if err != nil {
			fail(c, http.StatusInternalServerError, err.Error())
			return
		}
		notice := ""
		if len(pts) == 0 {
			// 说清楚为什么没有，否则用户会以为是 bug。
			// 东财只给当日分时，历史全靠我们自己那天有没有存下来。
			notice = "没有 " + date + " 的分时记录。东财只提供当日分时，历史数据依赖当天收盘后落库；" +
				"该功能上线前的日期查不到属正常。"
		}
		ok(c, gin.H{"bk": bk, "date": date, "points": pts,
			"source": "db", "stale": false, "notice": notice})
		return
	}

	// 当天：实时抓。一次请求就返回全天已有的全部分钟点，不需要轮询。
	pts, err := fetcher.FetchSectorMinute(bk)
	if err == nil && len(pts) > 0 {
		ok(c, gin.H{"bk": bk, "date": today, "points": pts,
			"source": "live", "stale": false, "notice": ""})
		return
	}
	// 抓不到就退今天已落库的部分（盘中落过盘的话会有）
	if pts, dbErr := sectorflow.LoadMinute(h.DB, bk, today); dbErr == nil && len(pts) > 0 {
		ok(c, gin.H{"bk": bk, "date": today, "points": pts, "source": "db", "stale": true,
			"notice": "实时分时取不到（多半是限流），下面是今天已落库的部分"})
		return
	}
	// 今天什么都没有，就退到库里最近有记录的那个交易日。
	// 休市日和限流期间走的都是这条：榜单能显示上一交易日，分时不跟上的话
	// 会出现「榜单有数据、点进去是空的」，用户只会以为坏了。
	if last, lerr := sectorflow.LatestMinuteDate(h.DB, bk); lerr == nil && last != "" {
		if pts, dbErr := sectorflow.LoadMinute(h.DB, bk, last); dbErr == nil && len(pts) > 0 {
			ok(c, gin.H{"bk": bk, "date": last, "points": pts, "source": "db", "stale": true,
				"notice": "今天的分时暂时取不到，下面是最近一个交易日（" + last + "）的曲线"})
			return
		}
	}
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	ok(c, gin.H{"bk": bk, "date": today, "points": []fetcher.FlowPoint{},
		"source": "none", "stale": true, "detail": detail,
		"notice": "暂时拿不到分时数据。东财行情接口触发限流后可能数小时才恢复，期间反复刷新只会更慢。"})
}

// GetSectorFlowDaily 某板块的历史日线资金流。
//
// GET /api/fundflow/daily?bk=BK1215&n=120
//
// 优先查库：库里可能比东财的 120 天上限更长 —— 那正是落库的意义所在。
// 库里没有（功能刚上线）才回源，回源最多也只有 120 天。
func (h *Handler) GetSectorFlowDaily(c *gin.Context) {
	bk := c.Query("bk")
	if !bkCodePattern.MatchString(bk) {
		fail(c, http.StatusBadRequest, "bk 必须是形如 BK1215 的板块代码")
		return
	}
	n, _ := strconv.Atoi(c.DefaultQuery("n", "120"))
	if n <= 0 || n > 2000 {
		n = 120
	}

	if days, err := sectorflow.LoadDaily(h.DB, bk, n); err == nil && len(days) > 0 {
		ok(c, gin.H{"bk": bk, "days": days, "source": "db", "stale": false,
			"notice": "", "count": len(days)})
		return
	}

	days, err := fetcher.FetchSectorDaily(bk)
	if err != nil {
		ok(c, gin.H{"bk": bk, "days": []fetcher.FlowDay{}, "source": "none", "stale": true,
			"count": 0, "detail": err.Error(),
			"notice": "暂时拿不到历史数据。东财行情接口触发限流后可能数小时才恢复，期间反复刷新只会更慢。"})
		return
	}
	if len(days) > n {
		days = days[len(days)-n:]
	}
	ok(c, gin.H{"bk": bk, "days": days, "source": "live", "stale": false, "count": len(days),
		"notice": "东财最多提供 120 个交易日。更长的历史会随着每日落库逐步积累。"})
}

// GetSectorFlowReplay 一天的回放数据（产品稿 fundflow-replay.html）。
//
// GET /api/fundflow/replay?date=2026-08-14
//
// **一次返回全部留档板块的完整分时**，而不是让前端逐个板块请求 ——
// 回放要在时间轴上来回拖动，逐个请求会让拖动变成一场灾难。
// 数据结构上共享时间轴、数值只给数组，50 个板块约 120 KB。
//
// date 省略 = 库里最近一个有留档的交易日。
func (h *Handler) GetSectorFlowReplay(c *gin.Context) {
	date := c.Query("date")
	if date != "" && !dateOnlyPattern.MatchString(date) {
		fail(c, http.StatusBadRequest, "date 格式应为 2026-08-14")
		return
	}

	day, err := sectorflow.LoadReplay(h.DB, date)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	dates, _ := sectorflow.ReplayDates(h.DB, 60)

	if day == nil || len(day.Sectors) == 0 {
		// 说清楚为什么空。回放依赖每日收盘留档，功能上线前的日子永远补不回来
		// （东财只提供当日分时）—— 不讲明白，用户会以为是坏了。
		msg := "还没有可回放的数据。回放依赖每个交易日收盘后的分时留档，" +
			"功能上线后的第一个交易日结束（19:00 落库）才会有第一天可看。"
		if date != "" {
			msg = date + " 没有留档。东财只提供当日分时，历史分时无法补抓 —— " +
				"该日期若在功能上线前，是拿不到的。"
		}
		ok(c, gin.H{"date": date, "times": []string{}, "sectors": []any{},
			"available_dates": dates, "notice": msg})
		return
	}
	ok(c, gin.H{
		"date": day.Date, "times": day.Times, "sectors": day.Sectors,
		"available_dates": dates, "notice": "",
	})
}
