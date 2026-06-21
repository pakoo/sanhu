package watchlist

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
)

// WatchlistItem 与 Python get_watchlist 输出对齐
type WatchlistItem struct {
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	Category     string    `json:"category"`
	AddedAt      string    `json:"added_at"`
	IsHolding    bool      `json:"is_holding"`
	CurrentValue float64   `json:"current_value"`
	LatestNav    *float64  `json:"latest_nav"`
	DailyReturn  *float64  `json:"daily_return"`
	Ret7d        *float64  `json:"ret_7d"`
	Ret1m        *float64  `json:"ret_1m"`
	Sparkline    []float64 `json:"sparkline"`
	TotalScore   *float64  `json:"total_score"`
}

// GetWatchlist 复刻 Python get_watchlist：name/category 取自 funds，
// ret_7d/ret_1m 用 acc_nav OFFSET 7/21 比值，sparkline 取最近 30 个 nav 正序。
func GetWatchlist(db *sqlx.DB) ([]WatchlistItem, error) {
	type wrow struct {
		Code    string `db:"code"`
		AddedAt string `db:"added_at"`
	}
	var rows []wrow
	if err := db.Select(&rows, `SELECT code, COALESCE(added_at,'') AS added_at FROM watchlist ORDER BY added_at DESC`); err != nil {
		return nil, fmt.Errorf("query watchlist: %w", err)
	}

	items := make([]WatchlistItem, 0, len(rows))
	for _, r := range rows {
		code := r.Code
		var fund struct {
			Name     sql.NullString `db:"name"`
			Category sql.NullString `db:"category"`
		}
		_ = db.Get(&fund, `SELECT name, category FROM funds WHERE code=?`, code)
		name := code
		if fund.Name.Valid && fund.Name.String != "" {
			name = fund.Name.String
		}

		var shares sql.NullFloat64
		isHolding := false
		if err := db.Get(&shares, `SELECT shares FROM holdings WHERE code=?`, code); err == nil {
			isHolding = true
		}

		// 最近2行 nav（含 acc_nav / daily_return）
		type navRow struct {
			Nav         sql.NullFloat64 `db:"nav"`
			AccNav      sql.NullFloat64 `db:"acc_nav"`
			DailyReturn sql.NullFloat64 `db:"daily_return"`
		}
		var navRows []navRow
		_ = db.Select(&navRows, `SELECT nav, acc_nav, daily_return FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 2`, code)

		var latestNav, dailyReturn *float64
		currentValue := 0.0
		var latestAcc sql.NullFloat64
		if len(navRows) > 0 {
			if navRows[0].Nav.Valid {
				v := math.Round(navRows[0].Nav.Float64*10000) / 10000
				latestNav = &v
			}
			if navRows[0].DailyReturn.Valid {
				v := math.Round(navRows[0].DailyReturn.Float64*10000) / 10000
				dailyReturn = &v
			}
			latestAcc = navRows[0].AccNav
			if isHolding && latestNav != nil {
				currentValue = shares.Float64 * navRows[0].Nav.Float64
			}
		}

		ret7d := offsetReturn(db, code, latestAcc, 7)
		ret1m := offsetReturn(db, code, latestAcc, 21)

		// sparkline：最近30个 nav，正序
		var sparkDesc []float64
		_ = db.Select(&sparkDesc, `SELECT nav FROM nav_history WHERE code=? AND nav IS NOT NULL ORDER BY date DESC LIMIT 30`, code)
		sparkline := make([]float64, 0, len(sparkDesc))
		for i := len(sparkDesc) - 1; i >= 0; i-- {
			sparkline = append(sparkline, sparkDesc[i])
		}

		var totalScore *float64
		var sc sql.NullFloat64
		if err := db.Get(&sc, `SELECT total_score FROM fund_scores WHERE code=? ORDER BY date DESC LIMIT 1`, code); err == nil && sc.Valid {
			v := math.Round(sc.Float64*10) / 10
			totalScore = &v
		}

		cat := ""
		if fund.Category.Valid {
			cat = fund.Category.String
		}
		items = append(items, WatchlistItem{
			Code: code, Name: name, Category: cat, AddedAt: r.AddedAt,
			IsHolding: isHolding, CurrentValue: math.Round(currentValue*100) / 100,
			LatestNav: latestNav, DailyReturn: dailyReturn, Ret7d: ret7d, Ret1m: ret1m,
			Sparkline: sparkline, TotalScore: totalScore,
		})
	}
	return items, nil
}

// offsetReturn = (最新 acc_nav / OFFSET 处 acc_nav - 1) * 100，round 2
func offsetReturn(db *sqlx.DB, code string, latestAcc sql.NullFloat64, offset int) *float64 {
	if !latestAcc.Valid || latestAcc.Float64 == 0 {
		return nil
	}
	var prev sql.NullFloat64
	if err := db.Get(&prev, `SELECT acc_nav FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 1 OFFSET ?`, code, offset); err != nil || !prev.Valid || prev.Float64 == 0 {
		return nil
	}
	v := math.Round((latestAcc.Float64/prev.Float64-1)*100*100) / 100
	return &v
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
