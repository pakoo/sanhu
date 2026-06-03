package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/position"
	"github.com/pakoo/sanhu/internal/timing"
)

func (h *Handler) GetTiming(c *gin.Context) {
	result, err := timing.GetTimingSignal(h.DB, c.Param("code"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, result)
}

func (h *Handler) GetPositionSizing(c *gin.Context) {
	settings, err := getSettingsCompat(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	riskLevel := settings["risk_level"]
	if riskLevel == "" {
		riskLevel = "moderate"
	}
	suggestion, err := position.SuggestPosition(h.DB, c.Param("code"), riskLevel)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, suggestion)
}
