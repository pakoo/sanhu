package analysis

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
)

type StockExposure struct {
	StockCode   string  `json:"stock_code"`
	StockName   string  `json:"stock_name"`
	WeightedPct float64 `json:"weighted_pct"`
}

// FundWeight 单只基金对某股票的持仓权重(用于 overlap 明细)
type FundWeight struct {
	Code     string  `json:"code"`
	FundName string  `json:"fund_name"`
	Weight   float64 `json:"weight"`
}

// OverlapStock 一只被多只基金共同持有的股票
type OverlapStock struct {
	StockCode        string       `json:"stock_code"`
	StockName        string       `json:"stock_name"`
	AppearIn         []string     `json:"appear_in"`
	AppearCount      int          `json:"appear_count"`
	TotalExposurePct float64      `json:"total_exposure_pct"`
	FundWeights      []FundWeight `json:"fund_weights"`
}

type OverlapResult struct {
	OverlapStocks        []OverlapStock `json:"overlap_stocks"`
	DiversificationScore float64        `json:"diversification_score"`
	TotalFundsAnalyzed   int            `json:"total_funds_analyzed"`
	ReportDate           string         `json:"report_date"`
}

type IndustryWeight struct {
	Industry string  `json:"industry"`
	Weight   float64 `json:"weight"`
}

// IndustryFund 某行业内单只基金的权重明细
type IndustryFund struct {
	Code     string  `json:"code"`
	FundName string  `json:"fund_name"`
	Weight   float64 `json:"weight"`
}

// IndustryAgg 某行业的组合加权占比 + 基金拆解
type IndustryAgg struct {
	Industry      string         `json:"industry"`
	TotalWeight   float64        `json:"total_weight"`
	FundBreakdown []IndustryFund `json:"fund_breakdown"`
}

// IndustryBreakdownResult 与 Python get_industry_breakdown 对齐
type IndustryBreakdownResult struct {
	Industries    []IndustryAgg `json:"industries"`
	ReportDate    string        `json:"report_date"`
	FundsAnalyzed []string      `json:"funds_analyzed"`
}

type HoldingsChange struct {
	Code       string   `json:"code"`
	NewEntries []string `json:"new_entries"`
	Exits      []string `json:"exits"`
	Increased  []string `json:"increased"`
	Decreased  []string `json:"decreased"`
	Unchanged  []string `json:"unchanged"`
}

// GetHoldingsOverlap 复刻 Python get_holdings_overlap：
// 非债基金间的「重叠持股」——出现在 ≥2 只基金里的股票，按组合加权敞口排序，
// 并给出分散度评分(1/赫芬达尔指数)。
func GetHoldingsOverlap(db *sqlx.DB) (*OverlapResult, error) {
	fundCodeCol, err := chooseColumn(db, "fund_holdings", "fund_code", "code")
	if err != nil {
		return nil, err
	}

	// 非债持仓基金 + 当前市值
	type fundRow struct {
		Code   string  `db:"code"`
		Name   string  `db:"name"`
		Shares float64 `db:"shares"`
	}
	var frows []fundRow
	if err := db.Select(&frows, `
		SELECT f.code, COALESCE(f.name,'') AS name, COALESCE(h.shares,0) AS shares
		FROM funds f JOIN holdings h ON f.code=h.code
		WHERE f.category != 'bond'`); err != nil {
		return nil, fmt.Errorf("query overlap funds: %w", err)
	}

	type fundInfo struct {
		name       string
		pctOfTotal float64 // 0-100
	}
	fundMap := map[string]*fundInfo{}
	totalMV := 0.0
	mvOf := map[string]float64{}
	for _, r := range frows {
		nav, _ := latestNav(db, r.Code)
		mv := nav * r.Shares
		mvOf[r.Code] = mv
		totalMV += mv
		fundMap[r.Code] = &fundInfo{name: r.Name}
	}
	for code, fi := range fundMap {
		if totalMV > 0 {
			fi.pctOfTotal = mvOf[code] / totalMV * 100
		}
	}

	if len(fundMap) == 0 {
		return &OverlapResult{OverlapStocks: []OverlapStock{}, DiversificationScore: 1.0}, nil
	}

	// stock_code -> {name, [{fund, weight}]}
	type sf struct {
		code   string
		weight float64
	}
	type stockInfo struct {
		name  string
		funds []sf
	}
	stockMap := map[string]*stockInfo{}
	latestDate := ""
	type holdingRow struct {
		StockCode string  `db:"stock_code"`
		StockName string  `db:"stock_name"`
		Weight    float64 `db:"weight"`
	}
	for code := range fundMap {
		reportDate, err := maxReportDate(db, "fund_holdings", fundCodeCol, code)
		if err != nil || reportDate == "" {
			continue
		}
		if reportDate > latestDate {
			latestDate = reportDate
		}
		query := fmt.Sprintf(`
			SELECT stock_code, stock_name, COALESCE(weight,0) AS weight
			FROM fund_holdings WHERE %s=? AND report_date=?`, fundCodeCol)
		var rows []holdingRow
		if err := db.Select(&rows, query, code, reportDate); err != nil {
			return nil, fmt.Errorf("query fund holdings %s: %w", code, err)
		}
		for _, row := range rows {
			si := stockMap[row.StockCode]
			if si == nil {
				si = &stockInfo{name: row.StockName}
				stockMap[row.StockCode] = si
			}
			si.funds = append(si.funds, sf{code: code, weight: row.Weight})
		}
	}

	overlap := make([]OverlapStock, 0)
	for sc, si := range stockMap {
		if len(si.funds) < 2 {
			continue
		}
		total := 0.0
		fws := make([]FundWeight, 0, len(si.funds))
		appearIn := make([]string, 0, len(si.funds))
		for _, fw := range si.funds {
			fi := fundMap[fw.code]
			total += fi.pctOfTotal * fw.weight / 100
			fws = append(fws, FundWeight{Code: fw.code, FundName: fi.name, Weight: fw.weight})
			appearIn = append(appearIn, fw.code)
		}
		overlap = append(overlap, OverlapStock{
			StockCode:        sc,
			StockName:        si.name,
			AppearIn:         appearIn,
			AppearCount:      len(si.funds),
			TotalExposurePct: math.Round(total*10000) / 10000,
			FundWeights:      fws,
		})
	}
	sort.Slice(overlap, func(i, j int) bool {
		if overlap[i].TotalExposurePct == overlap[j].TotalExposurePct {
			return overlap[i].StockCode < overlap[j].StockCode
		}
		return overlap[i].TotalExposurePct > overlap[j].TotalExposurePct
	})

	// 分散度评分 = 1 / 赫芬达尔指数(基金占比平方和)
	herfindahl := 0.0
	for _, fi := range fundMap {
		p := fi.pctOfTotal / 100
		herfindahl += p * p
	}
	divScore := 1.0
	if herfindahl > 0 {
		divScore = math.Round(1/herfindahl*100) / 100
	}

	return &OverlapResult{
		OverlapStocks:        overlap,
		DiversificationScore: divScore,
		TotalFundsAnalyzed:   len(fundMap),
		ReportDate:           latestDate,
	}, nil
}

// GetIndustryBreakdown 复刻 Python get_industry_breakdown：
// 非债基金按市值占比加权的行业暴露 + 每行业的基金拆解。
func GetIndustryBreakdown(db *sqlx.DB) (*IndustryBreakdownResult, error) {
	fundCodeCol, err := chooseColumn(db, "fund_industry", "fund_code", "code")
	if err != nil {
		return nil, err
	}
	industryCol, err := chooseColumn(db, "fund_industry", "industry_name", "industry")
	if err != nil {
		return nil, err
	}

	// 非债持仓基金 + 市值占比
	type fundRow struct {
		Code   string  `db:"code"`
		Name   string  `db:"name"`
		Shares float64 `db:"shares"`
	}
	var frows []fundRow
	if err := db.Select(&frows, `
		SELECT f.code, COALESCE(f.name,'') AS name, COALESCE(h.shares,0) AS shares
		FROM funds f JOIN holdings h ON f.code=h.code
		WHERE f.category != 'bond'`); err != nil {
		return nil, fmt.Errorf("query industry funds: %w", err)
	}
	totalMV := 0.0
	mvOf := map[string]float64{}
	nameOf := map[string]string{}
	codes := make([]string, 0, len(frows))
	for _, r := range frows {
		nav, _ := latestNav(db, r.Code)
		mv := nav * r.Shares
		mvOf[r.Code] = mv
		nameOf[r.Code] = r.Name
		totalMV += mv
		codes = append(codes, r.Code)
	}

	type entry struct {
		code, name string
		weight     float64
		fundPct    float64
	}
	industryMap := map[string][]entry{}
	latestDate := ""
	analyzed := make([]string, 0)
	type irow struct {
		Industry string  `db:"industry"`
		Weight   float64 `db:"weight"`
	}
	for _, code := range codes {
		reportDate, err := maxReportDate(db, "fund_industry", fundCodeCol, code)
		if err != nil || reportDate == "" {
			continue
		}
		if reportDate > latestDate {
			latestDate = reportDate
		}
		analyzed = append(analyzed, code)
		query := fmt.Sprintf(`
			SELECT %s AS industry, COALESCE(weight,0) AS weight
			FROM fund_industry WHERE %s=? AND report_date=?`, industryCol, fundCodeCol)
		var rows []irow
		if err := db.Select(&rows, query, code, reportDate); err != nil {
			return nil, fmt.Errorf("query fund industry %s: %w", code, err)
		}
		fundPct := 0.0
		if totalMV > 0 {
			fundPct = mvOf[code] / totalMV * 100
		}
		for _, r := range rows {
			industryMap[r.Industry] = append(industryMap[r.Industry], entry{code: code, name: nameOf[code], weight: r.Weight, fundPct: fundPct})
		}
	}

	industries := make([]IndustryAgg, 0, len(industryMap))
	for ind, entries := range industryMap {
		total := 0.0
		fb := make([]IndustryFund, 0, len(entries))
		for _, e := range entries {
			total += e.weight * e.fundPct / 100
			fb = append(fb, IndustryFund{Code: e.code, FundName: e.name, Weight: e.weight})
		}
		industries = append(industries, IndustryAgg{Industry: ind, TotalWeight: math.Round(total*100) / 100, FundBreakdown: fb})
	}
	sort.Slice(industries, func(i, j int) bool {
		if industries[i].TotalWeight == industries[j].TotalWeight {
			return industries[i].Industry < industries[j].Industry
		}
		return industries[i].TotalWeight > industries[j].TotalWeight
	})

	return &IndustryBreakdownResult{
		Industries:    industries,
		ReportDate:    latestDate,
		FundsAnalyzed: analyzed,
	}, nil
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
