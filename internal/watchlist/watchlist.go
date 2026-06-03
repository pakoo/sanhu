package watchlist

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
)

type WatchlistItem struct {
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	Category   string    `json:"category"`
	LatestNav  float64   `json:"latest_nav"`
	Return7d   float64   `json:"return_7d"`
	Return1m   float64   `json:"return_1m"`
	TotalScore float64   `json:"total_score"`
	Spark30    []float64 `json:"spark30"`
}

func GetWatchlist(db *sqlx.DB) ([]WatchlistItem, error) {
	type row struct {
		Code string `db:"code"`
		Name string `db:"name"`
		Type string `db:"fund_type"`
	}

	var rows []row
	if err := db.Select(&rows, `
		SELECT w.code,
		       COALESCE(f.name, '') AS name,
		       COALESCE(f.fund_type, '') AS fund_type
		FROM watchlist w
		LEFT JOIN funds f ON f.code = w.code
		ORDER BY w.added_at DESC, w.id DESC
	`); err != nil {
		return nil, fmt.Errorf("query watchlist: %w", err)
	}

	items := make([]WatchlistItem, 0, len(rows))
	for _, r := range rows {
		latestNav, latestDate, _ := latestNavAndDate(db, r.Code)
		score, _ := latestScore(db, r.Code)
		item := WatchlistItem{
			Code:       r.Code,
			Name:       r.Name,
			Category:   r.Type,
			LatestNav:  latestNav,
			Return7d:   trailingReturn(db, r.Code, latestDate, 7),
			Return1m:   trailingReturn(db, r.Code, latestDate, 30),
			TotalScore: score,
			Spark30:    sparkline30(db, r.Code),
		}
		items = append(items, item)
	}

	return items, nil
}

func AddToWatchlist(db *sqlx.DB, code string) error {
	if code == "" {
		return fmt.Errorf("code is required")
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO watchlist (code) VALUES (?)`, code)
	return err
}

func RemoveFromWatchlist(db *sqlx.DB, code string) error {
	if code == "" {
		return fmt.Errorf("code is required")
	}
	_, err := db.Exec(`DELETE FROM watchlist WHERE code=?`, code)
	return err
}

func BulkAddToWatchlist(db *sqlx.DB, codes []string) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, code := range codes {
		if code == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO watchlist (code) VALUES (?)`, code); err != nil {
			return fmt.Errorf("insert watchlist %s: %w", code, err)
		}
	}

	return tx.Commit()
}

func latestNavAndDate(db *sqlx.DB, code string) (float64, string, error) {
	type row struct {
		Date string  `db:"date"`
		Nav  float64 `db:"nav"`
	}

	var r row
	if err := db.Get(&r, `
		SELECT date, nav
		FROM nav_history
		WHERE code=? AND nav>0
		ORDER BY date DESC
		LIMIT 1
	`, code); err != nil {
		if err == sql.ErrNoRows {
			return 0, "", nil
		}
		return 0, "", err
	}
	return r.Nav, r.Date, nil
}

func latestScore(db *sqlx.DB, code string) (float64, error) {
	var score sql.NullFloat64
	if err := db.Get(&score, `
		SELECT total_score
		FROM fund_scores
		WHERE code=?
		ORDER BY score_date DESC, id DESC
		LIMIT 1
	`, code); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	if !score.Valid {
		return 0, nil
	}
	return score.Float64, nil
}

func trailingReturn(db *sqlx.DB, code, latestDate string, days int) float64 {
	if latestDate == "" {
		return 0
	}

	latestTime, err := time.Parse("2006-01-02", latestDate)
	if err != nil {
		return 0
	}
	cutoff := latestTime.AddDate(0, 0, -days).Format("2006-01-02")

	var base sql.NullFloat64
	if err := db.Get(&base, `
		SELECT nav
		FROM nav_history
		WHERE code=? AND date>=? AND nav>0
		ORDER BY date ASC
		LIMIT 1
	`, code, cutoff); err != nil || !base.Valid || base.Float64 == 0 {
		return 0
	}

	latestNav, _, err := latestNavAndDate(db, code)
	if err != nil || latestNav == 0 {
		return 0
	}

	return (latestNav - base.Float64) / base.Float64 * 100
}

func sparkline30(db *sqlx.DB, code string) []float64 {
	type row struct {
		Date string  `db:"date"`
		Nav  float64 `db:"nav"`
	}

	var rows []row
	if err := db.Select(&rows, `
		SELECT date, nav
		FROM nav_history
		WHERE code=? AND nav>0
		ORDER BY date DESC
		LIMIT 30
	`, code); err != nil || len(rows) == 0 {
		return nil
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Date < rows[j].Date
	})

	base := rows[0].Nav
	if base == 0 {
		return nil
	}

	curve := make([]float64, 0, len(rows))
	for _, r := range rows {
		curve = append(curve, math.Round(r.Nav/base*1000)/1000)
	}
	return curve
}
