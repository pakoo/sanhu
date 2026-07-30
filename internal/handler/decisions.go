package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/aiprompt"
	"github.com/pakoo/sanhu/internal/decisions"
)

func (h *Handler) CreateDecision(c *gin.Context) {
	var payload decisions.CreatePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	id, err := decisions.CreateDecision(h.DB, payload)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}

func (h *Handler) ListDecisions(c *gin.Context) {
	limit := parseIntDefault(c.DefaultQuery("limit", "50"), 50)
	list, err := decisions.ListDecisions(h.DB, c.DefaultQuery("status", ""), c.DefaultQuery("kind", "all"), limit)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, list)
}

func (h *Handler) CheckTriggers(c *gin.Context) {
	if err := decisions.CheckTPSLTriggers(h.DB); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{})
}

func (h *Handler) GetDecision(c *gin.Context) {
	id, err := parseInt64Param(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	perf, err := decisions.GetDecisionPerformance(h.DB, id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, perf)
}

func (h *Handler) LinkTransaction(c *gin.Context) {
	id, err := parseInt64Param(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	var req struct {
		TransactionID int64 `json:"transaction_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := decisions.LinkTransaction(h.DB, id, req.TransactionID); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{})
}

func (h *Handler) ExportDecision(c *gin.Context) {
	// 原来这里吐的是九行 `- code: 012345` 式的键值列表，且前端根本没有按钮调它。
	// 现在转发到 decision_review 场景，输出一份能直接粘给 AI 的复盘提示词。
	b := aiprompt.NewBuilder(aiprompt.Options{Redact: c.Query("redact") == "1"})
	scene, _ := aiprompt.SceneByID("decision_review")
	res, err := h.sceneDecision(b, scene, time.Now().Format("2006-01-02 15:04"), c.Param("id"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if !res.Ready {
		fail(c, http.StatusBadRequest, res.ReadyNote)
		return
	}
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(res.Prompt))
}

func (h *Handler) CancelDecision(c *gin.Context) {
	id, err := parseInt64Param(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := decisions.CancelDecision(h.DB, id); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{})
}
