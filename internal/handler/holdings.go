package handler

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/analysis"
	"github.com/pakoo/sanhu/internal/fetcher"
)

func (h *Handler) RefreshHoldings(c *gin.Context) {
	if !startTask(&holdingsRefreshTask) {
		c.JSON(http.StatusOK, gin.H{"status": "running"})
		return
	}

	go func() {
		codes := allFundCodes(h.DB)
		holdingsRefreshTask.mu.Lock()
		holdingsRefreshTask.Total = len(codes)
		holdingsRefreshTask.mu.Unlock()
		err := fetcher.FetchFundHoldingsAll(h.DB)
		finishTask(&holdingsRefreshTask, len(codes), err)
	}()

	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "后台刷新已启动"})
}

func (h *Handler) GetHoldingsRefreshStatus(c *gin.Context) {
	c.JSON(http.StatusOK, holdingsTaskSnapshot(&holdingsRefreshTask))
}

func (h *Handler) GetFundStocks(c *gin.Context) {
	code := c.Param("code")
	codeCol, err := chooseColumn(h.DB, "fund_holdings", "fund_code", "code")
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	var nullDate sql.NullString
	if err := h.DB.Get(&nullDate, fmt.Sprintf(`SELECT MAX(report_date) FROM fund_holdings WHERE %s=?`, codeCol), code); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	reportDate := nullDate.String

	type stockRow struct {
		StockCode  string  `db:"stock_code" json:"stock_code"`
		StockName  string  `db:"stock_name" json:"stock_name"`
		Weight     float64 `db:"weight" json:"weight"`
		Industry   string  `db:"industry" json:"industry"`
		ReportDate string  `db:"report_date" json:"report_date"`
	}
	var stocks []stockRow
	if reportDate != "" {
		query := fmt.Sprintf(`
			SELECT stock_code, stock_name, COALESCE(weight, 0) AS weight,
			       COALESCE(industry, '') AS industry, report_date
			FROM fund_holdings
			WHERE %s=? AND report_date=?
			ORDER BY report_date DESC, weight DESC
			LIMIT 20
		`, codeCol)
		if err := h.DB.Select(&stocks, query, code, reportDate); err != nil {
			fail(c, http.StatusInternalServerError, err.Error())
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": code, "stocks": stocks, "report_date": reportDate})
}

func (h *Handler) GetFundIndustry(c *gin.Context) {
	code := c.Param("code")
	codeCol, err := chooseColumn(h.DB, "fund_industry", "fund_code", "code")
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	industryCol, err := chooseColumn(h.DB, "fund_industry", "industry_name", "industry")
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	type row struct {
		Industry   string  `db:"industry" json:"industry"`
		Weight     float64 `db:"weight" json:"weight"`
		ReportDate string  `db:"report_date" json:"report_date"`
	}
	var rows []row
	query := fmt.Sprintf(`
		SELECT %s AS industry, COALESCE(weight, 0) AS weight, report_date
		FROM fund_industry
		WHERE %s=?
		ORDER BY report_date DESC, weight DESC
		LIMIT 10
	`, industryCol, codeCol)
	if err := h.DB.Select(&rows, query, code); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (h *Handler) GetHoldingsOverlap(c *gin.Context) {
	result, err := analysis.GetHoldingsOverlap(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) GetHoldingsIndustryTotal(c *gin.Context) {
	result, err := analysis.GetIndustryBreakdown(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) GetHoldingsChanges(c *gin.Context) {
	result, err := analysis.GetHoldingsChanges(h.DB, c.Param("code"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) UpdateHolding(c *gin.Context) {
	code := c.Param("code")
	var req struct {
		CurrentValue float64 `json:"current_value"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	cols, err := tableColumns(h.DB, "holdings")
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	field := "cost_amount"
	if !hasColumn(cols, field) {
		field = "cost_basis"
	}
	if _, err := h.DB.Exec(fmt.Sprintf(`UPDATE holdings SET %s=? WHERE code=?`, field), req.CurrentValue, code); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "code": code})
}

func (h *Handler) DeleteHolding(c *gin.Context) {
	code := c.Param("code")
	var cnt int
	if err := h.DB.Get(&cnt, `SELECT COUNT(*) FROM holdings WHERE code=?`, code); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if cnt == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if _, err := h.DB.Exec(`DELETE FROM holdings WHERE code=?`, code); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "code": code})
}
