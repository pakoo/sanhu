package position

import (
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/jmoiron/sqlx"
)

type StepDetail struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Value       float64 `json:"value"`
}

type PositionSuggestion struct {
	SuggestedAmount float64      `json:"suggested_amount"`
	RangeLow        float64      `json:"range_low"`
	RangeHigh       float64      `json:"range_high"`
	Steps           []StepDetail `json:"steps"`
	Warnings        []string     `json:"warnings"`
}

func SuggestPosition(db *sqlx.DB, code, riskLevel string) (*PositionSuggestion, error) {
	totalAssets, currentPct, err := currentExposure(db, code)
	if err != nil {
		return nil, err
	}

	warnings := []string{}
	if totalAssets == 0 {
		totalAssets = 10000
		warnings = append(warnings, "portfolio is empty, seeded total assets with 10000")
	}

	targetPct, fromSetting, err := targetWeight(db, code)
	if err != nil {
		return nil, err
	}
	if !fromSetting {
		warnings = append(warnings, "target allocation not configured for this fund, defaulted to 10%")
	}

	step1 := math.Max(0, targetPct-currentPct) / 100 * totalAssets
	batchFactor := batchMultiplier(riskLevel)
	step2 := step1 * batchFactor

	signal, err := latestTimingSignal(db, code)
	if err != nil {
		return nil, err
	}
	timingFactor := timingDiscount(signal)
	if signal == "unfavorable" {
		warnings = append(warnings, "latest timing signal is unfavorable")
	}
	step3 := step2 * timingFactor

	step4 := step3 * 1.0

	maxBound := totalAssets * 0.30
	if maxBound < 500 {
		maxBound = 500
	}
	finalAmount := clamp(step4, 500, maxBound)
	if step1 == 0 {
		finalAmount = 500
		warnings = append(warnings, "current allocation already meets or exceeds target")
	}
	finalAmount = roundToHundred(finalAmount)

	suggestion := &PositionSuggestion{
		SuggestedAmount: finalAmount,
		RangeLow:        roundToHundred(finalAmount * 0.8),
		RangeHigh:       roundToHundred(finalAmount * 1.2),
		Steps: []StepDetail{
			{Name: "S1", Description: "allocation gap amount", Value: step1},
			{Name: "S2", Description: "risk batch multiplier", Value: step2},
			{Name: "S3", Description: "timing discount applied", Value: step3},
			{Name: "S4", Description: "manual override factor", Value: step4},
			{Name: "S5", Description: "clamped and rounded amount", Value: finalAmount},
		},
		Warnings: warnings,
	}
	return suggestion, nil
}

func currentExposure(db *sqlx.DB, code string) (float64, float64, error) {
	type row struct {
		Code   string  `db:"code"`
		Shares float64 `db:"shares"`
	}

	var rows []row
	if err := db.Select(&rows, `
		SELECT code, COALESCE(shares, 0) AS shares
		FROM holdings
		WHERE COALESCE(shares, 0) > 0
	`); err != nil {
		return 0, 0, fmt.Errorf("query holdings: %w", err)
	}

	totalAssets := 0.0
	currentValue := 0.0
	for _, row := range rows {
		nav, _ := latestNav(db, row.Code)
		value := nav * row.Shares
		totalAssets += value
		if row.Code == code {
			currentValue = value
		}
	}

	currentPct := 0.0
	if totalAssets > 0 {
		currentPct = currentValue / totalAssets * 100
	}
	return totalAssets, currentPct, nil
}

func targetWeight(db *sqlx.DB, code string) (float64, bool, error) {
	// target_allocation is category-based (category, target_pct).
	// No per-fund target_weight exists; return default.
	_ = code
	_ = db
	return 10, false, nil
}

func latestTimingSignal(db *sqlx.DB, code string) (string, error) {
	var exists int
	if err := db.Get(&exists, `
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type='table' AND name='timing_signals'
	`); err != nil {
		return "neutral", fmt.Errorf("inspect timing_signals table: %w", err)
	}
	if exists == 0 {
		return "neutral", nil
	}

	columns, err := tableColumns(db, "timing_signals")
	if err != nil {
		return "neutral", err
	}
	if _, ok := columns["signal"]; !ok {
		return "neutral", nil
	}

	orderColumn := "created_at"
	for _, candidate := range []string{"signal_date", "date", "updated_at", "created_at"} {
		if _, ok := columns[candidate]; ok {
			orderColumn = candidate
			break
		}
	}

	if _, ok := columns["code"]; ok {
		var signal sql.NullString
		query := fmt.Sprintf(`
			SELECT signal
			FROM timing_signals
			WHERE code=? OR code='' OR code IS NULL
			ORDER BY %s DESC
			LIMIT 1
		`, orderColumn)
		if err := db.Get(&signal, query, code); err == nil && signal.Valid {
			return strings.ToLower(signal.String), nil
		}
	}

	var signal sql.NullString
	query := fmt.Sprintf(`
		SELECT signal
		FROM timing_signals
		ORDER BY %s DESC
		LIMIT 1
	`, orderColumn)
	if err := db.Get(&signal, query); err != nil {
		if err == sql.ErrNoRows {
			return "neutral", nil
		}
		return "neutral", fmt.Errorf("query timing signal: %w", err)
	}
	if !signal.Valid {
		return "neutral", nil
	}
	return strings.ToLower(signal.String), nil
}

func tableColumns(db *sqlx.DB, table string) (map[string]struct{}, error) {
	type columnInfo struct {
		CID     int            `db:"cid"`
		Name    string         `db:"name"`
		Type    string         `db:"type"`
		NotNull int            `db:"notnull"`
		DfltVal sql.NullString `db:"dflt_value"`
		PK      int            `db:"pk"`
	}

	var rows []columnInfo
	if err := db.Select(&rows, fmt.Sprintf("PRAGMA table_info(%s)", table)); err != nil {
		return nil, fmt.Errorf("inspect table %s: %w", table, err)
	}
	columns := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		columns[strings.ToLower(row.Name)] = struct{}{}
	}
	return columns, nil
}

func latestNav(db *sqlx.DB, code string) (float64, error) {
	var nav float64
	err := db.Get(&nav, `
		SELECT nav
		FROM nav_history
		WHERE code=? AND nav>0
		ORDER BY date DESC
		LIMIT 1
	`, code)
	return nav, err
}

func batchMultiplier(riskLevel string) float64 {
	switch strings.ToLower(riskLevel) {
	case "conservative":
		return 0.25
	case "aggressive":
		return 0.50
	default:
		return 0.33
	}
}

func timingDiscount(signal string) float64 {
	switch strings.ToLower(signal) {
	case "favorable":
		return 1.0
	case "unfavorable":
		return 0.5
	default:
		return 0.8
	}
}

func clamp(v, low, high float64) float64 {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func roundToHundred(v float64) float64 {
	if v <= 0 {
		return 0
	}
	return math.Round(v/100) * 100
}
