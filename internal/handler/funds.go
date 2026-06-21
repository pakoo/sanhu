package handler

import (
	"io/fs"
	"net/http"

	"github.com/gin-gonic/gin"
	sanhugo "github.com/pakoo/sanhu"
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

func (h *Handler) GetFundRealtime(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"error": "not implemented"})
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

func (h *Handler) GetFundAIThemePrompt(c *gin.Context) {
	content, err := fs.ReadFile(sanhugo.PromptsFS, "prompts/theme_analysis.md")
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"prompt": string(content)})
}
