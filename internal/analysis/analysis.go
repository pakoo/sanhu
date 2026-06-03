package analysis

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
)

type StockExposure struct {
	StockCode   string  `json:"stock_code"`
	StockName   string  `json:"stock_name"`
	WeightedPct float64 `json:"weighted_pct"`
}

type OverlapResult struct {
	TopStocks      []StockExposure `json:"top_stocks"`
	HHI            float64         `json:"hhi"`
	DiversityScore float64         `json:"diversity_score"`
}

type IndustryWeight struct {
	Industry string  `json:"industry"`
	Weight   float64 `json:"weight"`
}

type HoldingsChange struct {
	Code       string   `json:"code"`
	NewEntries []string `json:"new_entries"`
	Exits      []string `json:"exits"`
	Increased  []string `json:"increased"`
	Decreased  []string `json:"decreased"`
	Unchanged  []string `json:"unchanged"`
}

func GetHoldingsOverlap(db *sqlx.DB) (*OverlapResult, error) {
	positions, totalValue, err := portfolioPositions(db)
	if err != nil {
		return nil, err
	}
	if totalValue == 0 {
		return &OverlapResult{}, nil
	}

	fundCodeCol, err := chooseColumn(db, "fund_holdings", "fund_code", "code")
	if err != nil {
		return nil, err
	}

	type holdingRow struct {
		StockCode string  `db:"stock_code"`
		StockName string  `db:"stock_name"`
		Weight    float64 `db:"weight"`
	}

	exposures := map[string]*StockExposure{}
	for _, position := range positions {
		reportDate, err := maxReportDate(db, "fund_holdings", fundCodeCol, position.Code)
		if err != nil || reportDate == "" {
			continue
		}

		query := fmt.Sprintf(`
			SELECT stock_code, stock_name, COALESCE(weight, 0) AS weight
			FROM fund_holdings
			WHERE %s=? AND report_date=?
		`, fundCodeCol)

		var rows []holdingRow
		if err := db.Select(&rows, query, position.Code, reportDate); err != nil {
			return nil, fmt.Errorf("query fund holdings %s: %w", position.Code, err)
		}

		portfolioWeight := position.Value / totalValue
		for _, row := range rows {
			exposurePct := portfolioWeight * row.Weight
			item := exposures[row.StockCode]
			if item == nil {
				item = &StockExposure{
					StockCode: row.StockCode,
					StockName: row.StockName,
				}
				exposures[row.StockCode] = item
			}
			item.WeightedPct += exposurePct
			if item.StockName == "" {
				item.StockName = row.StockName
			}
		}
	}

	topStocks := make([]StockExposure, 0, len(exposures))
	totalExposurePct := 0.0
	for _, exposure := range exposures {
		topStocks = append(topStocks, *exposure)
		totalExposurePct += exposure.WeightedPct
	}
	sort.Slice(topStocks, func(i, j int) bool {
		if topStocks[i].WeightedPct == topStocks[j].WeightedPct {
			return topStocks[i].StockCode < topStocks[j].StockCode
		}
		return topStocks[i].WeightedPct > topStocks[j].WeightedPct
	})
	if len(topStocks) > 10 {
		topStocks = topStocks[:10]
	}

	hhi := 0.0
	if totalExposurePct > 0 {
		for _, exposure := range exposures {
			w := exposure.WeightedPct / totalExposurePct
			hhi += w * w
		}
	}

	result := &OverlapResult{
		TopStocks: topStocks,
		HHI:       hhi,
	}
	if hhi > 0 {
		result.DiversityScore = 1 / hhi
	}
	return result, nil
}

func GetIndustryBreakdown(db *sqlx.DB) ([]IndustryWeight, error) {
	positions, totalValue, err := portfolioPositions(db)
	if err != nil {
		return nil, err
	}
	if totalValue == 0 {
		return nil, nil
	}

	fundCodeCol, err := chooseColumn(db, "fund_industry", "fund_code", "code")
	if err != nil {
		return nil, err
	}
	industryCol, err := chooseColumn(db, "fund_industry", "industry_name", "industry")
	if err != nil {
		return nil, err
	}

	type row struct {
		Industry string  `db:"industry"`
		Weight   float64 `db:"weight"`
	}

	weights := map[string]float64{}
	for _, position := range positions {
		reportDate, err := maxReportDate(db, "fund_industry", fundCodeCol, position.Code)
		if err != nil || reportDate == "" {
			continue
		}

		query := fmt.Sprintf(`
			SELECT %s AS industry, COALESCE(weight, 0) AS weight
			FROM fund_industry
			WHERE %s=? AND report_date=?
		`, industryCol, fundCodeCol)

		var rows []row
		if err := db.Select(&rows, query, position.Code, reportDate); err != nil {
			return nil, fmt.Errorf("query fund industry %s: %w", position.Code, err)
		}

		portfolioWeight := position.Value / totalValue
		for _, row := range rows {
			weights[row.Industry] += portfolioWeight * row.Weight
		}
	}

	out := make([]IndustryWeight, 0, len(weights))
	for industry, weight := range weights {
		out = append(out, IndustryWeight{Industry: industry, Weight: weight})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Weight == out[j].Weight {
			return out[i].Industry < out[j].Industry
		}
		return out[i].Weight > out[j].Weight
	})
	return out, nil
}

func GetHoldingsChanges(db *sqlx.DB, code string) (*HoldingsChange, error) {
	fundCodeCol, err := chooseColumn(db, "fund_holdings", "fund_code", "code")
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`
		SELECT DISTINCT report_date
		FROM fund_holdings
		WHERE %s=?
		ORDER BY report_date DESC
		LIMIT 2
	`, fundCodeCol)

	var reportDates []string
	if err := db.Select(&reportDates, query, code); err != nil {
		return nil, fmt.Errorf("query report dates: %w", err)
	}
	if len(reportDates) == 0 {
		return &HoldingsChange{Code: code}, nil
	}
	if len(reportDates) == 1 {
		reportDates = append(reportDates, reportDates[0])
	}

	type row struct {
		StockCode string  `db:"stock_code"`
		StockName string  `db:"stock_name"`
		Weight    float64 `db:"weight"`
	}

	load := func(reportDate string) (map[string]row, error) {
		query := fmt.Sprintf(`
			SELECT stock_code, stock_name, COALESCE(weight, 0) AS weight
			FROM fund_holdings
			WHERE %s=? AND report_date=?
		`, fundCodeCol)
		var rows []row
		if err := db.Select(&rows, query, code, reportDate); err != nil {
			return nil, err
		}
		out := make(map[string]row, len(rows))
		for _, row := range rows {
			out[row.StockCode] = row
		}
		return out, nil
	}

	current, err := load(reportDates[0])
	if err != nil {
		return nil, fmt.Errorf("query current holdings: %w", err)
	}
	previous, err := load(reportDates[1])
	if err != nil {
		return nil, fmt.Errorf("query previous holdings: %w", err)
	}

	result := &HoldingsChange{Code: code}
	for stockCode, row := range current {
		prev, ok := previous[stockCode]
		label := formatHoldingLabel(stockCode, row.StockName)
		switch {
		case !ok:
			result.NewEntries = append(result.NewEntries, label)
		case row.Weight > prev.Weight:
			result.Increased = append(result.Increased, label)
		case row.Weight < prev.Weight:
			result.Decreased = append(result.Decreased, label)
		default:
			result.Unchanged = append(result.Unchanged, label)
		}
	}
	for stockCode, row := range previous {
		if _, ok := current[stockCode]; !ok {
			result.Exits = append(result.Exits, formatHoldingLabel(stockCode, row.StockName))
		}
	}

	sort.Strings(result.NewEntries)
	sort.Strings(result.Exits)
	sort.Strings(result.Increased)
	sort.Strings(result.Decreased)
	sort.Strings(result.Unchanged)

	return result, nil
}

type portfolioPosition struct {
	Code  string
	Value float64
}

func portfolioPositions(db *sqlx.DB) ([]portfolioPosition, float64, error) {
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
		return nil, 0, fmt.Errorf("query holdings: %w", err)
	}

	positions := make([]portfolioPosition, 0, len(rows))
	totalValue := 0.0
	for _, row := range rows {
		nav, _ := latestNav(db, row.Code)
		value := nav * row.Shares
		if value <= 0 {
			continue
		}
		positions = append(positions, portfolioPosition{
			Code:  row.Code,
			Value: value,
		})
		totalValue += value
	}
	return positions, totalValue, nil
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

func maxReportDate(db *sqlx.DB, table, codeColumn, code string) (string, error) {
	query := fmt.Sprintf(`SELECT MAX(report_date) FROM %s WHERE %s=?`, table, codeColumn)
	var reportDate sql.NullString
	if err := db.Get(&reportDate, query, code); err != nil {
		return "", err
	}
	if !reportDate.Valid {
		return "", nil
	}
	return reportDate.String, nil
}

func chooseColumn(db *sqlx.DB, table string, candidates ...string) (string, error) {
	type columnInfo struct {
		CID     int            `db:"cid"`
		Name    string         `db:"name"`
		Type    string         `db:"type"`
		NotNull int            `db:"notnull"`
		DfltVal sql.NullString `db:"dflt_value"`
		PK      int            `db:"pk"`
	}

	query := fmt.Sprintf("PRAGMA table_info(%s)", table)
	var columns []columnInfo
	if err := db.Select(&columns, query); err != nil {
		return "", fmt.Errorf("inspect %s: %w", table, err)
	}

	available := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		available[strings.ToLower(column.Name)] = struct{}{}
	}

	for _, candidate := range candidates {
		if _, ok := available[strings.ToLower(candidate)]; ok {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no matching column for %s", table)
}

func formatHoldingLabel(stockCode, stockName string) string {
	if stockName == "" {
		return stockCode
	}
	return stockCode + " " + stockName
}
