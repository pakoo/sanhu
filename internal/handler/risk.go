package handler

import (
	"math"
	"net/http"
	"sort"

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
	// 与 Python 基准对齐：仅持仓基金；每只取最近 252 行 daily_return 列；
	// 全局 dropna（保留所有基金都有数据的日期）后计算 Pearson；矩阵四舍五入 3 位。
	var codes []string
	_ = h.DB.Select(&codes, `SELECT code FROM holdings`)
	if len(codes) == 0 {
		c.JSON(http.StatusOK, gin.H{"codes": []string{}, "names": []string{}, "matrix": [][]float64{}, "correlation_matrix": [][]float64{}})
		return
	}

	series := make(map[string]map[string]float64, len(codes))
	nameOf := make(map[string]string, len(codes))
	withData := make([]string, 0, len(codes))
	for _, code := range codes {
		type row struct {
			Date string  `db:"date"`
			Ret  float64 `db:"ret"`
			Name string  `db:"name"`
		}
		var rows []row
		if err := h.DB.Select(&rows, `
			SELECT nh.date, nh.daily_return AS ret, f.name
			FROM nav_history nh JOIN funds f ON nh.code=f.code
			WHERE nh.code=? AND nh.daily_return IS NOT NULL
			ORDER BY nh.date DESC LIMIT 252`, code); err != nil {
			fail(c, http.StatusInternalServerError, err.Error())
			return
		}
		if len(rows) == 0 {
			continue
		}
		nameOf[code] = rows[0].Name
		m := make(map[string]float64, len(rows))
		for _, r := range rows {
			m[r.Date] = r.Ret
		}
		series[code] = m
		withData = append(withData, code)
	}

	names := make([]string, len(withData))
	for i, code := range withData {
		names[i] = nameOf[code]
	}

	if len(withData) < 2 {
		c.JSON(http.StatusOK, gin.H{"codes": withData, "names": names, "matrix": [][]float64{}, "correlation_matrix": [][]float64{}})
		return
	}

	// 全局 dropna：仅保留所有基金都有数据的日期
	counter := map[string]int{}
	for _, code := range withData {
		for d := range series[code] {
			counter[d]++
		}
	}
	dates := make([]string, 0)
	for d, cnt := range counter {
		if cnt == len(withData) {
			dates = append(dates, d)
		}
	}
	if len(dates) < 10 {
		c.JSON(http.StatusOK, gin.H{"codes": withData, "names": names, "matrix": [][]float64{}, "correlation_matrix": [][]float64{}})
		return
	}
	sort.Strings(dates)

	vecs := make(map[string][]float64, len(withData))
	for _, code := range withData {
		v := make([]float64, len(dates))
		for i, d := range dates {
			v[i] = series[code][d]
		}
		vecs[code] = v
	}

	matrix := make([][]float64, len(withData))
	for i, ci := range withData {
		matrix[i] = make([]float64, len(withData))
		for j, cj := range withData {
			if i == j {
				matrix[i][j] = 1
				continue
			}
			matrix[i][j] = math.Round(pearson(vecs[ci], vecs[cj])*1000) / 1000
		}
	}

	c.JSON(http.StatusOK, gin.H{"codes": withData, "names": names, "matrix": matrix, "correlation_matrix": matrix})
}
