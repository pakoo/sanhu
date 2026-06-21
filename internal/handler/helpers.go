package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/rebalance"
	"github.com/pakoo/sanhu/internal/risk"
)

type portfolioHolding struct {
	Code       string
	Name       string
	Shares     float64
	Value      float64
	CostAmount float64
	Category   string
}

func parseIntDefault(raw string, fallback int) int {
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func parseInt64Param(raw string) (int64, error) {
	return strconv.ParseInt(raw, 10, 64)
}

func parseFloat64(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	default:
		return 0
	}
}

func tableColumns(db *sqlx.DB, table string) (map[string]struct{}, error) {
	// PRAGMA table_info 返回 cid/name/type/notnull/dflt_value/pk 六列
	// dflt_value 可能为 NULL，用 sql.NullString
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
		return nil, err
	}
	out := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		out[strings.ToLower(row.Name)] = struct{}{}
	}
	return out, nil
}

func chooseColumn(db *sqlx.DB, table string, candidates ...string) (string, error) {
	cols, err := tableColumns(db, table)
	if err != nil {
		return "", err
	}
	for _, candidate := range candidates {
		if _, ok := cols[strings.ToLower(candidate)]; ok {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no matching column for %s", table)
}

func hasColumn(cols map[string]struct{}, name string) bool {
	_, ok := cols[strings.ToLower(name)]
	return ok
}

func latestNavValue(db *sqlx.DB, code string) float64 {
	for _, col := range []string{"nav", "pe"} {
		_ = col
	}
	var nav float64
	if err := db.Get(&nav, `SELECT nav FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 1`, code); err == nil {
		return nav
	}
	return 0
}

func fundCategoryMap(db *sqlx.DB) map[string]string {
	cols, err := tableColumns(db, "funds")
	if err != nil || !hasColumn(cols, "code") {
		return map[string]string{}
	}

	categoryExpr := `'mixed'`
	if hasColumn(cols, "category") {
		categoryExpr = "COALESCE(category, 'mixed')"
	} else if hasColumn(cols, "fund_type") {
		categoryExpr = "CASE " +
			"WHEN fund_type LIKE '%债%' THEN 'bond' " +
			"WHEN fund_type LIKE '%货币%' THEN 'money' " +
			"WHEN fund_type LIKE '%QDII%' THEN 'qdii' " +
			"WHEN fund_type LIKE '%股票%' OR fund_type LIKE '%指数%' THEN 'equity' " +
			"ELSE 'mixed' END"
	}

	type row struct {
		Code     string `db:"code"`
		Category string `db:"category"`
	}
	var rows []row
	query := fmt.Sprintf("SELECT code, %s AS category FROM funds", categoryExpr)
	if err := db.Select(&rows, query); err != nil {
		return map[string]string{}
	}

	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Code] = row.Category
	}
	return out
}

func loadPortfolioHoldings(db *sqlx.DB) ([]portfolioHolding, float64, error) {
	cols, err := tableColumns(db, "holdings")
	if err != nil {
		return nil, 0, err
	}

	// holdings 表无 name 列时从 funds 取（与 Python get_portfolio_summary 的 JOIN funds 对齐）
	nameExpr := "COALESCE((SELECT name FROM funds WHERE funds.code=holdings.code), '')"
	if hasColumn(cols, "name") {
		nameExpr = "COALESCE(NULLIF(name,''), (SELECT name FROM funds WHERE funds.code=holdings.code), '')"
	}
	sharesExpr := "0"
	if hasColumn(cols, "shares") {
		sharesExpr = "COALESCE(shares, 0)"
	}
	valueExpr := "0"
	if hasColumn(cols, "market_value") {
		valueExpr = "COALESCE(market_value, 0)"
	}
	costExpr := "0"
	switch {
	case hasColumn(cols, "cost_amount"):
		costExpr = "COALESCE(cost_amount, 0)"
	case hasColumn(cols, "cost_basis"):
		costExpr = "COALESCE(cost_basis, 0)"
	}

	type row struct {
		Code       string  `db:"code"`
		Name       string  `db:"name"`
		Shares     float64 `db:"shares"`
		Value      float64 `db:"market_value"`
		CostAmount float64 `db:"cost_amount"`
	}

	var rows []row
	query := fmt.Sprintf(`
		SELECT code, %s AS name, %s AS shares, %s AS market_value, %s AS cost_amount
		FROM holdings
	`, nameExpr, sharesExpr, valueExpr, costExpr)
	if err := db.Select(&rows, query); err != nil {
		return nil, 0, err
	}

	categories := fundCategoryMap(db)
	holdings := make([]portfolioHolding, 0, len(rows))
	total := 0.0
	for _, row := range rows {
		value := row.Value
		if value <= 0 && row.Shares > 0 {
			value = latestNavValue(db, row.Code) * row.Shares
		}
		// 与 Python 基准对齐：仅持有中(shares>0)且净值缺失时才用成本兜底；
		// 清仓持仓(shares=0)当前市值为 0，保留(value=0)——Python 同样保留，
		// 用于回撤/波动/亏损告警等明细（权重为 0，不影响组合占比）。
		if value <= 0 && row.Shares > 0 && row.CostAmount > 0 {
			value = row.CostAmount
		}
		if row.Code == "" {
			continue
		}
		item := portfolioHolding{
			Code:       row.Code,
			Name:       row.Name,
			Shares:     row.Shares,
			Value:      value,
			CostAmount: row.CostAmount,
			Category:   categories[row.Code],
		}
		if item.Category == "" {
			item.Category = "mixed"
		}
		holdings = append(holdings, item)
		total += item.Value
	}
	return holdings, total, nil
}

func toRiskInputs(items []portfolioHolding) []risk.HoldingInput {
	out := make([]risk.HoldingInput, 0, len(items))
	for _, item := range items {
		profitRate := 0.0
		if item.CostAmount > 0 {
			profitRate = (item.Value - item.CostAmount) / item.CostAmount * 100
		}
		out = append(out, risk.HoldingInput{
			Code:       item.Code,
			Name:       item.Name,
			Value:      item.Value,
			Category:   item.Category,
			ProfitRate: profitRate,
		})
	}
	return out
}

func toRebalanceInputs(items []portfolioHolding) []rebalance.HoldingInput {
	out := make([]rebalance.HoldingInput, 0, len(items))
	for _, item := range items {
		out = append(out, rebalance.HoldingInput{
			Code:     item.Code,
			Value:    item.Value,
			Category: item.Category,
		})
	}
	return out
}

func holdingsTaskSnapshot(task *taskState) map[string]interface{} {
	task.mu.Lock()
	defer task.mu.Unlock()
	return map[string]interface{}{
		"running":         task.Running,
		"done":            task.Done,
		"logs":            append([]string(nil), task.Logs...),
		"done_count":      task.DoneCount,
		"total":           task.Total,
		"errors":          append([]string(nil), task.Errors...),
		"recent":          append([]string(nil), task.Recent...),
		"last_refresh_at": task.LastRefreshAt,
	}
}

func startTask(task *taskState) bool {
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.Running {
		return false
	}
	task.Running = true
	task.Done = false
	task.Logs = nil
	task.DoneCount = 0
	task.Total = 0
	task.Errors = nil
	task.Recent = nil
	return true
}

func finishTask(task *taskState, total int, err error) {
	task.mu.Lock()
	defer task.mu.Unlock()
	task.Total = total
	task.DoneCount = total
	task.Running = false
	task.Done = true
	task.LastRefreshAt = time.Now().Format("2006-01-02 15:04:05")
	if err != nil {
		task.Errors = append(task.Errors, err.Error())
		task.Logs = append(task.Logs, err.Error())
	} else {
		task.Logs = append(task.Logs, "done")
	}
}

func loadKVSettings(db *sqlx.DB) (map[string]string, error) {
	type row struct {
		Key   string `db:"key"`
		Value string `db:"value"`
	}

	var rows []row
	if err := db.Select(&rows, `SELECT key, value FROM user_settings`); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Key] = row.Value
	}
	return out, nil
}

func getSettingsCompat(db *sqlx.DB) (map[string]string, error) {
	cols, err := tableColumns(db, "user_settings")
	if err != nil {
		return map[string]string{
			"risk_level":       "moderate",
			"ocrspace_api_key": "",
		}, nil
	}

	if (hasColumn(cols, "setting_key") && hasColumn(cols, "setting_value")) ||
		(hasColumn(cols, "key") && hasColumn(cols, "value")) {
		values, err := loadKVSettings(db)
		if err != nil {
			return nil, err
		}
		if values["risk_level"] == "" {
			values["risk_level"] = "moderate"
		}
		if _, ok := values["ocrspace_api_key"]; !ok {
			values["ocrspace_api_key"] = ""
		}
		return values, nil
	}

	type row struct {
		RiskLevel string `db:"risk_level"`
		OcrKey    string `db:"ocrspace_api_key"`
	}
	var data row
	err = db.Get(&data, `SELECT COALESCE(risk_level,'moderate') AS risk_level, COALESCE(ocrspace_api_key,'') AS ocrspace_api_key FROM user_settings LIMIT 1`)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if data.RiskLevel == "" {
		data.RiskLevel = "moderate"
	}
	return map[string]string{
		"risk_level":       data.RiskLevel,
		"ocrspace_api_key": data.OcrKey,
	}, nil
}

func saveSettingsCompat(db *sqlx.DB, values map[string]string) error {
	cols, err := tableColumns(db, "user_settings")
	if err != nil {
		return err
	}

	kvMode := (hasColumn(cols, "setting_key") && hasColumn(cols, "setting_value")) ||
		(hasColumn(cols, "key") && hasColumn(cols, "value"))
	if kvMode {
		keyCol, valCol := "setting_key", "setting_value"
		if hasColumn(cols, "key") {
			keyCol, valCol = "key", "value"
		}
		tx, err := db.Beginx()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, key := range []string{"risk_level", "ocrspace_api_key"} {
			if _, err := tx.Exec(fmt.Sprintf(`
				INSERT INTO user_settings (%s, %s)
				VALUES (?, ?)
				ON CONFLICT(%s) DO UPDATE SET %s=excluded.%s
			`, keyCol, valCol, keyCol, valCol, valCol), key, values[key]); err != nil {
				return err
			}
		}
		return tx.Commit()
	}

	_, err = db.Exec(`
		INSERT OR REPLACE INTO user_settings (id, risk_level, ocrspace_api_key)
		VALUES (1, ?, ?)
	`, values["risk_level"], values["ocrspace_api_key"])
	return err
}

func jsonString(raw interface{}) (string, error) {
	switch v := raw.(type) {
	case nil:
		return "", nil
	case string:
		if v == "" {
			return "", nil
		}
		if json.Valid([]byte(v)) {
			return v, nil
		}
		data, err := json.Marshal(v)
		return string(data), err
	default:
		data, err := json.Marshal(v)
		return string(data), err
	}
}

func pearson(xs, ys []float64) float64 {
	if len(xs) == 0 || len(xs) != len(ys) {
		return 0
	}

	var sumX, sumY float64
	for i := range xs {
		sumX += xs[i]
		sumY += ys[i]
	}
	meanX := sumX / float64(len(xs))
	meanY := sumY / float64(len(ys))

	var num, denX, denY float64
	for i := range xs {
		dx := xs[i] - meanX
		dy := ys[i] - meanY
		num += dx * dy
		denX += dx * dx
		denY += dy * dy
	}
	if denX == 0 || denY == 0 {
		return 0
	}
	return num / math.Sqrt(denX*denY)
}

func dailyReturnSeries(db *sqlx.DB, code string, days int) (map[string]float64, error) {
	type row struct {
		Date string  `db:"date"`
		Nav  float64 `db:"nav"`
	}
	var rows []row
	cutoff := time.Now().AddDate(0, 0, -days-5).Format("2006-01-02")
	if err := db.Select(&rows, `
		SELECT date, nav
		FROM nav_history
		WHERE code=? AND date>=? AND nav>0
		ORDER BY date ASC
	`, code, cutoff); err != nil {
		return nil, err
	}

	out := map[string]float64{}
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Nav <= 0 {
			continue
		}
		out[rows[i].Date] = (rows[i].Nav - rows[i-1].Nav) / rows[i-1].Nav
	}
	return out, nil
}

func buildMarkdown(title string, lines []string) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")
	for _, line := range lines {
		b.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func sortedKeys[K ~string, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
