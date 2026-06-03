package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/simulation"
)

func (h *Handler) GetSimulationStrategies(c *gin.Context) {
	ok(c, []gin.H{
		{"name": "dca_monthly", "label": "每月定投", "params_schema": gin.H{"amount": "每次买入金额（元）"}},
	})
}

func (h *Handler) CreateSimulation(c *gin.Context) {
	var req struct {
		StrategyName  string      `json:"strategy_name"`
		ParamsJSON    interface{} `json:"params_json"`
		FundPoolJSON  interface{} `json:"fund_pool_json"`
		Mode          string      `json:"mode"`
		StartDate     string      `json:"start_date"`
		EndDate       string      `json:"end_date"`
		InitialCash   float64     `json:"initial_cash"`
		MonthlyAmount float64     `json:"monthly_amount"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	paramsJSON, err := jsonString(req.ParamsJSON)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	fundPoolJSON, err := jsonString(req.FundPoolJSON)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	id, err := simulation.CreateSimulation(h.DB, req.StrategyName, paramsJSON, fundPoolJSON, req.Mode, req.StartDate, req.EndDate, req.InitialCash, req.MonthlyAmount)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Mode == "backtest" {
		if err := simulation.RunSimulation(h.DB, id); err != nil {
			fail(c, http.StatusInternalServerError, err.Error())
			return
		}
	}
	ok(c, gin.H{"id": id})
}

func (h *Handler) ListSimulations(c *gin.Context) {
	list, err := simulation.ListSimulations(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, list)
}

func (h *Handler) GetSimulationDetail(c *gin.Context) {
	id, err := parseInt64Param(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	detail, err := simulation.GetSimulationDetail(h.DB, id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, detail)
}

func (h *Handler) RunSimulation(c *gin.Context) {
	id, err := parseInt64Param(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := simulation.RunSimulation(h.DB, id); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{})
}

func (h *Handler) DeleteSimulation(c *gin.Context) {
	id, err := parseInt64Param(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := simulation.DeleteSimulation(h.DB, id); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{})
}

func (h *Handler) AdvanceSimulation(c *gin.Context) {
	id, err := parseInt64Param(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := simulation.AdvanceForwardSimulation(h.DB, id, ""); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{})
}
