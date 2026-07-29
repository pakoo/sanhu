package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/market"
)

// 市场板块（docs/product-specs/market.html）的接口。
//
// 抓取放后端而不是前端直连：新浪与 OKX 都不带 CORS 头，
// 新浪还要求特定 Referer —— 浏览器直接请求两条都过不去。

const aiWatchlistKey = "ai_watchlist"

type aiWatchlist struct {
	US []market.WatchItem `json:"us"`
	CN []market.WatchItem `json:"cn"`
}

// GetGlobalMarket 市场板块的全部数据，一次返回。
//
// 永远返回 200：某个源失败时对应分组带 stale 标记和缓存数据，
// 前端据此把卡片灰化并显示「点此重试」，而不是整页报错
// —— 免费行情源必然会间歇失败，这是常态不是异常。
func (h *Handler) GetGlobalMarket(c *gin.Context) {
	wl := h.loadAIWatchlist()
	board := market.BuildBoard(
		market.WatchToSpecs(wl.US),
		market.WatchToSpecs(wl.CN),
	)
	ok(c, board)
}

// GetAIWatchlist 返回当前 AI 关注名单（未设置过则返回预置名单）
func (h *Handler) GetAIWatchlist(c *gin.Context) {
	wl := h.loadAIWatchlist()
	defUS, defCN := market.DefaultWatchlists()
	ok(c, gin.H{
		"us":         wl.US,
		"cn":         wl.CN,
		"default_us": defUS,
		"default_cn": defCN,
	})
}

// SaveAIWatchlist 保存 AI 关注名单。
// 传 {"reset":true} 恢复预置名单。
func (h *Handler) SaveAIWatchlist(c *gin.Context) {
	var req struct {
		US    []market.WatchItem `json:"us"`
		CN    []market.WatchItem `json:"cn"`
		Reset bool               `json:"reset"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	wl := aiWatchlist{US: req.US, CN: req.CN}
	if req.Reset {
		wl.US, wl.CN = market.DefaultWatchlists()
	}

	raw, err := json.Marshal(wl)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.saveKV(aiWatchlistKey, string(raw)); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"us": wl.US, "cn": wl.CN})
}

// LookupQuote 校验一个行情代码能不能取到数据，供「添加到 AI 名单」时即时反馈。
// 与录入基金时的即时校验同一个思路：填错的代码当场拦住，不要等保存后才发现是空卡片。
func (h *Handler) LookupQuote(c *gin.Context) {
	raw := strings.TrimSpace(c.Query("code"))
	if raw == "" {
		fail(c, http.StatusBadRequest, "缺少 code 参数")
		return
	}

	code, unit := market.NormalizeQuoteCode(raw)
	if code == "" {
		fail(c, http.StatusBadRequest, "无法识别的代码；美股直接填代码如 NVDA，A股填 6 位数字如 300308")
		return
	}

	q, err := market.LookupOne(code)
	if err != nil || q.Name == "" {
		fail(c, http.StatusNotFound, "没有查到 "+raw+" 的行情，请检查代码")
		return
	}
	ok(c, gin.H{
		"code":  code,
		"name":  q.Name,
		"sub":   raw,
		"unit":  unit,
		"price": q.Price,
	})
}

// loadAIWatchlist 读取名单，未设置或解析失败都回落到预置名单
func (h *Handler) loadAIWatchlist() aiWatchlist {
	defUS, defCN := market.DefaultWatchlists()
	fallback := aiWatchlist{US: defUS, CN: defCN}

	raw, err := h.loadKV(aiWatchlistKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return fallback
	}
	var wl aiWatchlist
	if err := json.Unmarshal([]byte(raw), &wl); err != nil {
		return fallback
	}
	// 两个都空说明用户把名单清光了，尊重这个选择，不强行塞回预置
	return wl
}

// ── user_settings 的通用键值读写 ────────────────────────────────────────────
// 复用既有的 kv 结构（key/value 两列，见 migrations/001_init.sql）

func (h *Handler) loadKV(key string) (string, error) {
	var v string
	err := h.DB.Get(&v, `SELECT value FROM user_settings WHERE key=?`, key)
	return v, err
}

func (h *Handler) saveKV(key, value string) error {
	_, err := h.DB.Exec(`
		INSERT INTO user_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
