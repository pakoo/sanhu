package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/fetcher"
	"github.com/pakoo/sanhu/internal/watchlist"
)

func (h *Handler) GetWatchlist(c *gin.Context) {
	result, err := watchlist.GetWatchlist(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) AddWatchlist(c *gin.Context) {
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
	go func() { _ = fetcher.FetchFundInfo(h.DB, req.Code) }()
	c.JSON(http.StatusOK, gin.H{"ok": true, "code": req.Code})
}

func (h *Handler) RemoveWatchlist(c *gin.Context) {
	code := c.Param("code")
	if err := watchlist.RemoveFromWatchlist(h.DB, code); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "code": code})
}
