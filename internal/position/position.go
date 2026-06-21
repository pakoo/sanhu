package position

import (
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/timing"
)

type StepDetail struct {
	Label  string  `json:"label"`
	Detail string  `json:"detail"`
	Value  float64 `json:"value"`
}

type PositionInputs struct {
	TotalValue    float64  `json:"total_value"`
	CurrentCatPct float64  `json:"current_cat_pct"`
	TargetCatPct  *float64 `json:"target_cat_pct"`
	TimingLevel   string   `json:"timing_level"`
	OverlapRate   *float64 `json:"overlap_rate"`
}

type PositionSuggestion struct {
	Code            string         `json:"code"`
	Name            string         `json:"name"`
	Category        string         `json:"category"`
	RiskLevel       string         `json:"risk_level"`
	SuggestedAmount int            `json:"suggested_amount"`
	RangeMin        int            `json:"range_min"`
	RangeMax        int            `json:"range_max"`
	Steps           []StepDetail   `json:"steps"`
	Warnings        []string       `json:"warnings"`
	Inputs          PositionInputs `json:"inputs"`
}

const (
	minAmount        = 500.0
	singleFundCapPct = 0.30
	emptySeed        = 1000.0
)

// SuggestPosition 复刻 Python suggest_position：类别缺口 → 分批系数 → Timing 折扣
// → Overlap 折扣 → 边界取整，每步透明暴露。
func SuggestPosition(db *sqlx.DB, code, riskLevel string) (*PositionSuggestion, error) {
	if riskLevel != "conservative" && riskLevel != "moderate" && riskLevel != "aggressive" {
		riskLevel = "moderate"
	}
	batchRatio := batchMultiplier(riskLevel)

	name := fundName(db, code)
	category := fundCategory(db, code)
	totalValue, catValues := holdingsSnapshot(db)
	targets := targetAllocation(db)
	overlapRate := overlapRateOf(db, code)

	timingLevel := "neutral"
	var timingFragments []string
	if tsig, err := timing.GetTimingSignal(db, code); err == nil && tsig != nil {
		timingLevel = tsig.Level
		timingFragments = tsig.Fragments
	}

	warnings := []string{}
	steps := []StepDetail{}

	if category == "" {
		warnings = append(warnings, fmt.Sprintf("基金 %s 无类别信息，无法计算缺口", code))
		return emptyResult(code, name, "", riskLevel, warnings, timingLevel), nil
	}

	catValue := catValues[category]
	currentCatPct := 0.0
	if totalValue > 0 {
		currentCatPct = catValue / totalValue
	}
	targetCatPct, hasTarget := targets[category]

	// ── Step 1: 类别缺口 ──────────────────────────────────────────────────
	var gapAmount float64
	if totalValue <= 0 {
		gapAmount = emptySeed
		steps = append(steps, StepDetail{Label: "初始试仓 (holdings 为空)",
			Detail: fmt.Sprintf("%s 目标 %s，建议先小额 ¥%d 试仓", category, targetPctStr(targetCatPct, hasTarget), int(emptySeed)),
			Value:  emptySeed})
		warnings = append(warnings, "当前无持仓，按种子金额建议")
	} else if !hasTarget {
		gapAmount = totalValue * 0.15
		steps = append(steps, StepDetail{Label: "类别未在目标配置中",
			Detail: fmt.Sprintf("target_allocation 没定义 %s，回退到总资产 15%% 作为单基上限", category),
			Value:  round2(gapAmount)})
		warnings = append(warnings, fmt.Sprintf("请先在目标配置里定义 %s 类别的目标比例", category))
	} else {
		gapAmount = math.Max(0, targetCatPct-currentCatPct) * totalValue
		catDetail := fmt.Sprintf("%s 当前 %s (%s), 目标 %s (%s)", category, pctStr(currentCatPct), moneyStr(catValue), pctStr(targetCatPct), moneyStr(targetCatPct*totalValue))
		if gapAmount <= 0 {
			steps = append(steps, StepDetail{Label: "类别已超配", Detail: catDetail + "，无需加仓", Value: 0})
			warnings = append(warnings, fmt.Sprintf("%s 当前 %s ≥ 目标 %s，建议暂不加仓", category, pctStr(currentCatPct), pctStr(targetCatPct)))
			tp := targetCatPct
			return &PositionSuggestion{Code: code, Name: name, Category: category, RiskLevel: riskLevel,
				SuggestedAmount: 0, RangeMin: 0, RangeMax: 0, Steps: steps, Warnings: warnings,
				Inputs: PositionInputs{TotalValue: round2(totalValue), CurrentCatPct: round4(currentCatPct), TargetCatPct: &tp, TimingLevel: timingLevel, OverlapRate: overlapRate}}, nil
		}
		steps = append(steps, StepDetail{Label: "类别缺口", Detail: catDetail, Value: round2(gapAmount)})
	}

	// ── Step 2: 分批系数 ──────────────────────────────────────────────────
	batchAmount := gapAmount * batchRatio
	batchLabel := map[string]string{"conservative": "保守 1/4", "moderate": "稳健 1/3", "aggressive": "激进 1/2"}[riskLevel]
	steps = append(steps, StepDetail{Label: fmt.Sprintf("× 分批系数 (%s)", batchLabel), Detail: "分批建仓降低择时风险", Value: round2(batchAmount)})

	// ── Step 3: Timing 折扣 ───────────────────────────────────────────────
	timingFactor := timingDiscount(timingLevel)
	afterTiming := batchAmount * timingFactor
	timingDetail := timingLevel
	if len(timingFragments) > 0 {
		n := 2
		if len(timingFragments) < 2 {
			n = len(timingFragments)
		}
		timingDetail = strings.Join(timingFragments[:n], " / ")
	}
	steps = append(steps, StepDetail{Label: fmt.Sprintf("× Timing 折扣 %g", timingFactor), Detail: timingDetail, Value: round2(afterTiming)})
	if timingLevel == "unfavorable" {
		warnings = append(warnings, "市场 timing 信号不利（高估/均线偏高），建议延后或进一步减半")
	} else if timingLevel == "neutral" {
		warnings = append(warnings, "市场 timing 中性，已按 0.8 折扣")
	}

	// ── Step 4: Overlap 折扣 ──────────────────────────────────────────────
	overlapFactor := overlapDiscount(overlapRate)
	afterOverlap := afterTiming * overlapFactor
	overlapDetail := "持仓数据不足，未打折"
	if overlapRate != nil {
		overlapDetail = fmt.Sprintf("与现有持仓重叠率 %s", pctStr(*overlapRate))
	}
	steps = append(steps, StepDetail{Label: fmt.Sprintf("× Overlap 折扣 %g", overlapFactor), Detail: overlapDetail, Value: round2(afterOverlap)})
	if overlapRate != nil && *overlapRate >= 0.40 {
		warnings = append(warnings, fmt.Sprintf("与现有持仓重叠率 %s，组合集中度过高", pctStr(*overlapRate)))
	}

	// ── Step 5: 边界 + 取整 ───────────────────────────────────────────────
	singleCap := gapAmount
	if totalValue > 0 {
		singleCap = totalValue * singleFundCapPct
	}
	rangeMax := math.Max(minAmount, math.Min(singleCap, gapAmount))
	clamped := math.Max(0, math.Min(afterOverlap, rangeMax))
	if clamped > 0 && clamped < minAmount {
		clamped = minAmount
	}
	rounded := math.Round(clamped/100) * 100
	steps = append(steps, StepDetail{Label: "边界约束 + 取整百元",
		Detail: fmt.Sprintf("范围 %s ~ %s，单基上限 %s 总资产", moneyStr(minAmount), moneyStr(rangeMax), pctStr(singleFundCapPct)),
		Value:  rounded})

	var tp *float64
	if hasTarget {
		v := round4(targetCatPct)
		tp = &v
	}
	return &PositionSuggestion{
		Code: code, Name: name, Category: category, RiskLevel: riskLevel,
		SuggestedAmount: int(rounded), RangeMin: int(minAmount), RangeMax: int(math.Round(rangeMax)),
		Steps: steps, Warnings: warnings,
		Inputs: PositionInputs{TotalValue: round2(totalValue), CurrentCatPct: round4(currentCatPct), TargetCatPct: tp, TimingLevel: timingLevel, OverlapRate: overlapRate},
	}, nil
}

func emptyResult(code, name, category, riskLevel string, warnings []string, timingLevel string) *PositionSuggestion {
	return &PositionSuggestion{Code: code, Name: name, Category: category, RiskLevel: riskLevel,
		SuggestedAmount: 0, RangeMin: 0, RangeMax: 0, Steps: []StepDetail{}, Warnings: warnings,
		Inputs: PositionInputs{TimingLevel: timingLevel}}
}

// holdingsSnapshot 各类别市值快照（市值 = shares×最新nav，缺则用 cost_amount）。
func holdingsSnapshot(db *sqlx.DB) (float64, map[string]float64) {
	type row struct {
		Category string  `db:"category"`
		MV       float64 `db:"market_value"`
	}
	var rows []row
	_ = db.Select(&rows, `
		SELECT COALESCE(fp.category, f.category, 'unknown') AS category,
		       COALESCE(h.shares * n.nav, h.cost_amount, 0) AS market_value
		FROM holdings h
		LEFT JOIN funds f ON f.code=h.code
		LEFT JOIN fund_profile fp ON fp.code=h.code
		LEFT JOIN (
			SELECT nh.code, nh.nav FROM nav_history nh
			JOIN (SELECT code, MAX(date) AS d FROM nav_history GROUP BY code) m
			  ON m.code=nh.code AND m.d=nh.date
		) n ON n.code=h.code`)
	total := 0.0
	cat := map[string]float64{}
	for _, r := range rows {
		c := r.Category
		if c == "" {
			c = "unknown"
		}
		total += r.MV
		cat[c] += r.MV
	}
	return total, cat
}

func fundName(db *sqlx.DB, code string) string {
	var s sql.NullString
	_ = db.Get(&s, `SELECT COALESCE((SELECT name FROM fund_profile WHERE code=?),(SELECT name FROM funds WHERE code=?))`, code, code)
	return s.String
}

func fundCategory(db *sqlx.DB, code string) string {
	var s sql.NullString
	_ = db.Get(&s, `SELECT COALESCE((SELECT category FROM fund_profile WHERE code=?),(SELECT category FROM funds WHERE code=?))`, code, code)
	return s.String
}

func targetAllocation(db *sqlx.DB) map[string]float64 {
	type row struct {
		Category  string  `db:"category"`
		TargetPct float64 `db:"target_pct"`
	}
	var rows []row
	_ = db.Select(&rows, `SELECT category, target_pct FROM target_allocation`)
	m := map[string]float64{}
	for _, r := range rows {
		m[r.Category] = r.TargetPct / 100.0
	}
	return m
}

// overlapRateOf 候选基金前十大重仓股与用户权益类持仓重仓股的重叠率。
func overlapRateOf(db *sqlx.DB, code string) *float64 {
	var userStocks []string
	_ = db.Select(&userStocks, `
		SELECT DISTINCT fh.stock_code
		FROM fund_holdings fh
		JOIN (SELECT code, MAX(report_date) AS report_date FROM fund_holdings GROUP BY code) latest
		  ON latest.code=fh.code AND latest.report_date=fh.report_date
		JOIN holdings h ON h.code=fh.code
		JOIN funds f ON f.code=h.code
		WHERE f.category IN ('equity','mixed','qdii')`)
	var candStocks []string
	_ = db.Select(&candStocks, `
		SELECT fh.stock_code
		FROM fund_holdings fh
		JOIN (SELECT code, MAX(report_date) AS report_date FROM fund_holdings WHERE code=? GROUP BY code) latest
		  ON latest.code=fh.code AND latest.report_date=fh.report_date
		WHERE fh.code=?`, code, code)
	candSet := map[string]bool{}
	for _, s := range candStocks {
		if s != "" {
			candSet[s] = true
		}
	}
	if len(candSet) == 0 {
		return nil
	}
	userSet := map[string]bool{}
	for _, s := range userStocks {
		if s != "" {
			userSet[s] = true
		}
	}
	if len(userSet) == 0 {
		z := 0.0
		return &z
	}
	inter := 0
	for s := range candSet {
		if userSet[s] {
			inter++
		}
	}
	r := float64(inter) / float64(len(candSet))
	return &r
}

func overlapDiscount(rate *float64) float64 {
	if rate == nil {
		return 1.0
	}
	if *rate < 0.20 {
		return 1.0
	}
	if *rate < 0.40 {
		return 0.8
	}
	return 0.5
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
func pctStr(v float64) string  { return fmt.Sprintf("%.1f%%", v*100) }

func moneyStr(v float64) string {
	n := int(math.Round(v))
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	out := strings.Join(parts, ",")
	if neg {
		out = "-" + out
	}
	return "¥" + out
}

func targetPctStr(target float64, has bool) string {
	if !has {
		return "未配置"
	}
	return pctStr(target)
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
		return 1.0 / 3.0
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
