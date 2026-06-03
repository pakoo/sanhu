package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
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
	id, err := parseInt64Param(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	decision, err := decisions.GetDecision(h.DB, id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	md := buildMarkdown(fmt.Sprintf("Decision %d", decision.ID), []string{
		fmt.Sprintf("- code: %s", decision.Code),
		fmt.Sprintf("- decision_type: %s", decision.DecisionType),
		fmt.Sprintf("- status: %s", decision.Status),
		fmt.Sprintf("- created_at: %s", decision.CreatedAt),
		fmt.Sprintf("- notes: %s", strings.TrimSpace(decision.Notes.String)),
		fmt.Sprintf("- is_virtual: %d", decision.IsVirtual),
		fmt.Sprintf("- virtual_entry_nav: %v", decision.VirtualEntryNav),
		fmt.Sprintf("- target_tp_pct: %v", decision.TargetTPPct),
		fmt.Sprintf("- target_sl_pct: %v", decision.TargetSLPct),
	})
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(md))
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
