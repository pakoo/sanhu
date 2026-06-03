package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *Handler) GetSettings(c *gin.Context) {
	values, err := getSettingsCompat(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{
		"risk_level":       values["risk_level"],
		"ocrspace_api_key": values["ocrspace_api_key"],
	})
}

func (h *Handler) PatchSettings(c *gin.Context) {
	var req struct {
		RiskLevel      *string `json:"risk_level"`
		OcrspaceAPIKey *string `json:"ocrspace_api_key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	values, err := getSettingsCompat(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if req.RiskLevel != nil {
		values["risk_level"] = *req.RiskLevel
	}
	if req.OcrspaceAPIKey != nil {
		values["ocrspace_api_key"] = *req.OcrspaceAPIKey
	}
	if values["risk_level"] == "" {
		values["risk_level"] = "moderate"
	}

	if err := saveSettingsCompat(h.DB, values); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{
		"risk_level":       values["risk_level"],
		"ocrspace_api_key": values["ocrspace_api_key"],
	})
}
