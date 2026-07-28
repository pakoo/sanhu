package handler

import (
	"math"
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
	// 前端是照着 Python 的字段名写的，这里必须逐个对上，否则页面直接抛异常：
	//   Python/前端  →  Go 内部
	//   delta_pct        gap_pct（符号相反！Python 是 目标−当前，Go 是 当前−目标）
	//   amount           amount_suggest
	//   fund_suggestions candidates（且可能为 nil，前端会 .map 崩掉）
	//   category_name    无
	// 之前直接把内部结构体序列化出去，调仓建议整块渲染不出来（控制台 fund_suggestions.map of undefined）。
	categoryNames := map[string]string{
		"bond": "债券型", "equity": "股票型", "mixed": "混合型", "qdii": "QDII/海外", "money": "货币型",
	}

	current := map[string]float64{}
	target := map[string]float64{}
	comparison := map[string]map[string]float64{}
	totalAdj := 0.0
	out := make([]map[string]interface{}, 0, len(suggestions))

	for _, s := range suggestions {
		curPct := math.Round(s.CurrentPct*100) / 100
		current[s.Category] = curPct
		target[s.Category] = s.TargetPct
		totalAdj += math.Abs(s.AmountSuggest)

		// 取反：对外统一用「还需调整多少」的口径（正=该加仓，负=该减仓）
		deltaPct := math.Round(-s.GapPct*100) / 100
		amount := math.Round(-s.GapPct/100*total*100) / 100

		funds := s.Candidates
		if funds == nil {
			funds = []string{} // 保证前端 .map 不会崩
		}

		out = append(out, map[string]interface{}{
			"category":         s.Category,
			"category_name":    categoryNames[s.Category],
			"current_pct":      curPct,
			"target_pct":       s.TargetPct,
			"delta_pct":        deltaPct,
			"action":           s.Action,
			"amount":           amount,
			"fund_suggestions": funds,
			"note":             s.Note,
		})

		comparison[s.Category] = map[string]float64{
			"current_pct": curPct,
			"target_pct":  s.TargetPct,
			"diff":        math.Round((s.TargetPct-curPct)*100) / 100,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"suggestions":        out,
		"current_allocation": current,
		"target_allocation":  target,
		"total_adjustment":   math.Round(totalAdj/2*100) / 100,
		"comparison":         comparison,
	})
}

func (h *Handler) GetTargetAllocation(c *gin.Context) {
	allocs, err := rebalance.GetTargetAllocation(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	// 与 Python get_target_allocation 及前端契约对齐：
	// 顶层就是 {category: {...}}，不要再包一层 allocations。
	// 包了这一层会让前端 renderTargetSliders 的 Object.entries 遍历到 "allocations" 这个键本身，
	// 于是滑杆标签显示成 "allocations"、数值显示成 undefined%（目标配置页实测如此）。
	out := make(map[string]map[string]interface{}, len(allocs))
	for _, alloc := range allocs {
		out[alloc.Category] = map[string]interface{}{
			"category":   alloc.Category,
			"target_pct": alloc.TargetPct,
		}
	}
	c.JSON(http.StatusOK, out)
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
