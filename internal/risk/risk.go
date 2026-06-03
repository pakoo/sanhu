package risk

import (
	"fmt"
	"math"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/fetcher"
)

// ─── 数据类型 ──────────────────────────────────────────────────────────────────

type Alert struct {
	Level   string `json:"level"`   // high / medium / low
	Message string `json:"message"`
}

type DrawdownResult struct {
	MaxDrawdown float64 `json:"max_drawdown"` // 百分比（正数）
	PeakDate    string  `json:"peak_date"`
	TroughDate  string  `json:"trough_date"`
}

type RiskAnalysis struct {
	Score           float64          `json:"score"` // 0-100，越高越安全
	Alerts          []Alert          `json:"alerts"`
	ConcentrationOK bool             `json:"concentration_ok"`
	VolatilityAnn   float64          `json:"volatility_ann"`   // 年化波动率 %
	MaxDrawdown     float64          `json:"max_drawdown"`
	ValuationSignal string           `json:"valuation_signal"` // favorable/neutral/unfavorable
}

type HoldingInput struct {
	Code     string  `json:"code"`
	Value    float64 `json:"value"`
	Category string  `json:"category"`
}

// ─── 公开接口 ──────────────────────────────────────────────────────────────────

// RiskAnalysisAll 综合风险分析（集中度 + 估值 + 回撤 + 波动）
func RiskAnalysisAll(db *sqlx.DB, holdings []HoldingInput, totalValue float64) (*RiskAnalysis, error) {
	if totalValue <= 0 {
		for _, h := range holdings {
			totalValue += h.Value
		}
	}

	alerts := make([]Alert, 0)
	score := 100.0

	// ── 集中度检查 ────────────────────────────────────────────────────────────
	catMap := map[string]float64{}
	for _, h := range holdings {
		pct := h.Value / totalValue * 100
		// 单只基金
		if pct > 40 {
			alerts = append(alerts, Alert{
				Level:   "high",
				Message: fmt.Sprintf("%s 占比 %.1f%%，单基金集中度过高", h.Code, pct),
			})
			score -= 15
		} else if pct > 25 {
			alerts = append(alerts, Alert{
				Level:   "medium",
				Message: fmt.Sprintf("%s 占比 %.1f%%，建议关注集中风险", h.Code, pct),
			})
			score -= 5
		}
		catMap[h.Category] += h.Value
	}

	// 类别集中
	for cat, val := range catMap {
		pct := val / totalValue * 100
		var threshold float64
		var label string
		switch cat {
		case "bond":
			threshold, label = 80, "债券类"
		case "equity":
			threshold, label = 60, "权益类"
		}
		if threshold > 0 && pct > threshold {
			alerts = append(alerts, Alert{
				Level:   "medium",
				Message: fmt.Sprintf("%s占比 %.1f%%，超过%.0f%%警戒线", label, pct, threshold),
			})
			score -= 5
		}
	}

	// ── 估值信号 ─────────────────────────────────────────────────────────────
	pePct, err := fetcher.CalcPEPercentile(db, "000300", 5)
	valuationSignal := "neutral"
	if err == nil {
		switch {
		case pePct > 80:
			valuationSignal = "unfavorable"
			alerts = append(alerts, Alert{
				Level:   "medium",
				Message: fmt.Sprintf("HS300 PE 百分位 %.0f%%，当前市场估值偏高", pePct),
			})
			score -= 5
		case pePct < 20:
			valuationSignal = "favorable"
		}
	}

	// ── 回撤检查（持仓亏损率）────────────────────────────────────────────────
	totalDrawdown := 0.0
	for _, h := range holdings {
		dd, err := calcFundDrawdown(db, h.Code, 365)
		if err == nil && dd.MaxDrawdown > 30 {
			alerts = append(alerts, Alert{
				Level:   "medium",
				Message: fmt.Sprintf("%s 年内最大回撤 %.1f%%", h.Code, dd.MaxDrawdown),
			})
			score -= 5
		}
		if err == nil {
			totalDrawdown = math.Max(totalDrawdown, dd.MaxDrawdown)
		}
	}

	// ── 组合波动率 ────────────────────────────────────────────────────────────
	portVol := portfolioVolatility(db, holdings, 90)

	if score < 0 {
		score = 0
	}

	return &RiskAnalysis{
		Score:           math.Round(score*100) / 100,
		Alerts:          alerts,
		ConcentrationOK: len(alerts) == 0,
		VolatilityAnn:   portVol,
		MaxDrawdown:     totalDrawdown,
		ValuationSignal: valuationSignal,
	}, nil
}

// MaxDrawdown 计算单只基金回撤
func calcFundDrawdown(db *sqlx.DB, code string, days int) (*DrawdownResult, error) {
	cutoff := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	type row struct {
		Date string  `db:"date"`
		Nav  float64 `db:"nav"`
	}
	var rows []row
	if err := db.Select(&rows, `
		SELECT date, nav FROM nav_history
		WHERE code=? AND date>=? AND nav>0 ORDER BY date ASC`, code, cutoff); err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return &DrawdownResult{}, nil
	}

	peak := rows[0].Nav
	peakDate := rows[0].Date
	maxDD := 0.0
	troughDate := rows[0].Date

	for _, r := range rows {
		if r.Nav > peak {
			peak = r.Nav
			peakDate = r.Date
		}
		if peak > 0 {
			dd := (peak - r.Nav) / peak * 100
			if dd > maxDD {
				maxDD = dd
				troughDate = r.Date
			}
		}
	}
	return &DrawdownResult{
		MaxDrawdown: math.Round(maxDD*100) / 100,
		PeakDate:    peakDate,
		TroughDate:  troughDate,
	}, nil
}

// portfolioVolatility 组合年化波动率（加权简化版，不含相关性）
func portfolioVolatility(db *sqlx.DB, holdings []HoldingInput, days int) float64 {
	cutoff := time.Now().AddDate(0, 0, -days).Format("2006-01-02")

	totalValue := 0.0
	for _, h := range holdings {
		totalValue += h.Value
	}
	if totalValue == 0 {
		return 0
	}

	portVol := 0.0
	for _, h := range holdings {
		weight := h.Value / totalValue
		var navs []float64
		type row struct {
			Nav float64 `db:"nav"`
		}
		var rows []row
		if err := db.Select(&rows, `
			SELECT nav FROM nav_history WHERE code=? AND date>=? AND nav>0 ORDER BY date ASC`,
			h.Code, cutoff); err != nil || len(rows) < 10 {
			continue
		}
		for _, r := range rows {
			navs = append(navs, r.Nav)
		}
		rets := dailyReturns(navs)
		if len(rets) == 0 {
			continue
		}
		vol := stdDev(rets) * math.Sqrt(250) * 100
		portVol += weight * vol
	}
	return math.Round(portVol*100) / 100
}

// ─── 内部辅助 ──────────────────────────────────────────────────────────────────

func dailyReturns(navs []float64) []float64 {
	if len(navs) < 2 {
		return nil
	}
	ret := make([]float64, len(navs)-1)
	for i := 1; i < len(navs); i++ {
		if navs[i-1] > 0 {
			ret[i-1] = (navs[i] - navs[i-1]) / navs[i-1]
		}
	}
	return ret
}

func stdDev(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(len(vals))
	varSum := 0.0
	for _, v := range vals {
		d := v - mean
		varSum += d * d
	}
	return math.Sqrt(varSum / float64(len(vals)))
}
