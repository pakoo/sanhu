package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/risk"
)

func (h *Handler) GetRiskAnalysis(c *gin.Context) {
	holdings, total, err := loadPortfolioHoldings(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	result, err := risk.RiskAnalysisAll(h.DB, toRiskInputs(holdings), total)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) GetCorrelation(c *gin.Context) {
	codes := allFundCodes(h.DB)
	if len(codes) == 0 {
		c.JSON(http.StatusOK, gin.H{"codes": []string{}, "correlation_matrix": [][]float64{}})
		return
	}

	series := make(map[string]map[string]float64, len(codes))
	for _, code := range codes {
		data, err := dailyReturnSeries(h.DB, code, 90)
		if err != nil {
			fail(c, http.StatusInternalServerError, err.Error())
			return
		}
		series[code] = data
	}

	matrix := make([][]float64, len(codes))
	for i, codeI := range codes {
		matrix[i] = make([]float64, len(codes))
		for j, codeJ := range codes {
			if i == j {
				matrix[i][j] = 1
				continue
			}
			var xs, ys []float64
			for date, x := range series[codeI] {
				if y, ok := series[codeJ][date]; ok {
					xs = append(xs, x)
					ys = append(ys, y)
				}
			}
			matrix[i][j] = pearson(xs, ys)
		}
	}

	c.JSON(http.StatusOK, gin.H{"codes": codes, "correlation_matrix": matrix})
}
