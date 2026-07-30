package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/fetcher"
	"github.com/pakoo/sanhu/internal/scoring"
	"github.com/pakoo/sanhu/internal/selector"
)

func (h *Handler) SearchFunds(c *gin.Context) {
	results, err := fetcher.SearchFund(c.Query("q"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, results)
}

// LookupFund 按代码确认一只基金是否存在，供录入时即时校验。
//
// 库里有档案就直接返回；没有就现抓一次东财（约 1 秒），抓到即入库。
// 录入流程靠它做到「填完代码自动带出名称」，并把填错的代码挡在暂存列表之外——
// 否则会录进一个查无此基金的空壳，最终在持仓列表里显示成一行代码、市值 0。
func (h *Handler) LookupFund(c *gin.Context) {
	code := c.Param("code")

	type fundRow struct {
		Code     string  `db:"code"`
		Name     string  `db:"name"`
		FundType string  `db:"fund_type"`
		Category string  `db:"category"`
		Nav      float64 `db:"nav"`
		NavDate  string  `db:"nav_date"`
	}

	query := `
		SELECT f.code,
		       COALESCE(f.name,'')      AS name,
		       COALESCE(f.fund_type,'') AS fund_type,
		       COALESCE(f.category,'')  AS category,
		       COALESCE((SELECT nav  FROM nav_history WHERE code=f.code ORDER BY date DESC LIMIT 1), 0)  AS nav,
		       COALESCE((SELECT date FROM nav_history WHERE code=f.code ORDER BY date DESC LIMIT 1), '') AS nav_date
		FROM funds f WHERE f.code=?`

	var row fundRow
	err := h.DB.Get(&row, query, code)

	// 库里没有、或只有个占位名（早期录入只写了 code+name 就落库）→ 现抓
	if err != nil || row.FundType == "" {
		if ferr := fetcher.FetchFundInfo(h.DB, code); ferr != nil {
			fail(c, http.StatusNotFound, "没有查到这只基金，请检查代码是否正确")
			return
		}
		if err := h.DB.Get(&row, query, code); err != nil {
			fail(c, http.StatusNotFound, "没有查到这只基金，请检查代码是否正确")
			return
		}
	}
	if row.Name == "" || row.Name == code {
		fail(c, http.StatusNotFound, "没有查到这只基金，请检查代码是否正确")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":      row.Code,
		"name":      row.Name,
		"fund_type": row.FundType,
		"category":  row.Category,
		"nav":       row.Nav,
		"nav_date":  row.NavDate,
	})
}

func (h *Handler) RefreshFunds(c *gin.Context) {
	if !startTask(&navRefreshTask) {
		c.JSON(http.StatusOK, gin.H{"status": "running"})
		return
	}

	go func() {
		codes := allFundCodes(h.DB)
		navRefreshTask.mu.Lock()
		navRefreshTask.Total = len(codes)
		navRefreshTask.mu.Unlock()
		err := fetcher.FetchNavAll(h.DB, 90)
		finishTask(&navRefreshTask, len(codes), err)
	}()

	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "后台刷新已启动"})
}

func (h *Handler) GetRefreshStatus(c *gin.Context) {
	c.JSON(http.StatusOK, holdingsTaskSnapshot(&navRefreshTask))
}

func (h *Handler) GetFundNav(c *gin.Context) {
	days := parseIntDefault(c.DefaultQuery("days", "365"), 365)
	// 与 Python get_nav_history 对齐：裸数组 [{date,nav,acc_nav,daily_return}]，最近 days 行降序。
	type navRow struct {
		Date        string   `db:"date" json:"date"`
		Nav         float64  `db:"nav" json:"nav"`
		AccNav      *float64 `db:"acc_nav" json:"acc_nav"`
		DailyReturn *float64 `db:"daily_return" json:"daily_return"`
	}
	rows := []navRow{}
	if err := h.DB.Select(&rows, `
		SELECT date, nav, acc_nav, daily_return FROM nav_history
		WHERE code=? ORDER BY date DESC LIMIT ?`, c.Param("code"), days); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, rows)
}

// GetFundRealtime 盘中实时估值 —— 数据源已下线，暂无替代。
//
// Python 版用的是 fundgz.1234567.com.cn/js/{code}.js（JSONP 的 gsz/gszzl 字段），
// 2026-07-28 复核：该域名对所有基金代码都返回 404 页面，接口已被东财移除，
// 因此这不是 Go 重写的遗漏 —— 把 Python 原版跑起来同样拿不到数据。
//
// 保留端点是为了不破坏前端契约（api.js 有 getRealtime 封装），
// 返回 503 + 明确原因，比返回一个含糊的 "not implemented" 更容易排查。
// 若要恢复此功能，需要先找到新的估值数据源。
func (h *Handler) GetFundRealtime(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"status": "unavailable",
		"code":   c.Param("code"),
		"msg":    "实时估值数据源已下线（fundgz.1234567.com.cn 已停止服务），暂无替代来源；净值请以每日更新为准",
	})
}

func (h *Handler) GetFundScores(c *gin.Context) {
	scores, err := scoring.GetLatestScores(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"scores": scores})
}

func (h *Handler) RefreshFundScores(c *gin.Context) {
	codes := allFundCodes(h.DB)
	if err := scoring.CalculateAllScores(h.DB, codes); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"count": len(codes)})
}

func (h *Handler) GetFundPeers(c *gin.Context) {
	limit := parseIntDefault(c.DefaultQuery("limit", "10"), 10)
	peers, err := selector.FindPeerFundsByTheme(h.DB, c.Param("code"), limit)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, peers)
}

// GetFundAIThemePrompt 见 ai_prompts.go —— 那里做完整渲染，不再原样返回模板
