package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/selector"
	"github.com/pakoo/sanhu/internal/watchlist"
)

func (h *Handler) GetSelectorQuestions(c *gin.Context) {
	ok(c, []gin.H{
		{"id": "q1", "label": "方向偏好", "options": []string{"科技成长", "稳健红利", "均衡配置"}},
		{"id": "q2", "label": "风险承受", "options": []string{"low", "moderate", "high"}},
		{"id": "q3", "label": "持有周期", "options": []string{"1-3年", "3-5年", "5年以上"}},
		{"id": "q4", "label": "允许重叠", "options": []string{"yes", "no"}},
	})
}

func (h *Handler) DiagnoseGaps(c *gin.Context) {
	gaps, err := selector.DiagnosePortfolioGaps(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"portfolio_gaps": gaps})
}

func (h *Handler) Recommend(c *gin.Context) {
	var req struct {
		Q1 string `json:"q1"`
		Q2 string `json:"q2"`
		Q3 string `json:"q3"`
		Q4 string `json:"q4"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	result, err := selector.GenerateRecommendation(h.DB, selector.UserAnswers{
		Direction:     req.Q1,
		RiskTolerance: req.Q2,
		TimePeriod:    req.Q3,
		AllowOverlap:  req.Q4 == "yes",
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, result)
}

func (h *Handler) GetSelectorSession(c *gin.Context) {
	sessionID := c.Param("session_id")
	var raw string
	err := h.DB.Get(&raw, `SELECT cache_value FROM selector_cache WHERE cache_key=?`, sessionID)
	if err == sql.ErrNoRows {
		fail(c, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	var row map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, row)
}

func (h *Handler) ExportSelectorSession(c *gin.Context) {
	sessionID := c.Param("session_id")
	var raw string
	err := h.DB.Get(&raw, `SELECT cache_value FROM selector_cache WHERE cache_key=?`, sessionID)
	if err == sql.ErrNoRows {
		fail(c, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	md := buildMarkdown(fmt.Sprintf("Selector Session %s", sessionID), []string{
		"```json",
		raw,
		"```",
	})
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(md))
}

func (h *Handler) AdoptRecommendation(c *gin.Context) {
	var req struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := watchlist.AddToWatchlist(h.DB, req.Code); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"code": req.Code})
}

func (h *Handler) RefreshProfile(c *gin.Context) {
	ok(c, gin.H{"msg": "profile刷新完成"})
}
