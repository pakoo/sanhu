package timing

import (
	"fmt"
	"math"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/scoring"
)

// Signal 兼容旧调用方的类型别名
type Signal = string

const (
	SignalFavorable   Signal = "favorable"
	SignalNeutral     Signal = "neutral"
	SignalUnfavorable Signal = "unfavorable"
)

// MarketSignal / QualitySignal / PriceSignal 对齐 Python 三层信号明细
type MarketSignal struct {
	Level string   `json:"level"`
	PEPct *float64 `json:"pe_pct"`
}
type QualitySignal struct {
	Level      string   `json:"level"`
	TotalScore *float64 `json:"total_score"`
}
type PriceSignal struct {
	Level        string   `json:"level"`
	DeviationPct *float64 `json:"deviation_pct"`
}

// TimingResult 对齐 Python get_timing_signal 输出
type TimingResult struct {
	Code           string        `json:"code"`
	Level          string        `json:"level"`
	CompositeScore int           `json:"composite_score"`
	Fragments      []string      `json:"fragments"`
	MarketSignal   MarketSignal  `json:"market_signal"`
	QualitySignal  QualitySignal `json:"quality_signal"`
	PriceSignal    PriceSignal   `json:"price_signal"`
	Signal         Signal        `json:"-"` // 兼容字段（= Level）
}

// GetTimingSignal 三层综合择时：市场估值(HS300 PE) + 基金质量(评分) + 技术面(20日均线)
func GetTimingSignal(db *sqlx.DB, code string) (*TimingResult, error) {
	levelScore := map[string]int{"favorable": 1, "neutral": 0, "unfavorable": -1}

	// ── 层 1：市场估值（HS300 PE 百分位，近10年）─────────────────────────────
	marketLevel := "neutral"
	marketNote := "市场估值数据暂缺"
	var pePct *float64
	if pct, ok := hs300Percentile(db); ok {
		pePct = &pct
		switch {
		case pct < 30:
			marketLevel, marketNote = "favorable", fmt.Sprintf("HS300 PE %.0f%% 低估区间", pct)
		case pct > 70:
			marketLevel, marketNote = "unfavorable", fmt.Sprintf("HS300 PE %.0f%% 高估区间", pct)
		default:
			marketLevel, marketNote = "neutral", fmt.Sprintf("HS300 PE %.0f%% 合理区间", pct)
		}
	}

	// ── 层 2：基金质量（评分）────────────────────────────────────────────────
	qualityLevel := "neutral"
	qualityNote := "暂无评分数据"
	var totalScore *float64
	if s, err := scoring.GetFundScore(db, code); err == nil && s != nil {
		ts := s.TotalScore
		totalScore = &ts
		switch {
		case ts >= 75:
			qualityLevel, qualityNote = "favorable", fmt.Sprintf("综合分 %.0f 较高", ts)
		case ts < 50:
			qualityLevel, qualityNote = "unfavorable", fmt.Sprintf("综合分 %.0f 偏低", ts)
		default:
			qualityLevel, qualityNote = "neutral", fmt.Sprintf("综合分 %.0f", ts)
		}
	}

	// ── 层 3：技术面（净值 vs 20 日均线偏离）─────────────────────────────────
	priceLevel := "neutral"
	priceNote := "历史净值数据不足"
	var deviationPct *float64
	if dev, ok := navDeviation(db, code, 20); ok {
		d := dev * 100
		deviationPct = &d
		switch {
		case dev <= -0.05:
			priceLevel, priceNote = "favorable", fmt.Sprintf("现价低于20日均线 %.1f%%", math.Abs(dev)*100)
		case dev >= 0.05:
			priceLevel, priceNote = "unfavorable", fmt.Sprintf("现价高于20日均线 %.1f%%", dev*100)
		default:
			priceLevel, priceNote = "neutral", fmt.Sprintf("现价接近20日均线 (%+.1f%%)", dev*100)
		}
	}

	composite := levelScore[marketLevel] + levelScore[qualityLevel] + levelScore[priceLevel]
	finalLevel := "neutral"
	if composite >= 2 {
		finalLevel = "favorable"
	} else if composite <= -2 {
		finalLevel = "unfavorable"
	}

	return &TimingResult{
		Code:           code,
		Level:          finalLevel,
		CompositeScore: composite,
		Fragments:      []string{marketNote, qualityNote, priceNote},
		MarketSignal:   MarketSignal{Level: marketLevel, PEPct: pePct},
		QualitySignal:  QualitySignal{Level: qualityLevel, TotalScore: totalScore},
		PriceSignal:    PriceSignal{Level: priceLevel, DeviationPct: deviationPct},
		Signal:         finalLevel,
	}, nil
}

// hs300Percentile 近10年 HS300 PE 百分位
func hs300Percentile(db *sqlx.DB) (float64, bool) {
	var pes []float64
	if err := db.Select(&pes, `
		SELECT pe FROM index_pe_history
		WHERE index_code='hs300' AND pe IS NOT NULL
		ORDER BY date DESC LIMIT ?`, 10*252); err != nil || len(pes) == 0 {
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

// ─── 内部辅助 ──────────────────────────────────────────────────────────────────

// navDeviation 计算最新净值与 n 日均线的偏离率（净值/均线 - 1）
func navDeviation(db *sqlx.DB, code string, days int) (float64, bool) {
	type row struct {
		Nav float64 `db:"nav"`
	}
	var rows []row
	if err := db.Select(&rows, `
		SELECT nav FROM nav_history
		WHERE code=? AND nav>0
		ORDER BY date DESC LIMIT ?`, code, days); err != nil || len(rows) < days {
		return 0, false
	}
	if len(rows) == 0 {
		return 0, false
	}
	latest := rows[0].Nav
	sum := 0.0
	for _, r := range rows {
		sum += r.Nav
	}
	ma := sum / float64(len(rows))
	if ma == 0 {
		return 0, false
	}
	return (latest - ma) / ma, true
}
