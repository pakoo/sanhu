package decisions

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type Decision struct {
	ID              int64           `db:"id"                json:"id"`
	CreatedAt       string          `db:"created_at"        json:"created_at"`
	DecisionType    string          `db:"decision_type"     json:"decision_type"`
	Code            string          `db:"code"              json:"code"`
	TargetAmount    sql.NullFloat64 `db:"target_amount"     json:"target_amount"`
	TargetNavMax    sql.NullFloat64 `db:"target_nav_max"    json:"target_nav_max"`
	TargetTPPct     sql.NullFloat64 `db:"target_tp_pct"     json:"target_tp_pct"`
	TargetSLPct     sql.NullFloat64 `db:"target_sl_pct"     json:"target_sl_pct"`
	RationaleJSON   sql.NullString  `db:"rationale_json"    json:"rationale_json"`
	SourceSessionID sql.NullString  `db:"source_session_id" json:"source_session_id"`
	TransactionID   sql.NullInt64   `db:"transaction_id"    json:"transaction_id"`
	Status          string          `db:"status"            json:"status"`
	ExecutedAt      sql.NullString  `db:"executed_at"       json:"executed_at"`
	ClosedAt        sql.NullString  `db:"closed_at"         json:"closed_at"`
	ClosingPnLPct   sql.NullFloat64 `db:"closing_pnl_pct"   json:"closing_pnl_pct"`
	Notes           sql.NullString  `db:"notes"             json:"notes"`
	IsVirtual       int             `db:"is_virtual"        json:"is_virtual"`
	VirtualEntryNav sql.NullFloat64 `db:"virtual_entry_nav" json:"virtual_entry_nav"`
}

type CurvePoint struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

type DecisionPerf struct {
	EntryNav   float64      `json:"entry_nav"`
	CurrentNav float64      `json:"current_nav"`
	PnLPct     float64      `json:"pnl_pct"`
	Curve      []CurvePoint `json:"curve"`
}

type CreatePayload struct {
	Code         string  `json:"code"`
	Type         string  `json:"type"`
	Notes        string  `json:"notes"`
	TargetAmount float64 `json:"target_amount"`
	EntryNavHint float64 `json:"entry_nav_hint"`
	TargetTPPct  float64 `json:"target_tp_pct"`
	TargetSLPct  float64 `json:"target_sl_pct"`
	IsVirtual    bool    `json:"is_virtual"`
}

func CreateDecision(db *sqlx.DB, p CreatePayload) (int64, error) {
	if p.Code == "" {
		return 0, fmt.Errorf("code is required")
	}
	if p.Type == "" {
		return 0, fmt.Errorf("type is required")
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	status := "pending"

	var virtualEntryNav *float64
	if p.IsVirtual {
		nav, err := latestNav(db, p.Code)
		if err != nil && err != sql.ErrNoRows {
			return 0, fmt.Errorf("query latest nav: %w", err)
		}
		if nav <= 0 && p.EntryNavHint > 0 {
			nav = p.EntryNavHint
		}
		if nav <= 0 {
			return 0, fmt.Errorf("latest nav not found for virtual decision")
		}
		status = "executed"
		virtualEntryNav = &nav
	}

	var targetAmount, targetNavMax, targetTPPct, targetSLPct *float64
	if p.TargetAmount > 0 {
		targetAmount = &p.TargetAmount
	}
	if p.EntryNavHint > 0 {
		targetNavMax = &p.EntryNavHint
	}
	if p.TargetTPPct > 0 {
		targetTPPct = &p.TargetTPPct
	}
	if p.TargetSLPct > 0 {
		targetSLPct = &p.TargetSLPct
	}

	var notes *string
	if p.Notes != "" {
		notes = &p.Notes
	}

	isVirtual := 0
	if p.IsVirtual {
		isVirtual = 1
	}

	res, err := db.Exec(`
		INSERT INTO decisions
			(created_at, decision_type, code, target_amount, target_nav_max,
			 target_tp_pct, target_sl_pct, notes, status, is_virtual, virtual_entry_nav)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, now, p.Type, p.Code,
		targetAmount, targetNavMax, targetTPPct, targetSLPct,
		notes, status, isVirtual, virtualEntryNav)
	if err != nil {
		return 0, fmt.Errorf("insert decision: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

func LinkTransaction(db *sqlx.DB, decisionID, txID int64) error {
	var decision Decision
	if err := db.Get(&decision, `SELECT id, code, status FROM decisions WHERE id=?`, decisionID); err != nil {
		return fmt.Errorf("query decision %d: %w", decisionID, err)
	}

	type txRow struct {
		Code  string  `db:"code"`
		Price float64 `db:"price"`
	}
	var tx txRow
	if err := db.Get(&tx, `SELECT code, price FROM transactions WHERE id=?`, txID); err != nil {
		return fmt.Errorf("query transaction %d: %w", txID, err)
	}
	if tx.Code != decision.Code {
		return fmt.Errorf("transaction %d does not belong to decision fund %s", txID, decision.Code)
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	var entryNav *float64
	if tx.Price > 0 {
		entryNav = &tx.Price
	}

	_, err := db.Exec(`
		UPDATE decisions
		SET status='executed', transaction_id=?, executed_at=?, virtual_entry_nav=COALESCE(virtual_entry_nav, ?)
		WHERE id=?
	`, txID, now, entryNav, decisionID)
	return err
}

const decisionSelectCols = `
	id, created_at, decision_type, code,
	target_amount, target_nav_max, target_tp_pct, target_sl_pct,
	rationale_json, source_session_id, transaction_id,
	status, executed_at, closed_at, closing_pnl_pct,
	notes, is_virtual, virtual_entry_nav
`

func ListDecisions(db *sqlx.DB, status, kind string, limit int) ([]Decision, error) {
	query := "SELECT " + decisionSelectCols + " FROM decisions"

	args := make([]any, 0, 8)
	clauses := make([]string, 0, 2)

	if values := splitFilter(status); len(values) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(values)), ",")
		clauses = append(clauses, "status IN ("+placeholders+")")
		for _, value := range values {
			args = append(args, value)
		}
	}

	if values := splitFilter(kind); len(values) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(values)), ",")
		clauses = append(clauses, "decision_type IN ("+placeholders+")")
		for _, value := range values {
			args = append(args, value)
		}
	}

	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC"

	if limit <= 0 {
		limit = 50
	}
	query += " LIMIT ?"
	args = append(args, limit)

	var decisions []Decision
	if err := db.Select(&decisions, query, args...); err != nil {
		return nil, fmt.Errorf("list decisions: %w", err)
	}
	return decisions, nil
}

func GetDecision(db *sqlx.DB, id int64) (*Decision, error) {
	var d Decision
	err := db.Get(&d, "SELECT "+decisionSelectCols+" FROM decisions WHERE id=?", id)
	if err != nil {
		return nil, fmt.Errorf("query decision %d: %w", id, err)
	}
	return &d, nil
}

func CancelDecision(db *sqlx.DB, id int64) error {
	now := time.Now().Format("2006-01-02 15:04:05")
	_, err := db.Exec(`UPDATE decisions SET status='cancelled', closed_at=? WHERE id=?`, now, id)
	return err
}

func CheckTPSLTriggers(db *sqlx.DB) error {
	var decisions []Decision
	if err := db.Select(&decisions,
		"SELECT "+decisionSelectCols+" FROM decisions WHERE status='executed'"); err != nil {
		return fmt.Errorf("query executed decisions: %w", err)
	}

	for _, d := range decisions {
		entryNav := d.VirtualEntryNav.Float64
		if entryNav <= 0 {
			continue
		}

		currentNav, err := latestNav(db, d.Code)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("query nav for %s: %w", d.Code, err)
		}
		if currentNav <= 0 {
			continue
		}

		changePct := (currentNav - entryNav) / entryNav

		nextStatus := ""
		switch {
		case d.TargetTPPct.Valid && d.TargetTPPct.Float64 > 0 && changePct >= d.TargetTPPct.Float64/100:
			nextStatus = "triggered_tp"
		case d.TargetSLPct.Valid && d.TargetSLPct.Float64 > 0 && changePct <= -d.TargetSLPct.Float64/100:
			nextStatus = "triggered_sl"
		default:
			continue
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		if _, err := db.Exec(`
			UPDATE decisions
			SET status=?, closed_at=?, closing_pnl_pct=?
			WHERE id=?
		`, nextStatus, now, changePct*100, d.ID); err != nil {
			return fmt.Errorf("update decision %d: %w", d.ID, err)
		}
	}

	return nil
}

func GetDecisionPerformance(db *sqlx.DB, id int64) (*DecisionPerf, error) {
	d, err := GetDecision(db, id)
	if err != nil {
		return nil, err
	}

	entryNav := d.VirtualEntryNav.Float64
	if entryNav <= 0 && d.TargetNavMax.Valid {
		entryNav = d.TargetNavMax.Float64
	}
	if entryNav <= 0 {
		entryNav, err = navOnOrBefore(db, d.Code, d.CreatedAt[:10])
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("query entry nav: %w", err)
		}
	}

	currentNav, err := latestNav(db, d.Code)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("query current nav: %w", err)
	}

	type row struct {
		Date string  `db:"date"`
		Nav  float64 `db:"nav"`
	}
	var rows []row
	startDate := d.CreatedAt
	if len(startDate) > 10 {
		startDate = startDate[:10]
	}
	if err := db.Select(&rows, `
		SELECT date, nav
		FROM nav_history
		WHERE code=? AND date>=? AND nav>0
		ORDER BY date ASC
	`, d.Code, startDate); err != nil {
		return nil, fmt.Errorf("query decision curve: %w", err)
	}

	curve := make([]CurvePoint, 0, len(rows))
	for _, row := range rows {
		curve = append(curve, CurvePoint{Date: row.Date, Value: row.Nav})
	}

	perf := &DecisionPerf{
		EntryNav:   entryNav,
		CurrentNav: currentNav,
		Curve:      curve,
	}
	if entryNav > 0 && currentNav > 0 {
		perf.PnLPct = (currentNav - entryNav) / entryNav * 100
	}

	return perf, nil
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

func navOnOrBefore(db *sqlx.DB, code, date string) (float64, error) {
	var nav float64
	err := db.Get(&nav, `
		SELECT nav
		FROM nav_history
		WHERE code=? AND date<=? AND nav>0
		ORDER BY date DESC
		LIMIT 1
	`, code, date)
	return nav, err
}

func splitFilter(raw string) []string {
	if raw == "" || raw == "all" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}
