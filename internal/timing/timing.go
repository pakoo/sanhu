package timing

import (
	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/fetcher"
	"github.com/pakoo/sanhu/internal/scoring"
)

// Signal 三层择时信号的综合结果
type Signal string

const (
	SignalFavorable   Signal = "favorable"
	SignalNeutral     Signal = "neutral"
	SignalUnfavorable Signal = "unfavorable"
)

// LayerSignal 单层信号
type LayerSignal struct {
	Layer  string `json:"layer"`   // market / quality / technical
	Signal Signal `json:"signal"`  // favorable / neutral / unfavorable
	Reason string `json:"reason"`
	Score  int    `json:"score"`   // -1 / 0 / 1
}

// TimingResult 完整择时结果
type TimingResult struct {
	Signal   Signal        `json:"signal"`
	Layers   []LayerSignal `json:"layers"`
	Summary  string        `json:"summary"`
}

// GetTimingSignal 三层综合择时：市场估值 + 基金质量 + 技术面偏离
func GetTimingSignal(db *sqlx.DB, code string) (*TimingResult, error) {
	layers := make([]LayerSignal, 0, 3)
	total := 0

	// ── 层 1：市场估值（HS300 PE 百分位）──────────────────────────────────────
	pePct, err := fetcher.CalcPEPercentile(db, "000300", 5)
	marketLayer := LayerSignal{Layer: "market"}
	if err != nil || pePct == 0 {
		marketLayer.Signal = SignalNeutral
		marketLayer.Reason = "估值数据不足，视为合理"
		marketLayer.Score = 0
	} else {
		switch {
		case pePct < 30:
			marketLayer.Signal = SignalFavorable
			marketLayer.Reason = "HS300 PE 百分位 < 30%，市场低估"
			marketLayer.Score = 1
		case pePct > 70:
			marketLayer.Signal = SignalUnfavorable
			marketLayer.Reason = "HS300 PE 百分位 > 70%，市场高估"
			marketLayer.Score = -1
		default:
			marketLayer.Signal = SignalNeutral
			marketLayer.Reason = "HS300 PE 百分位合理区间"
			marketLayer.Score = 0
		}
	}
	layers = append(layers, marketLayer)
	total += marketLayer.Score

	// ── 层 2：基金质量（评分）────────────────────────────────────────────────
	qualityLayer := LayerSignal{Layer: "quality"}
	s, err := scoring.GetFundScore(db, code)
	if err != nil || s == nil {
		qualityLayer.Signal = SignalNeutral
		qualityLayer.Reason = "评分数据不足"
		qualityLayer.Score = 0
	} else {
		switch {
		case s.TotalScore >= 75:
			qualityLayer.Signal = SignalFavorable
			qualityLayer.Reason = "基金综合评分 ≥ 75，质量较优"
			qualityLayer.Score = 1
		case s.TotalScore < 50:
			qualityLayer.Signal = SignalUnfavorable
			qualityLayer.Reason = "基金综合评分 < 50，质量偏弱"
			qualityLayer.Score = -1
		default:
			qualityLayer.Signal = SignalNeutral
			qualityLayer.Reason = "基金综合评分中等"
			qualityLayer.Score = 0
		}
	}
	layers = append(layers, qualityLayer)
	total += qualityLayer.Score

	// ── 层 3：技术面（净值 vs 20 日均线偏离）─────────────────────────────────
	techLayer := LayerSignal{Layer: "technical"}
	deviation, ok := navDeviation(db, code, 20)
	if !ok {
		techLayer.Signal = SignalNeutral
		techLayer.Reason = "净值数据不足"
		techLayer.Score = 0
	} else {
		switch {
		case deviation < -0.05:
			techLayer.Signal = SignalFavorable
			techLayer.Reason = "净值低于 20 日均线 5% 以上，存在超跌机会"
			techLayer.Score = 1
		case deviation > 0.05:
			techLayer.Signal = SignalUnfavorable
			techLayer.Reason = "净值高于 20 日均线 5% 以上，短期偏高"
			techLayer.Score = -1
		default:
			techLayer.Signal = SignalNeutral
			techLayer.Reason = "净值在均线附近，技术面中性"
			techLayer.Score = 0
		}
	}
	layers = append(layers, techLayer)
	total += techLayer.Score

	// ── 合成 ──────────────────────────────────────────────────────────────────
	var overallSignal Signal
	var summary string
	switch {
	case total >= 2:
		overallSignal = SignalFavorable
		summary = "三层信号多数偏多，综合建议积极关注"
	case total <= -2:
		overallSignal = SignalUnfavorable
		summary = "三层信号多数偏空，建议谨慎或暂缓"
	default:
		overallSignal = SignalNeutral
		summary = "信号分歧，建议中性观望"
	}

	return &TimingResult{
		Signal:  overallSignal,
		Layers:  layers,
		Summary: summary,
	}, nil
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
		ORDER BY date DESC LIMIT ?`, code, days); err != nil || len(rows) < days/2 {
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
