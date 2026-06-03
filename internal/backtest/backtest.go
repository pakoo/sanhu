package backtest

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
)

type BacktestTrade struct {
	Date   string  `json:"date"`
	Action string  `json:"action"`
	Amount float64 `json:"amount"`
	Nav    float64 `json:"nav"`
	Shares float64 `json:"shares"`
}

type CurvePoint struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

type BacktestResult struct {
	TotalInvested float64         `json:"total_invested"`
	FinalValue    float64         `json:"final_value"`
	TotalReturn   float64         `json:"total_return"`
	AnnualReturn  float64         `json:"annual_return"`
	MaxDrawdown   float64         `json:"max_drawdown"`
	Trades        []BacktestTrade `json:"trades"`
	Curve         []CurvePoint    `json:"curve"`
}

func DcaBacktest(db *sqlx.DB, code string, amount float64, frequency, startDate, endDate string) (*BacktestResult, error) {
	return runBacktest(db, code, amount, frequency, 0, 0, startDate, endDate)
}

func TakeProfitBacktest(db *sqlx.DB, code string, amount float64, frequency string, tpPct, slPct float64, startDate, endDate string) (*BacktestResult, error) {
	return runBacktest(db, code, amount, frequency, tpPct, slPct, startDate, endDate)
}

type navRow struct {
	Date string  `db:"date"`
	Nav  float64 `db:"nav"`
}

func runBacktest(db *sqlx.DB, code string, amount float64, frequency string, tpPct, slPct float64, startDate, endDate string) (*BacktestResult, error) {
	if code == "" {
		return nil, fmt.Errorf("code is required")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}

	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil, fmt.Errorf("invalid start date: %w", err)
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return nil, fmt.Errorf("invalid end date: %w", err)
	}
	if end.Before(start) {
		return nil, fmt.Errorf("end date before start date")
	}

	var navs []navRow
	if err := db.Select(&navs, `
		SELECT date, nav
		FROM nav_history
		WHERE code=? AND date>=? AND date<=? AND nav>0
		ORDER BY date ASC
	`, code, startDate, endDate); err != nil {
		return nil, fmt.Errorf("query nav history: %w", err)
	}
	if len(navs) == 0 {
		return &BacktestResult{}, nil
	}

	scheduleDates, err := generateScheduleDates(start, end, frequency)
	if err != nil {
		return nil, err
	}
	buyPlan := map[string]int{}
	for _, scheduleDate := range scheduleDates {
		if buyDate, ok := nearestNavDate(navs, scheduleDate); ok {
			buyPlan[buyDate]++
		}
	}

	result := &BacktestResult{}
	shares := 0.0
	openCost := 0.0
	realizedCash := 0.0
	peakValue := 0.0
	firstTradeDate := ""

	for _, nav := range navs {
		for i := 0; i < buyPlan[nav.Date]; i++ {
			boughtShares := amount / nav.Nav
			shares += boughtShares
			openCost += amount
			result.TotalInvested += amount
			result.Trades = append(result.Trades, BacktestTrade{
				Date:   nav.Date,
				Action: "buy",
				Amount: amount,
				Nav:    nav.Nav,
				Shares: boughtShares,
			})
			if firstTradeDate == "" {
				firstTradeDate = nav.Date
			}
		}

		if shares > 0 && openCost > 0 && (tpPct > 0 || slPct > 0) {
			positionValue := shares * nav.Nav
			pnlPct := (positionValue - openCost) / openCost
			switch {
			case tpPct > 0 && pnlPct >= tpPct:
				result.Trades = append(result.Trades, BacktestTrade{
					Date:   nav.Date,
					Action: "sell_tp",
					Amount: positionValue,
					Nav:    nav.Nav,
					Shares: shares,
				})
				realizedCash += positionValue
				shares = 0
				openCost = 0
			case slPct > 0 && pnlPct <= -slPct:
				result.Trades = append(result.Trades, BacktestTrade{
					Date:   nav.Date,
					Action: "sell_sl",
					Amount: positionValue,
					Nav:    nav.Nav,
					Shares: shares,
				})
				realizedCash += positionValue
				shares = 0
				openCost = 0
			}
		}

		value := realizedCash + shares*nav.Nav
		result.Curve = append(result.Curve, CurvePoint{
			Date:  nav.Date,
			Value: value,
		})
		if value > peakValue {
			peakValue = value
		}
		if peakValue > 0 {
			drawdown := (peakValue - value) / peakValue * 100
			if drawdown > result.MaxDrawdown {
				result.MaxDrawdown = drawdown
			}
		}
	}

	lastNav := navs[len(navs)-1].Nav
	result.FinalValue = realizedCash + shares*lastNav
	if result.TotalInvested > 0 {
		result.TotalReturn = (result.FinalValue - result.TotalInvested) / result.TotalInvested * 100
	}
	if firstTradeDate != "" && result.FinalValue > 0 && result.TotalInvested > 0 {
		firstTradeTime, _ := time.Parse("2006-01-02", firstTradeDate)
		years := end.Sub(firstTradeTime).Hours() / 24 / 365
		if years > 0 {
			result.AnnualReturn = (math.Pow(result.FinalValue/result.TotalInvested, 1/years) - 1) * 100
		}
	}

	return result, nil
}

func generateScheduleDates(start, end time.Time, frequency string) ([]string, error) {
	current := start
	out := []string{}

	for !current.After(end) {
		out = append(out, current.Format("2006-01-02"))
		switch frequency {
		case "weekly":
			current = current.AddDate(0, 0, 7)
		case "biweekly":
			current = current.AddDate(0, 0, 14)
		case "monthly":
			current = current.AddDate(0, 1, 0)
		default:
			return nil, fmt.Errorf("unsupported frequency: %s", frequency)
		}
	}

	return out, nil
}

func nearestNavDate(rows []navRow, targetDate string) (string, bool) {
	idx := sort.Search(len(rows), func(i int) bool {
		return rows[i].Date >= targetDate
	})
	if idx >= len(rows) {
		return "", false
	}
	return rows[idx].Date, true
}
