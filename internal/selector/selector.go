package selector

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/analysis"
)

type Gap struct {
	Category   string  `json:"category"`
	Theme      string  `json:"theme"`
	CurrentPct float64 `json:"current_pct"` // 当前在该主题上的实际暴露，前端要显示"暴露 X%"
	DeficitPct float64 `json:"deficit_pct"`
	Priority   int     `json:"priority"`
}

type ScoredCandidate struct {
	Code          string  `json:"code"`
	Name          string  `json:"name"`
	Score         float64 `json:"score"`
	GapFitScore   float64 `json:"gap_fit_score"`
	QualityScore  float64 `json:"quality_score"`
	PeerRankScore float64 `json:"peer_rank_score"`
}

type RecommendationResult struct {
	Gaps          []Gap             `json:"gaps"`
	TopCandidates []ScoredCandidate `json:"top_candidates"`
	SessionID     string            `json:"session_id"`
}

type UserAnswers struct {
	Direction     string `json:"direction"`
	RiskTolerance string `json:"risk_tolerance"`
	TimePeriod    string `json:"time_period"`
	AllowOverlap  bool   `json:"allow_overlap"`
}

func DiagnosePortfolioGaps(db *sqlx.DB) ([]Gap, error) {
	breakdown, err := analysis.GetIndustryBreakdown(db)
	if err != nil {
		return nil, err
	}

	gaps := make([]Gap, 0, len(breakdown.Industries))
	for _, item := range breakdown.Industries {
		deficit := 10 - item.TotalWeight
		if deficit <= 0 {
			continue
		}
		gaps = append(gaps, Gap{
			Category:   "industry",
			Theme:      item.Industry,
			CurrentPct: item.TotalWeight,
			DeficitPct: deficit,
			Priority:   gapPriority(deficit),
		})
	}

	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].DeficitPct == gaps[j].DeficitPct {
			return gaps[i].Theme < gaps[j].Theme
		}
		return gaps[i].DeficitPct > gaps[j].DeficitPct
	})
	return gaps, nil
}

func BuildCandidatePool(db *sqlx.DB, category, theme string, minAUM float64) ([]string, error) {
	codes := map[string]struct{}{}

	if theme != "" {
		industryCodes, err := candidateCodesByIndustry(db, theme, minAUM)
		if err == nil {
			for _, code := range industryCodes {
				codes[code] = struct{}{}
			}
		}
	}

	// 候选池必须从 fund_profile 起（全市场约 2.8 万只），不能从 funds 起。
	// funds 表只装用户碰过的基金——新装的用户那里就 11 只（内置数据），
	// 从中选基永远选不出东西：推荐结果恒为空，而页面不会报错，看起来像"没推荐"。
	// Python 版同样是以 fund_profile 为全集（UNION funds 兜底）。
	//
	// 另外两列真实存在的筛选字段是 theme_tags / top_industry / sub_style；
	// 原来写的 investment_objective / strategy / tracking_index 在库里根本不存在，
	// 一调用就报 "no such column"。
	var textRows []string
	if err := db.Select(&textRows, `
		SELECT DISTINCT p.code
		FROM fund_profile p
		LEFT JOIN funds f ON f.code=p.code
		WHERE COALESCE(p.aum, 0) >= ?
		  AND EXISTS (
			SELECT 1 FROM fund_peer_ranks r WHERE r.code=p.code
		  )
		  AND (
			? = ''
			OR COALESCE(p.name, '') LIKE '%' || ? || '%'
			OR COALESCE(p.theme_tags, '') LIKE '%' || ? || '%'
			OR COALESCE(p.top_industry, '') LIKE '%' || ? || '%'
			OR COALESCE(p.sub_style, '') LIKE '%' || ? || '%'
			OR COALESCE(f.fund_type, '') LIKE '%' || ? || '%'
		  )
		ORDER BY COALESCE(p.aum, 0) DESC, p.code ASC
		LIMIT 300
	`, minAUM, theme, theme, theme, theme, theme, theme); err != nil {
		return nil, fmt.Errorf("query candidate pool: %w", err)
	}
	for _, code := range textRows {
		codes[code] = struct{}{}
	}

	out := make([]string, 0, len(codes))
	for code := range codes {
		out = append(out, code)
	}
	sort.Strings(out)
	if len(out) > 300 {
		out = out[:300]
	}
	return out, nil
}

func ScoreCandidatesForGap(db *sqlx.DB, codes []string, gap Gap) ([]ScoredCandidate, error) {
	scored := make([]ScoredCandidate, 0, len(codes))
	for _, code := range codes {
		name, _ := fundName(db, code)
		gapFit := gapFitScore(db, code, gap)
		quality := latestFundScore(db, code)
		peer := peerRankScore(db, code)

		scored = append(scored, ScoredCandidate{
			Code:          code,
			Name:          name,
			GapFitScore:   gapFit,
			QualityScore:  quality,
			PeerRankScore: peer,
			Score:         gapFit*0.45 + quality*0.35 + peer*0.20,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Code < scored[j].Code
		}
		return scored[i].Score > scored[j].Score
	})
	return scored, nil
}

func GenerateRecommendation(db *sqlx.DB, answers UserAnswers) (*RecommendationResult, error) {
	gaps, err := DiagnosePortfolioGaps(db)
	if err != nil {
		return nil, err
	}

	minAUM := candidateMinAUM(answers)
	heldCodes, err := heldFundCodes(db)
	if err != nil {
		return nil, err
	}

	bestByCode := map[string]ScoredCandidate{}
	for _, gap := range gaps {
		pool, err := BuildCandidatePool(db, gap.Category, gap.Theme, minAUM)
		if err != nil {
			return nil, err
		}
		if !answers.AllowOverlap {
			pool = filterCodes(pool, func(code string) bool {
				_, held := heldCodes[code]
				return !held
			})
		}
		scored, err := ScoreCandidatesForGap(db, pool, gap)
		if err != nil {
			return nil, err
		}
		for _, candidate := range scored {
			current, ok := bestByCode[candidate.Code]
			if !ok || candidate.Score > current.Score {
				bestByCode[candidate.Code] = candidate
			}
		}
	}

	topCandidates := make([]ScoredCandidate, 0, len(bestByCode))
	for _, candidate := range bestByCode {
		topCandidates = append(topCandidates, candidate)
	}
	sort.Slice(topCandidates, func(i, j int) bool {
		if topCandidates[i].Score == topCandidates[j].Score {
			return topCandidates[i].Code < topCandidates[j].Code
		}
		return topCandidates[i].Score > topCandidates[j].Score
	})
	if len(topCandidates) > 6 {
		topCandidates = topCandidates[:6]
	}

	sessionID := fmt.Sprintf("selector-%s", time.Now().Format("20060102150405"))
	result := &RecommendationResult{
		Gaps:          gaps,
		TopCandidates: topCandidates,
		SessionID:     sessionID,
	}

	// selector_cache 的真实结构是 (session_id, answers_json, gaps_json, candidates_json, created_at)，
	// 见 migrations/001_init.sql。原来这里按 (cache_key, cache_value, expires_at, updated_at) 写，
	// 那套列名在库里根本不存在 —— 推荐一生成就报 "no such column"，整个选基流程走不完。
	answersJSON, err := json.Marshal(answers)
	if err != nil {
		return nil, fmt.Errorf("marshal selector answers: %w", err)
	}
	gapsJSON, err := json.Marshal(gaps)
	if err != nil {
		return nil, fmt.Errorf("marshal selector gaps: %w", err)
	}
	candidatesJSON, err := json.Marshal(topCandidates)
	if err != nil {
		return nil, fmt.Errorf("marshal selector candidates: %w", err)
	}

	if _, err := db.Exec(`
		INSERT OR REPLACE INTO selector_cache
			(session_id, answers_json, gaps_json, candidates_json, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))
	`, sessionID, string(answersJSON), string(gapsJSON), string(candidatesJSON)); err != nil {
		return nil, fmt.Errorf("write selector cache: %w", err)
	}

	return result, nil
}

func FindPeerFundsByTheme(db *sqlx.DB, seedCode string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 10
	}

	industryCodes, err := latestFundIndustryCodes(db, seedCode)
	if err == nil && len(industryCodes) > 0 {
		out, err := peerFundsByIndustry(db, seedCode, industryCodes, limit)
		if err == nil && len(out) > 0 {
			return out, nil
		}
	}

	var fundType sql.NullString
	if err := db.Get(&fundType, `SELECT fund_type FROM funds WHERE code=?`, seedCode); err != nil {
		return nil, fmt.Errorf("query seed fund type: %w", err)
	}
	if !fundType.Valid || fundType.String == "" {
		return nil, nil
	}

	var rows []string
	if err := db.Select(&rows, `
		SELECT code
		FROM funds
		WHERE code<>? AND fund_type=?
		ORDER BY COALESCE(scope, 0) DESC, code ASC
		LIMIT ?
	`, seedCode, fundType.String, limit); err != nil {
		return nil, fmt.Errorf("query peer funds fallback: %w", err)
	}
	return rows, nil
}

func candidateCodesByIndustry(db *sqlx.DB, theme string, minAUM float64) ([]string, error) {
	fundCodeCol, err := chooseColumn(db, "fund_industry", "fund_code", "code")
	if err != nil {
		return nil, err
	}
	industryCol, err := chooseColumn(db, "fund_industry", "industry_name", "industry")
	if err != nil {
		return nil, err
	}

	// 同样的问题：INNER JOIN funds 会把候选限死在用户已有的那几只基金上。
	// 改join fund_profile（全市场），规模用 profile 的 aum 字段。
	query := fmt.Sprintf(`
		SELECT DISTINCT fi.%s
		FROM fund_industry fi
		INNER JOIN fund_profile p ON p.code=fi.%s
		WHERE %s=?
		  AND COALESCE(p.aum, 0) >= ?
		  AND EXISTS (
			SELECT 1 FROM fund_peer_ranks r WHERE r.code=p.code
		  )
		ORDER BY COALESCE(p.aum, 0) DESC, fi.%s ASC
		LIMIT 300
	`, fundCodeCol, fundCodeCol, industryCol, fundCodeCol)

	var rows []string
	if err := db.Select(&rows, query, theme, minAUM); err != nil {
		return nil, fmt.Errorf("query candidate industries: %w", err)
	}
	return rows, nil
}

func gapFitScore(db *sqlx.DB, code string, gap Gap) float64 {
	industries, err := latestFundIndustries(db, code)
	if err == nil {
		for _, industry := range industries {
			if strings.EqualFold(industry, gap.Theme) {
				return 100
			}
		}
	}

	name, _ := fundName(db, code)
	if strings.Contains(strings.ToLower(name), strings.ToLower(gap.Theme)) {
		return 75
	}
	return 40
}

func latestFundScore(db *sqlx.DB, code string) float64 {
	var score sql.NullFloat64
	if err := db.Get(&score, `
		SELECT total_score
		FROM fund_scores
		WHERE code=?
		ORDER BY score_date DESC, id DESC
		LIMIT 1
	`, code); err != nil || !score.Valid {
		return 50
	}
	return score.Float64
}

func peerRankScore(db *sqlx.DB, code string) float64 {
	type peerRankRow struct {
		RankValue  int `db:"rank_value"`
		TotalCount int `db:"total_count"`
	}
	var data peerRankRow
	if err := db.Get(&data, `
		SELECT rank_value, total_count
		FROM fund_peer_ranks
		WHERE code=? AND rank_value IS NOT NULL AND total_count IS NOT NULL AND total_count>0
		ORDER BY ranking_date DESC, id DESC
		LIMIT 1
	`, code); err != nil || data.TotalCount <= 0 || data.RankValue <= 0 {
		return 50
	}
	return (1 - float64(data.RankValue-1)/float64(data.TotalCount)) * 100
}

func fundName(db *sqlx.DB, code string) (string, error) {
	var name sql.NullString
	if err := db.Get(&name, `SELECT name FROM funds WHERE code=?`, code); err != nil {
		return "", err
	}
	if !name.Valid {
		return "", nil
	}
	return name.String, nil
}

func heldFundCodes(db *sqlx.DB) (map[string]struct{}, error) {
	var codes []string
	if err := db.Select(&codes, `
		SELECT code
		FROM holdings
		WHERE COALESCE(shares, 0) > 0
	`); err != nil {
		return nil, fmt.Errorf("query held funds: %w", err)
	}

	out := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		out[code] = struct{}{}
	}
	return out, nil
}

func latestFundIndustries(db *sqlx.DB, code string) ([]string, error) {
	fundCodeCol, err := chooseColumn(db, "fund_industry", "fund_code", "code")
	if err != nil {
		return nil, err
	}
	industryCol, err := chooseColumn(db, "fund_industry", "industry_name", "industry")
	if err != nil {
		return nil, err
	}

	var reportDate sql.NullString
	query := fmt.Sprintf(`SELECT MAX(report_date) FROM fund_industry WHERE %s=?`, fundCodeCol)
	if err := db.Get(&reportDate, query, code); err != nil {
		return nil, err
	}
	if !reportDate.Valid || reportDate.String == "" {
		return nil, nil
	}

	query = fmt.Sprintf(`
		SELECT %s
		FROM fund_industry
		WHERE %s=? AND report_date=?
		ORDER BY COALESCE(weight, 0) DESC, %s ASC
	`, industryCol, fundCodeCol, industryCol)
	var industries []string
	if err := db.Select(&industries, query, code, reportDate.String); err != nil {
		return nil, err
	}
	return industries, nil
}

func latestFundIndustryCodes(db *sqlx.DB, code string) ([]string, error) {
	industries, err := latestFundIndustries(db, code)
	if err != nil {
		return nil, err
	}
	return industries, nil
}

func peerFundsByIndustry(db *sqlx.DB, seedCode string, industries []string, limit int) ([]string, error) {
	fundCodeCol, err := chooseColumn(db, "fund_industry", "fund_code", "code")
	if err != nil {
		return nil, err
	}
	industryCol, err := chooseColumn(db, "fund_industry", "industry_name", "industry")
	if err != nil {
		return nil, err
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(industries)), ",")
	query := fmt.Sprintf(`
		SELECT %s
		FROM fund_industry
		WHERE %s<>?
		  AND %s IN (`+placeholders+`)
		GROUP BY %s
		ORDER BY COUNT(*) DESC, %s ASC
		LIMIT ?
	`, fundCodeCol, fundCodeCol, industryCol, fundCodeCol, fundCodeCol)

	args := make([]any, 0, len(industries)+2)
	args = append(args, seedCode)
	for _, industry := range industries {
		args = append(args, industry)
	}
	args = append(args, limit)

	var rows []string
	if err := db.Select(&rows, query, args...); err != nil {
		return nil, err
	}
	return rows, nil
}

func chooseColumn(db *sqlx.DB, table string, candidates ...string) (string, error) {
	type columnInfo struct {
		Name string `db:"name"`
	}

	var columns []columnInfo
	if err := db.Select(&columns, fmt.Sprintf("PRAGMA table_info(%s)", table)); err != nil {
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

func filterCodes(values []string, keep func(string) bool) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if keep(value) {
			out = append(out, value)
		}
	}
	return out
}

func candidateMinAUM(answers UserAnswers) float64 {
	switch strings.ToLower(answers.RiskTolerance) {
	case "low", "conservative":
		return 50
	case "high", "aggressive":
		return 10
	default:
		return 20
	}
}

func gapPriority(deficit float64) int {
	switch {
	case deficit >= 15:
		return 5
	case deficit >= 10:
		return 4
	case deficit >= 5:
		return 3
	case deficit >= 2:
		return 2
	default:
		return 1
	}
}
