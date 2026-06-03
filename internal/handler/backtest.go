package handler

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/backtest"
)

func (h *Handler) BacktestDCA(c *gin.Context) {
	var req struct {
		Code          string  `json:"code"`
		MonthlyAmount float64 `json:"monthly_amount"`
		StartDate     string  `json:"start_date"`
		EndDate       string  `json:"end_date"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	result, err := backtest.DcaBacktest(h.DB, req.Code, req.MonthlyAmount, "monthly", req.StartDate, req.EndDate)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, result)
}

func (h *Handler) BacktestTakeProfit(c *gin.Context) {
	var req struct {
		Code          string  `json:"code"`
		TargetReturn  float64 `json:"target_return"`
		MonthlyAmount float64 `json:"monthly_amount"`
		StartDate     string  `json:"start_date"`
		EndDate       string  `json:"end_date"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	result, err := backtest.TakeProfitBacktest(h.DB, req.Code, req.MonthlyAmount, "monthly", req.TargetReturn, 0, req.StartDate, req.EndDate)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, result)
}

func (h *Handler) BacktestPortfolio(c *gin.Context) {
	var req struct {
		Funds []struct {
			Code   string  `json:"code"`
			Weight float64 `json:"weight"`
		} `json:"funds"`
		StartDate     string  `json:"start_date"`
		EndDate       string  `json:"end_date"`
		InitialAmount float64 `json:"initial_amount"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	if len(req.Funds) == 0 {
		ok(c, gin.H{
			"total_invested": req.InitialAmount,
			"final_value":    req.InitialAmount,
			"total_return":   0,
			"annual_return":  0,
			"max_drawdown":   0,
			"curve":          []gin.H{},
		})
		return
	}

	type navRow struct {
		Date string  `db:"date"`
		Nav  float64 `db:"nav"`
	}

	totalWeight := 0.0
	for _, fund := range req.Funds {
		totalWeight += fund.Weight
	}
	if totalWeight == 0 {
		totalWeight = 1
	}

	unionDates := map[string]struct{}{}
	series := make(map[string][]navRow, len(req.Funds))
	for _, fund := range req.Funds {
		var rows []navRow
		if err := h.DB.Select(&rows, `
			SELECT date, nav FROM nav_history
			WHERE code=? AND date>=? AND date<=? AND nav>0
			ORDER BY date ASC
		`, fund.Code, req.StartDate, req.EndDate); err != nil {
			fail(c, http.StatusInternalServerError, err.Error())
			return
		}
		series[fund.Code] = rows
		for _, row := range rows {
			unionDates[row.Date] = struct{}{}
		}
	}

	dates := make([]string, 0, len(unionDates))
	for date := range unionDates {
		dates = append(dates, date)
	}
	sort.Strings(dates)

	type state struct {
		Weight  float64
		BaseNav float64
		Index   int
		LastNav float64
	}
	states := make(map[string]*state, len(req.Funds))
	for _, fund := range req.Funds {
		rows := series[fund.Code]
		base := 0.0
		if len(rows) > 0 {
			base = rows[0].Nav
		}
		states[fund.Code] = &state{Weight: fund.Weight / totalWeight, BaseNav: base}
	}

	curve := make([]gin.H, 0, len(dates))
	maxDrawdown := 0.0
	peak := 0.0
	finalValue := req.InitialAmount

	for _, date := range dates {
		unitValue := 0.0
		for code, rows := range series {
			st := states[code]
			for st.Index < len(rows) && rows[st.Index].Date <= date {
				st.LastNav = rows[st.Index].Nav
				st.Index++
			}
			if st.BaseNav <= 0 || st.LastNav <= 0 {
				continue
			}
			unitValue += st.Weight * (st.LastNav / st.BaseNav)
		}
		value := req.InitialAmount * unitValue
		finalValue = value
		curve = append(curve, gin.H{"date": date, "value": value})
		if value > peak {
			peak = value
		}
		if peak > 0 {
			dd := (peak - value) / peak * 100
			if dd > maxDrawdown {
				maxDrawdown = dd
			}
		}
	}

	totalReturn := 0.0
	annualReturn := 0.0
	if req.InitialAmount > 0 {
		totalReturn = (finalValue - req.InitialAmount) / req.InitialAmount * 100
	}
	if req.StartDate != "" && req.EndDate != "" && req.InitialAmount > 0 && finalValue > 0 {
		start, startErr := time.Parse("2006-01-02", req.StartDate)
		end, endErr := time.Parse("2006-01-02", req.EndDate)
		if startErr == nil && endErr == nil {
			years := end.Sub(start).Hours() / 24 / 365
			if years > 0 {
				annualReturn = (math.Pow(finalValue/req.InitialAmount, 1/years) - 1) * 100
			}
		}
	}

	ok(c, gin.H{
		"fund_count":      len(req.Funds),
		"total_invested":  req.InitialAmount,
		"final_value":     finalValue,
		"total_return":    totalReturn,
		"annual_return":   annualReturn,
		"max_drawdown":    maxDrawdown,
		"curve":           curve,
		"portfolio_label": fmt.Sprintf("%d funds", len(req.Funds)),
	})
}
