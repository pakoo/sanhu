package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/portfolio"
)

func (h *Handler) GetPortfolio(c *gin.Context) {
	summary, err := portfolio.GetPortfolioSummary(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, summary)
}

func (h *Handler) GetPortfolioDetail(c *gin.Context) {
	detail, err := portfolio.GetHoldingDetail(h.DB, c.Param("code"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *Handler) CreateTransaction(c *gin.Context) {
	var req struct {
		Code   string  `json:"code"`
		Type   string  `json:"type"`
		Date   string  `json:"date"`
		Amount float64 `json:"amount"`
		Nav    float64 `json:"nav"`
		Fee    float64 `json:"fee"`
		Notes  string  `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := portfolio.RecordTransaction(h.DB, req.Code, req.Type, req.Date, req.Amount, req.Nav, req.Fee, req.Notes); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
