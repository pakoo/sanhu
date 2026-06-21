package risk

import (
	"fmt"
	"math"

	"github.com/jmoiron/sqlx"
)

// ─── 数据类型 ──────────────────────────────────────────────────────────────────

type Alert struct {
	Level    string `json:"level"` // high / medium / low
	Category string `json:"category"`
	Message  string `json:"message"`
	Detail   string `json:"detail"`
}

type DrawdownResult struct {
	MaxDrawdown float64 `json:"max_drawdown"` // 百分比（负数，与 Python 一致）
	PeakDate    string  `json:"peak_date"`
	TroughDate  string  `json:"trough_date"`
}

// VolatilityResult 组合波动率 + 各基金年化波动率
type VolatilityResult struct {
	PortfolioVol float64            `json:"portfolio_vol"`
	FundVols     map[string]float64 `json:"fund_vols"`
}

// RiskAnalysis 与 Python risk_analysis 输出对齐：{alerts, drawdowns, volatility, score}
type RiskAnalysis struct {
	Alerts     []Alert                   `json:"alerts"`
	Drawdowns  map[string]DrawdownResult `json:"drawdowns"`
	Volatility VolatilityResult          `json:"volatility"`
	Score      float64                   `json:"score"`
}

type HoldingInput struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	Value      float64 `json:"value"`
	Category   string  `json:"category"`
	ProfitRate float64 `json:"profit_rate"`
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

	// ── 集中度检查（单只 + 类别），与 Python concentration_risk 对齐 ──────────
	catMap := map[string]float64{}
	for _, h := range holdings {
		pct := h.Value / totalValue * 100
		if pct > 40 {
			alerts = append(alerts, Alert{Level: "high", Category: "concentration",
				Message: fmt.Sprintf("%s占比%.1f%%，过度集中", h.Name, pct),
				Detail:  "单只基金占比超过40%，建议分散至30%以下"})
		} else if pct > 25 {
			alerts = append(alerts, Alert{Level: "medium", Category: "concentration",
				Message: fmt.Sprintf("%s占比%.1f%%，较为集中", h.Name, pct),
				Detail:  "建议关注该基金的仓位管理"})
		}
		catMap[h.Category] += h.Value
	}
	for cat, val := range catMap {
		pct := val / totalValue * 100
		if cat == "bond" && pct > 80 {
			alerts = append(alerts, Alert{Level: "medium", Category: "allocation",
				Message: fmt.Sprintf("债券配置占%.1f%%，配置偏保守", pct),
				Detail:  "当前配置以债券为主，可能错过权益市场的增长机会。建议逐步增加权益配置。"})
		} else if (cat == "equity" || cat == "mixed") && pct > 60 {
			alerts = append(alerts, Alert{Level: "medium", Category: "allocation",
				Message: fmt.Sprintf("权益类配置占%.1f%%，注意风险", pct),
				Detail:  "权益类占比较高，市场下跌时回撤可能较大"})
		}
	}

	// ── 逐基金回撤 + 回撤告警 ────────────────────────────────────────────────
	drawdowns := map[string]DrawdownResult{}
	for _, h := range holdings {
		dd, err := calcFundDrawdown(db, h.Code)
		if err != nil || dd == nil {
			dd = &DrawdownResult{}
		}
		drawdowns[h.Code] = *dd
		if dd.MaxDrawdown < -20 {
			alerts = append(alerts, Alert{Level: "high", Category: "drawdown",
				Message: fmt.Sprintf("%s近一年最大回撤%.1f%%", h.Name, dd.MaxDrawdown),
				Detail:  fmt.Sprintf("回撤较大，注意控制仓位。回撤区间: %s ~ %s", dd.PeakDate, dd.TroughDate)})
		} else if dd.MaxDrawdown < -10 {
			alerts = append(alerts, Alert{Level: "medium", Category: "drawdown",
				Message: fmt.Sprintf("%s近一年最大回撤%.1f%%", h.Name, dd.MaxDrawdown),
				Detail:  fmt.Sprintf("回撤区间: %s ~ %s", dd.PeakDate, dd.TroughDate)})
		}
	}

	// ── 组合波动率（含各基金）────────────────────────────────────────────────
	vol := portfolioVolatility(db, holdings, 252)

	// ── 亏损告警 ─────────────────────────────────────────────────────────────
	for _, h := range holdings {
		if h.ProfitRate < -5 {
			alerts = append(alerts, Alert{Level: "medium", Category: "loss",
				Message: fmt.Sprintf("%s当前亏损%.1f%%", h.Name, h.ProfitRate),
				Detail:  "建议评估是否需要止损或定投摊低成本"})
		}
	}

	// ── 估值告警（hs300 / csi500 PE 百分位）─────────────────────────────────
	for _, idx := range []struct{ code, label string }{{"hs300", "沪深300"}, {"csi500", "中证500"}} {
		pct, ok := indexPEPercentile(db, idx.code)
		if !ok {
			continue
		}
		if pct < 30 {
			alerts = append(alerts, Alert{Level: "low", Category: "valuation",
				Message: fmt.Sprintf("%s PE处于历史低估区间（%.0f%%分位）", idx.label, pct),
				Detail:  "市场估值偏低，可适当提高权益配置比例，分批加仓"})
		} else if pct > 70 {
			alerts = append(alerts, Alert{Level: "medium", Category: "valuation",
				Message: fmt.Sprintf("%s PE处于历史高估区间（%.0f%%分位）", idx.label, pct),
				Detail:  "市场估值偏高，建议谨慎加仓权益类资产，优先持有债券防守"})
		}
	}

	// ── 风险评分（0-100，越高越安全）─────────────────────────────────────────
	score := 80.0
	high, medium := 0, 0
	for _, a := range alerts {
		switch a.Level {
		case "high":
			high++
		case "medium":
			medium++
		}
	}
	score -= float64(high)*15 + float64(medium)*5
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	return &RiskAnalysis{
		Alerts:     alerts,
		Drawdowns:  drawdowns,
		Volatility: vol,
		Score:      score,
	}, nil
}

// indexPEPercentile 复刻 Python get_pe_percentile 的百分位（近10年）。
func indexPEPercentile(db *sqlx.DB, indexCode string) (float64, bool) {
	var pes []float64
	if err := db.Select(&pes, `
		SELECT pe FROM index_pe_history
		WHERE index_code=? AND pe IS NOT NULL
		ORDER BY date DESC LIMIT ?`, indexCode, 10*252); err != nil || len(pes) == 0 {
		return 0, false
	}
	cur := pes[0]
	below := 0
	for _, v := range pes {
		if v <= cur {
			below++
		}
	}
	return math.Round(float64(below)/float64(len(pes))*100*10) / 10, true
}

// calcFundDrawdown 计算单只基金近一年(最近365行)最大回撤，与 Python max_drawdown 对齐：
// drawdown=(nav-累计峰值)/峰值*100(≤0)，取最负点为 trough，trough 前的最高点为 peak。
func calcFundDrawdown(db *sqlx.DB, code string) (*DrawdownResult, error) {
	type row struct {
		Date string  `db:"date"`
		Nav  float64 `db:"nav"`
	}
	var desc []row
	if err := db.Select(&desc, `
		SELECT date, nav FROM nav_history
		WHERE code=? AND nav>0 ORDER BY date DESC LIMIT 365`, code); err != nil {
		return nil, err
	}
	if len(desc) < 2 {
		return &DrawdownResult{}, nil
	}
	// 反转为时间正序
	rows := make([]row, len(desc))
	for i, r := range desc {
		rows[len(desc)-1-i] = r
	}

	runningMax := rows[0].Nav
	runningMaxIdx := 0
	maxDD := 0.0
	troughIdx := 0
	peakIdx := 0
	for i, r := range rows {
		if r.Nav > runningMax {
			runningMax = r.Nav
			runningMaxIdx = i
		}
		dd := (r.Nav - runningMax) / runningMax * 100
		if dd < maxDD {
			maxDD = dd
			troughIdx = i
			peakIdx = runningMaxIdx
		}
	}
	return &DrawdownResult{
		MaxDrawdown: math.Round(maxDD*100) / 100,
		PeakDate:    rows[peakIdx].Date,
		TroughDate:  rows[troughIdx].Date,
	}, nil
}

// portfolioVolatility 组合年化波动率（加权简化版，不含相关性）
// 与 Python 基准对齐：每只取最近 days 行 daily_return 列（百分比），
// 总体 std × √252，单只先 round 2 再加权，最后 round 2。
func portfolioVolatility(db *sqlx.DB, holdings []HoldingInput, days int) VolatilityResult {
	fundVols := map[string]float64{}
	totalValue := 0.0
	for _, h := range holdings {
		totalValue += h.Value
	}

	portVol := 0.0
	for _, h := range holdings {
		var weight float64
		if totalValue > 0 {
			weight = h.Value / totalValue
		}
		var rets []float64
		if err := db.Select(&rets, `
			SELECT daily_return FROM nav_history
			WHERE code=? AND daily_return IS NOT NULL
			ORDER BY date DESC LIMIT ?`, h.Code, days); err != nil || len(rets) <= 5 {
			fundVols[h.Code] = 0
			continue
		}
		fundVol := math.Round(stdDev(rets)*math.Sqrt(252)*100) / 100
		fundVols[h.Code] = fundVol
		portVol += weight * fundVol
	}
	return VolatilityResult{
		PortfolioVol: math.Round(portVol*100) / 100,
		FundVols:     fundVols,
	}
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
