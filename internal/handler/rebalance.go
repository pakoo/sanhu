package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/rebalance"
)

func (h *Handler) GetRebalanceSuggestions(c *gin.Context) {
	holdings, total, err := loadPortfolioHoldings(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	suggestions, err := rebalance.GetRebalanceSuggestions(h.DB, toRebalanceInputs(holdings), total)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"suggestions": suggestions})
}

func (h *Handler) GetTargetAllocation(c *gin.Context) {
	allocs, err := rebalance.GetTargetAllocation(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make(map[string]float64, len(allocs))
	for _, alloc := range allocs {
		out[alloc.Category] = alloc.TargetPct
	}
	c.JSON(http.StatusOK, gin.H{"allocations": out})
}

func (h *Handler) SetTargetAllocation(c *gin.Context) {
	var req struct {
		Allocations map[string]float64 `json:"allocations"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	allocs := make([]rebalance.TargetAllocation, 0, len(req.Allocations))
	for category, pct := range req.Allocations {
		allocs = append(allocs, rebalance.TargetAllocation{
			Category:  category,
			TargetPct: pct,
		})
	}
	if err := rebalance.SetTargetAllocation(h.DB, allocs); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) GetTransitionPlan(c *gin.Context) {
	months := parseIntDefault(c.DefaultQuery("months", "6"), 6)
	monthlyInvest := parseFloat64(c.DefaultQuery("monthly_invest", "5000"))

	holdings, total, err := loadPortfolioHoldings(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	plan, err := rebalance.GradualTransitionPlan(h.DB, toRebalanceInputs(holdings), total, monthlyInvest, months)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"transition_plan": plan})
}
