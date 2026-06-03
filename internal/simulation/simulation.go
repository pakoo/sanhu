package simulation

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
)

// Simulation maps to the simulations table.
// Actual schema: id, name, strategy_name, params_json, fund_pool_json,
//   initial_capital, start_date, end_date, mode, status,
//   current_date, created_at, updated_at, notes
type Simulation struct {
	ID             int64   `db:"id"               json:"id"`
	Name           string  `db:"name"             json:"name"`
	StrategyName   string  `db:"strategy_name"    json:"strategy_name"`
	ParamsJSON     string  `db:"params_json"      json:"params_json"`
	FundPoolJSON   string  `db:"fund_pool_json"   json:"fund_pool_json"`
	InitialCapital float64 `db:"initial_capital"  json:"initial_cash"`
	StartDate      string  `db:"start_date"       json:"start_date"`
	EndDate        string  `db:"end_date"         json:"end_date"`
	Mode           string  `db:"mode"             json:"mode"`
	Status         string  `db:"status"           json:"status"`
	CurrentDate    string  `db:"current_date"     json:"current_date"`
	CreatedAt      string  `db:"created_at"       json:"created_at"`
	UpdatedAt      string  `db:"updated_at"       json:"updated_at"`
	Notes          string  `db:"notes"            json:"notes"`
}

// SimulationTrade maps to simulation_trades.
// Actual schema: id, sim_id, trade_date, code, action, shares, price, amount, reason
type SimulationTrade struct {
	ID        int64   `db:"id"         json:"id"`
	SimID     int64   `db:"sim_id"     json:"sim_id"`
	TradeDate string  `db:"trade_date" json:"trade_date"`
	Code      string  `db:"code"       json:"code"`
	Action    string  `db:"action"     json:"action"`
	Shares    float64 `db:"shares"     json:"shares"`
	Price     float64 `db:"price"      json:"price"`
	Amount    float64 `db:"amount"     json:"amount"`
	Reason    string  `db:"reason"     json:"reason"`
}

// SimulationSnapshot maps to simulation_snapshots.
// Actual schema: sim_id, date, cash, total_value, holdings_json
type SimulationSnapshot struct {
	SimID        int64   `db:"sim_id"       json:"sim_id"`
	Date         string  `db:"date"         json:"date"`
	Cash         float64 `db:"cash"         json:"cash"`
	TotalValue   float64 `db:"total_value"  json:"total_value"`
	HoldingsJSON string  `db:"holdings_json" json:"holdings_json"`
}

type SimulationDetail struct {
	Simulation
	Trades    []SimulationTrade    `json:"trades"`
	Snapshots []SimulationSnapshot `json:"snapshots"`
}

type simulationParams struct {
	MonthlyAmount float64 `json:"amount"`
}

type simulationSummary struct {
	FundCount      int     `json:"fund_count"`
	TradeCount     int     `json:"trade_count"`
	SnapshotCount  int     `json:"snapshot_count"`
	FinalCash      float64 `json:"final_cash"`
	FinalValue     float64 `json:"final_value"`
	PositionsValue float64 `json:"positions_value"`
}

type navPoint struct {
	Date string  `db:"date"`
	Nav  float64 `db:"nav"`
}

func CreateSimulation(db *sqlx.DB, strategyName, paramsJSON, fundPoolJSON, mode, startDate, endDate string, initialCapital, monthlyAmount float64) (int64, error) {
	// Merge monthlyAmount into paramsJSON if not already present
	if monthlyAmount > 0 && paramsJSON == "" {
		raw, _ := json.Marshal(simulationParams{MonthlyAmount: monthlyAmount})
		paramsJSON = string(raw)
	}

	name := strategyName
	if name == "" {
		name = "simulation"
	}
	if mode == "" {
		mode = "backtest"
	}

	res, err := db.Exec(`
		INSERT INTO simulations
			(name, strategy_name, params_json, fund_pool_json, initial_capital,
			 start_date, end_date, mode, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, name, strategyName, paramsJSON, fundPoolJSON, initialCapital,
		startDate, endDate, mode)
	if err != nil {
		return 0, fmt.Errorf("insert simulation: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

func RunSimulation(db *sqlx.DB, simID int64) error {
	sim, params, err := getSimulationConfig(db, simID)
	if err != nil {
		return err
	}
	trades, snapshots, summary, err := buildSimulation(db, sim, params, sim.EndDate)
	if err != nil {
		return err
	}
	return replaceSimulationRun(db, simID, sim.EndDate, trades, snapshots, summary)
}

func AdvanceForwardSimulation(db *sqlx.DB, simID int64, targetDate string) error {
	sim, params, err := getSimulationConfig(db, simID)
	if err != nil {
		return err
	}
	if sim.Mode != "forward" {
		return fmt.Errorf("simulation %d is not forward mode", simID)
	}
	if targetDate == "" {
		targetDate = time.Now().Format("2006-01-02")
	}
	trades, snapshots, summary, err := buildSimulation(db, sim, params, targetDate)
	if err != nil {
		return err
	}
	return replaceSimulationRun(db, simID, targetDate, trades, snapshots, summary)
}

func AdvanceAllForward(db *sqlx.DB) error {
	var ids []int64
	if err := db.Select(&ids, `SELECT id FROM simulations WHERE mode='forward' ORDER BY id ASC`); err != nil {
		return fmt.Errorf("list forward simulations: %w", err)
	}
	targetDate := time.Now().Format("2006-01-02")
	for _, id := range ids {
		if err := AdvanceForwardSimulation(db, id, targetDate); err != nil {
			return err
		}
	}
	return nil
}

const simSelectCols = `
	id, name, strategy_name,
	COALESCE(params_json, '') AS params_json,
	COALESCE(fund_pool_json, '') AS fund_pool_json,
	COALESCE(initial_capital, 0) AS initial_capital,
	COALESCE(start_date, '') AS start_date,
	COALESCE(end_date, '') AS end_date,
	mode, status,
	COALESCE(current_date, '') AS current_date,
	created_at, updated_at,
	COALESCE(notes, '') AS notes
`

func ListSimulations(db *sqlx.DB) ([]Simulation, error) {
	var items []Simulation
	if err := db.Select(&items,
		"SELECT "+simSelectCols+" FROM simulations ORDER BY updated_at DESC, id DESC"); err != nil {
		return nil, fmt.Errorf("list simulations: %w", err)
	}
	return items, nil
}

func GetSimulationDetail(db *sqlx.DB, id int64) (*SimulationDetail, error) {
	var sim Simulation
	if err := db.Get(&sim,
		"SELECT "+simSelectCols+" FROM simulations WHERE id=?", id); err != nil {
		return nil, fmt.Errorf("query simulation %d: %w", id, err)
	}

	detail := &SimulationDetail{Simulation: sim}
	if err := db.Select(&detail.Trades, `
		SELECT id, sim_id, trade_date, code, action, shares, price, amount, COALESCE(reason,'') AS reason
		FROM simulation_trades
		WHERE sim_id=?
		ORDER BY trade_date ASC, id ASC
	`, id); err != nil {
		return nil, fmt.Errorf("query simulation trades: %w", err)
	}
	if err := db.Select(&detail.Snapshots, `
		SELECT sim_id, date, cash, total_value, COALESCE(holdings_json,'') AS holdings_json
		FROM simulation_snapshots
		WHERE sim_id=?
		ORDER BY date ASC
	`, id); err != nil {
		return nil, fmt.Errorf("query simulation snapshots: %w", err)
	}
	return detail, nil
}

func DeleteSimulation(db *sqlx.DB, id int64) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM simulation_trades WHERE sim_id=?`, id); err != nil {
		return fmt.Errorf("delete simulation trades: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM simulation_snapshots WHERE sim_id=?`, id); err != nil {
		return fmt.Errorf("delete simulation snapshots: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM simulations WHERE id=?`, id); err != nil {
		return fmt.Errorf("delete simulation: %w", err)
	}
	return tx.Commit()
}

func getSimulationConfig(db *sqlx.DB, simID int64) (*Simulation, simulationParams, error) {
	var sim Simulation
	if err := db.Get(&sim,
		"SELECT "+simSelectCols+" FROM simulations WHERE id=?", simID); err != nil {
		return nil, simulationParams{}, fmt.Errorf("query simulation %d: %w", simID, err)
	}
	params := simulationParams{}
	if sim.ParamsJSON != "" {
		_ = json.Unmarshal([]byte(sim.ParamsJSON), &params)
	}
	return &sim, params, nil
}

func buildSimulation(db *sqlx.DB, sim *Simulation, params simulationParams, targetEndDate string) ([]SimulationTrade, []SimulationSnapshot, simulationSummary, error) {
	_, err := time.Parse("2006-01-02", sim.StartDate)
	if err != nil {
		return nil, nil, simulationSummary{}, fmt.Errorf("invalid simulation start date: %w", err)
	}
	end, err := time.Parse("2006-01-02", targetEndDate)
	if err != nil {
		return nil, nil, simulationSummary{}, fmt.Errorf("invalid simulation end date: %w", err)
	}
	start, _ := time.Parse("2006-01-02", sim.StartDate)
	if end.Before(start) {
		return nil, nil, simulationSummary{}, fmt.Errorf("simulation end date before start date")
	}

	codes := parseFundPool(sim.FundPoolJSON)
	if len(codes) == 0 {
		return nil, nil, simulationSummary{}, fmt.Errorf("simulation fund pool is empty")
	}

	navSeries, allDates, err := loadNavSeries(db, sim, codes, targetEndDate)
	if err != nil {
		return nil, nil, simulationSummary{}, err
	}
	if len(allDates) == 0 {
		return nil, nil, simulationSummary{FundCount: len(codes)}, nil
	}

	shares := make(map[string]float64, len(codes))
	lastNav := make(map[string]float64, len(codes))
	trades := make([]SimulationTrade, 0)
	snapshots := make([]SimulationSnapshot, 0, len(allDates))
	cash := sim.InitialCapital
	lastMonth := ""

	for _, currentDate := range allDates {
		dailyNavs := navSeries[currentDate]
		for code, nav := range dailyNavs {
			lastNav[code] = nav
		}

		monthKey := currentDate[:7]
		if monthKey != lastMonth && params.MonthlyAmount > 0 {
			cash += params.MonthlyAmount
			lastMonth = monthKey
		} else if lastMonth == "" {
			lastMonth = monthKey
		}

		if cash > 0 {
			availableCodes := make([]string, 0, len(dailyNavs))
			for code := range dailyNavs {
				availableCodes = append(availableCodes, code)
			}
			sort.Strings(availableCodes)
			if len(availableCodes) > 0 {
				perCodeAmount := cash / float64(len(availableCodes))
				spent := 0.0
				for _, code := range availableCodes {
					nav := dailyNavs[code]
					if nav <= 0 || perCodeAmount <= 0 {
						continue
					}
					boughtShares := perCodeAmount / nav
					shares[code] += boughtShares
					spent += perCodeAmount
					trades = append(trades, SimulationTrade{
						TradeDate: currentDate,
						Code:      code,
						Action:    "buy",
						Shares:    boughtShares,
						Price:     nav,
						Amount:    perCodeAmount,
					})
				}
				cash -= spent
				if cash < 0 && cash > -1e-9 {
					cash = 0
				}
			}
		}

		positionsValue := 0.0
		holdingsMap := make(map[string]float64, len(shares))
		for code, shareCount := range shares {
			val := shareCount * lastNav[code]
			positionsValue += val
			holdingsMap[code] = val
		}
		totalValue := cash + positionsValue

		holdingsJSON, _ := json.Marshal(holdingsMap)
		snapshots = append(snapshots, SimulationSnapshot{
			Date:         currentDate,
			Cash:         cash,
			TotalValue:   totalValue,
			HoldingsJSON: string(holdingsJSON),
		})
	}

	summary := simulationSummary{
		FundCount:     len(codes),
		TradeCount:    len(trades),
		SnapshotCount: len(snapshots),
		FinalCash:     cash,
	}
	if len(snapshots) > 0 {
		last := snapshots[len(snapshots)-1]
		summary.FinalValue = last.TotalValue
		summary.PositionsValue = last.TotalValue - last.Cash
	}
	return trades, snapshots, summary, nil
}

func loadNavSeries(db *sqlx.DB, sim *Simulation, codes []string, targetEndDate string) (map[string]map[string]float64, []string, error) {
	dateToNavs := map[string]map[string]float64{}
	dateSet := map[string]struct{}{}

	for _, code := range codes {
		var rows []navPoint
		if err := db.Select(&rows, `
			SELECT date, nav
			FROM nav_history
			WHERE code=? AND date>=? AND date<=? AND nav>0
			ORDER BY date ASC
		`, code, sim.StartDate, targetEndDate); err != nil {
			return nil, nil, err
		}
		for _, row := range rows {
			if dateToNavs[row.Date] == nil {
				dateToNavs[row.Date] = map[string]float64{}
			}
			dateToNavs[row.Date][code] = row.Nav
			dateSet[row.Date] = struct{}{}
		}
	}

	allDates := make([]string, 0, len(dateSet))
	for date := range dateSet {
		allDates = append(allDates, date)
	}
	sort.Strings(allDates)
	return dateToNavs, allDates, nil
}

func replaceSimulationRun(db *sqlx.DB, simID int64, targetEndDate string, trades []SimulationTrade, snapshots []SimulationSnapshot, summary simulationSummary) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		UPDATE simulations SET status='running', current_date=?, updated_at=CURRENT_TIMESTAMP WHERE id=?
	`, targetEndDate, simID); err != nil {
		return fmt.Errorf("mark simulation running: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM simulation_trades WHERE sim_id=?`, simID); err != nil {
		return fmt.Errorf("clear simulation trades: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM simulation_snapshots WHERE sim_id=?`, simID); err != nil {
		return fmt.Errorf("clear simulation snapshots: %w", err)
	}

	for _, trade := range trades {
		if _, err := tx.Exec(`
			INSERT INTO simulation_trades (sim_id, trade_date, code, action, shares, price, amount)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, simID, trade.TradeDate, trade.Code, trade.Action, trade.Shares, trade.Price, trade.Amount); err != nil {
			return fmt.Errorf("insert simulation trade: %w", err)
		}
	}
	for _, snapshot := range snapshots {
		if _, err := tx.Exec(`
			INSERT INTO simulation_snapshots (sim_id, date, cash, total_value, holdings_json)
			VALUES (?, ?, ?, ?, ?)
		`, simID, snapshot.Date, snapshot.Cash, snapshot.TotalValue, snapshot.HoldingsJSON); err != nil {
			return fmt.Errorf("insert simulation snapshot: %w", err)
		}
	}

	rawSummary, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("marshal simulation summary: %w", err)
	}
	if _, err := tx.Exec(`
		UPDATE simulations
		SET status='completed', current_date=?, notes=?, updated_at=CURRENT_TIMESTAMP
		WHERE id=?
	`, targetEndDate, string(rawSummary), simID); err != nil {
		return fmt.Errorf("update simulation summary: %w", err)
	}
	return tx.Commit()
}

func parseFundPool(raw string) []string {
	if raw == "" {
		return nil
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err == nil {
		return uniqueStrings(list)
	}
	var wrapped struct {
		Codes []string `json:"codes"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapped); err == nil {
		return uniqueStrings(wrapped.Codes)
	}
	var single string
	if err := json.Unmarshal([]byte(raw), &single); err == nil && single != "" {
		return []string{single}
	}
	return nil
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
