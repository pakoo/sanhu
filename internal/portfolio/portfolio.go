package portfolio

import (
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/jmoiron/sqlx"
)

// ─── 数据类型 ──────────────────────────────────────────────────────────────────

type Holding struct {
	Code         string    `db:"code" json:"code"`
	Name         string    `db:"name" json:"name"`
	Category     string    `db:"category" json:"category"`
	Shares       float64   `db:"shares" json:"shares"`
	CostAmount   float64   `db:"cost_amount" json:"cost_amount"`
	LatestNav    float64   `json:"latest_nav"`
	CurrentValue float64   `json:"current_value"`
	Profit       float64   `json:"profit"`       // was pnl_amount
	ProfitRate   float64   `json:"profit_rate"`  // was pnl_pct
	DailyProfit  float64   `json:"daily_profit"` // 今日浮盈（暂填 0）
	Return7d     float64   `json:"return_7d"`
	Return1m     float64   `json:"return_1m"`
	Return3m     float64   `json:"return_3m"`
	NavCurve     []float64 `json:"nav_curve"` // 归一化净值曲线（最近 30 日）
}

type CategoryAlloc struct {
	Category string  `json:"category"`
	Value    float64 `json:"value"`
	Pct      float64 `json:"pct"`
}

type PortfolioSummary struct {
	TotalValue  float64            `json:"total_value"`
	TotalCost   float64            `json:"total_cost"`
	TotalProfit float64            `json:"total_profit"`      // was total_pnl_amount
	TotalProfitRate float64        `json:"total_profit_rate"` // was total_pnl_pct
	DailyProfit float64            `json:"daily_profit"`      // 今日浮盈（暂填 0）
	Holdings         []Holding          `json:"holdings"`
	Allocation       map[string]float64 `json:"allocation"`        // {category: pct, ...}
	AllocationAmount map[string]float64 `json:"allocation_amount"` // {category: 金额, ...}
}

type NavPoint struct {
	Date string  `db:"date" json:"date"`
	Nav  float64 `db:"nav" json:"nav"`
}

type Transaction struct {
	ID          int64   `db:"id" json:"id"`
	Code        string  `db:"code" json:"code"`
	TxType      string  `db:"type" json:"type"` // buy / sell
	Date        string  `db:"date" json:"date"`
	Amount      float64 `db:"amount" json:"amount"`
	NavAtTrade  float64 `db:"nav_at_trade" json:"nav_at_trade"`
	Fee         float64 `db:"fee" json:"fee"`
	Notes       string  `db:"notes" json:"notes"`
}

type HoldingDetail struct {
	Code          string        `json:"code"`
	Name          string        `json:"name"`
	Category      string        `json:"category"`
	FundType      string        `json:"fund_type"`
	Manager       string        `json:"manager"`
	Company       string        `json:"company"`
	AUM           float64       `json:"aum"`
	Scope         string        `json:"scope"`         // fund description / investment objective text
	InceptionDate string        `json:"inception_date"`
	FeeRate       float64       `json:"fee_rate"`
	RiskLevel     string        `json:"risk_level"`
	Shares        float64       `json:"shares"`
	CostAmount    float64       `json:"cost_amount"`
	LatestNav     float64       `json:"latest_nav"`
	CurrentNav    float64       `json:"current_nav"`   // alias for latest_nav
	CurrentValue  float64       `json:"current_value"`
	Profit        float64       `json:"profit"`        // was pnl_amount
	ProfitRate    float64       `json:"profit_rate"`   // was pnl_pct
	DailyProfit   float64       `json:"daily_profit"`
	NavHistory    []NavPoint    `json:"nav_history"`
	Transactions  []Transaction `json:"transactions"`
	TopIndustries         []string `json:"top_industries"`
	PeerRank1y            float64  `json:"peer_rank_1y"` // 百分位 0-100，越低越好
	PeerRank              float64  `json:"peer_rank"`    // alias for peer_rank_1y
	BuyDate               *string  `json:"buy_date"`
	IndustryConcentration float64  `json:"industry_concentration"` // 前3大行业权重和
}

// ─── 公开接口 ──────────────────────────────────────────────────────────────────

// GetPortfolioSummary 汇总所有持仓，含分类配置比例和净值曲线
func GetPortfolioSummary(db *sqlx.DB) (*PortfolioSummary, error) {
	type rawHolding struct {
		Code       string  `db:"code"`
		Name       string  `db:"name"`
		Category   string  `db:"category"`
		Shares     float64 `db:"shares"`
		CostAmount float64 `db:"cost_amount"`
	}

	var rows []rawHolding
	// 与 Python 基准对齐：INNER JOIN funds、不过滤 shares=0（保留清仓持仓）、
	// 按当前市值(shares×最新净值)降序排序。
	if err := db.Select(&rows, `
		SELECT h.code, COALESCE(f.name,'') AS name,
		       COALESCE(f.category,'mixed') AS category,
		       h.shares, h.cost_amount
		FROM holdings h
		JOIN funds f ON f.code=h.code
		ORDER BY h.shares * COALESCE(
			(SELECT nav FROM nav_history WHERE code=h.code ORDER BY date DESC LIMIT 1), 1
		) DESC
	`); err != nil {
		return nil, fmt.Errorf("query holdings: %w", err)
	}

	summary := &PortfolioSummary{
		// 空库时也要序列化成 []，与 Python 原版一致；否则前端 holdings.length 会炸
		Holdings:         []Holding{},
		Allocation:       map[string]float64{},
		AllocationAmount: map[string]float64{},
	}

	catValueMap := map[string]float64{}

	for _, r := range rows {
		nav, err := latestNav(db, r.Code)
		if err != nil || nav == 0 {
			nav = r.CostAmount / max(r.Shares, 0.0001)
		}
		cur := nav * r.Shares
		cost := r.CostAmount
		profit := cur - cost
		profitRate := 0.0
		if cost > 0 {
			profitRate = profit / cost * 100
		}

		h := Holding{
			Code:         r.Code,
			Name:         r.Name,
			Category:     r.Category,
			Shares:       r.Shares,
			CostAmount:   cost,
			LatestNav:    nav,
			CurrentValue: cur,
			Profit:       profit,
			ProfitRate:   profitRate,
			DailyProfit:  0,
			Return7d:     periodReturn(db, r.Code, 7),
			Return1m:     periodReturn(db, r.Code, 30),
			Return3m:     periodReturn(db, r.Code, 90),
			NavCurve:     normalizedNavCurve(db, r.Code, 30),
		}
		summary.Holdings = append(summary.Holdings, h)
		summary.TotalValue += cur
		summary.TotalCost += cost
		catValueMap[r.Category] += cur
	}

	summary.TotalProfit = summary.TotalValue - summary.TotalCost
	if summary.TotalCost > 0 {
		summary.TotalProfitRate = summary.TotalProfit / summary.TotalCost * 100
	}

	// allocation as map[category]pct for frontend pie chart + 各类别金额
	for cat, val := range catValueMap {
		summary.AllocationAmount[cat] = math.Round(val*100) / 100
		if summary.TotalValue > 0 {
			summary.Allocation[cat] = math.Round(val/summary.TotalValue*10000) / 100
		}
	}

	return summary, nil
}

// GetHoldingDetail 单只基金详情（档案 + 净值历史 + 交易记录 + 行业 + 同类排名）
func GetHoldingDetail(db *sqlx.DB, code string) (*HoldingDetail, error) {
	type fundRow struct {
		Name          string         `db:"name"`
		Category      string         `db:"category"`
		FundType      string         `db:"fund_type"`
		Manager       string         `db:"manager"`
		Company       string         `db:"company"`
		AUM           float64        `db:"aum"`
		Scope         string         `db:"scope"` // fund description / investment objective text
		InceptionDate sql.NullString `db:"inception_date"`
		FeeRate       float64        `db:"fee_rate"`
		RiskLevel     string         `db:"risk_level"`
	}
	var f fundRow
	if err := db.Get(&f, `
		SELECT COALESCE(name,'') AS name,
		       COALESCE(category,'mixed') AS category,
		       COALESCE(fund_type,'') AS fund_type,
		       COALESCE(manager,'') AS manager,
		       COALESCE(company,'') AS company,
		       COALESCE(aum,0) AS aum,
		       COALESCE(scope,'') AS scope,
		       inception_date,
		       COALESCE(fee_rate,0) AS fee_rate,
		       COALESCE(risk_level,'') AS risk_level
		FROM funds WHERE code=?`, code); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("query fund %s: %w", code, err)
	}

	type holdingRow struct {
		Shares     float64 `db:"shares"`
		CostAmount float64 `db:"cost_amount"`
	}
	var hr holdingRow
	_ = db.Get(&hr, `SELECT COALESCE(shares,0) AS shares, COALESCE(cost_amount,0) AS cost_amount FROM holdings WHERE code=?`, code)

	nav, _ := latestNav(db, code)
	cur := nav * hr.Shares
	profit := cur - hr.CostAmount
	profitRate := 0.0
	if hr.CostAmount > 0 {
		profitRate = profit / hr.CostAmount * 100
	}

	navHistory, _ := GetNavHistory(db, code, 365)

	var txs []Transaction
	_ = db.Select(&txs, `
		SELECT id, code, type, date, amount, nav_at_trade, COALESCE(fee,0) AS fee, COALESCE(notes,'') AS notes
		FROM transactions WHERE code=? ORDER BY date DESC`, code)

	var industries []string
	var rawIndustries []struct {
		Industry string  `db:"industry"`
		Weight   float64 `db:"weight"`
	}
	_ = db.Select(&rawIndustries, `
		SELECT industry, COALESCE(weight,0) AS weight FROM fund_industry
		WHERE code=?
		ORDER BY report_date DESC, weight DESC
		LIMIT 5`, code)
	concentration := 0.0
	for i, ri := range rawIndustries {
		industries = append(industries, ri.Industry)
		if i < 3 {
			concentration += ri.Weight
		}
	}
	concentration = math.Round(concentration*100) / 100

	var buyDate *string
	var bd sql.NullString
	if err := db.Get(&bd, `SELECT buy_date FROM holdings WHERE code=?`, code); err == nil && bd.Valid && bd.String != "" {
		s := bd.String
		buyDate = &s
	}

	var peerRank float64
	_ = db.Get(&peerRank, `SELECT COALESCE(pct_1y,50) FROM fund_peer_ranks WHERE code=? ORDER BY rowid DESC LIMIT 1`, code)

	inception := ""
	if f.InceptionDate.Valid {
		inception = f.InceptionDate.String
	}

	return &HoldingDetail{
		Code:          code,
		Name:          f.Name,
		Category:      f.Category,
		FundType:      f.FundType,
		Manager:       f.Manager,
		Company:       f.Company,
		AUM:           f.AUM,
		Scope:         f.Scope,
		InceptionDate: inception,
		FeeRate:       f.FeeRate,
		RiskLevel:     f.RiskLevel,
		Shares:        hr.Shares,
		CostAmount:    hr.CostAmount,
		LatestNav:     nav,
		CurrentNav:    nav,
		CurrentValue:  cur,
		Profit:        profit,
		ProfitRate:    profitRate,
		DailyProfit:   0,
		NavHistory:    navHistory,
		Transactions:  txs,
		TopIndustries:         industries,
		PeerRank1y:            peerRank,
		PeerRank:              peerRank,
		BuyDate:               buyDate,
		IndustryConcentration: concentration,
	}, nil
}

// RecordTransaction 记录交易并同步持仓
func RecordTransaction(db *sqlx.DB, code, txType, date string, amount, navAtTrade, fee float64, notes string) error {
	if navAtTrade <= 0 {
		return fmt.Errorf("nav_at_trade must be positive")
	}
	shares := amount / navAtTrade

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO transactions (code, type, date, amount, nav_at_trade, fee, notes)
		VALUES (?,?,?,?,?,?,?)`,
		code, txType, date, amount, navAtTrade, fee, notes)
	if err != nil {
		return fmt.Errorf("insert transaction: %w", err)
	}

	switch txType {
	case "buy":
		// 买入：增加持仓
		_, err = tx.Exec(`
			INSERT INTO holdings (code, shares, cost_amount, buy_date)
			VALUES (?,?,?,?)
			ON CONFLICT(code) DO UPDATE SET
			  shares = shares + excluded.shares,
			  cost_amount = cost_amount + excluded.cost_amount`,
			code, shares, amount, date)
	case "sell":
		// 卖出：按比例减少持仓
		var cur struct {
			Shares     float64 `db:"shares"`
			CostAmount float64 `db:"cost_amount"`
		}
		if err2 := tx.Get(&cur, `SELECT shares, cost_amount FROM holdings WHERE code=?`, code); err2 != nil {
			return fmt.Errorf("no holding to sell: %w", err2)
		}
		sellRatio := shares / max(cur.Shares, 0.0001)
		if sellRatio > 1 {
			sellRatio = 1
		}
		newShares := cur.Shares - shares
		newCost := cur.CostAmount * (1 - sellRatio)
		if newShares <= 0.0001 {
			_, err = tx.Exec(`DELETE FROM holdings WHERE code=?`, code)
		} else {
			_, err = tx.Exec(`UPDATE holdings SET shares=?, cost_amount=? WHERE code=?`,
				newShares, newCost, code)
		}
	default:
		return fmt.Errorf("unknown tx type: %s", txType)
	}
	if err != nil {
		return fmt.Errorf("update holdings: %w", err)
	}

	return tx.Commit()
}

// GetNavHistory 查询净值历史
func GetNavHistory(db *sqlx.DB, code string, days int) ([]NavPoint, error) {
	var pts []NavPoint
	err := db.Select(&pts, `
		SELECT date, nav FROM nav_history
		WHERE code=? AND date >= date('now', ?)
		ORDER BY date ASC`,
		code, fmt.Sprintf("-%d days", days))
	return pts, err
}

// ─── 内部辅助 ──────────────────────────────────────────────────────────────────

func latestNav(db *sqlx.DB, code string) (float64, error) {
	var nav float64
	err := db.Get(&nav, `SELECT nav FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 1`, code)
	return nav, err
}

// periodReturn 计算 n 日前到今天的涨跌幅（%）
func periodReturn(db *sqlx.DB, code string, days int) float64 {
	cutoff := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	type pair struct {
		Nav float64 `db:"nav"`
	}
	var first, last pair
	if err := db.Get(&first, `SELECT nav FROM nav_history WHERE code=? AND date>=? ORDER BY date ASC LIMIT 1`, code, cutoff); err != nil {
		return 0
	}
	if err := db.Get(&last, `SELECT nav FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 1`, code); err != nil {
		return 0
	}
	if first.Nav == 0 {
		return 0
	}
	return (last.Nav - first.Nav) / first.Nav * 100
}

// normalizedNavCurve 返回最近 n 日净值，归一化到首日=1.0
func normalizedNavCurve(db *sqlx.DB, code string, days int) []float64 {
	pts, err := GetNavHistory(db, code, days)
	if err != nil || len(pts) == 0 {
		return nil
	}
	base := pts[0].Nav
	if base == 0 {
		return nil
	}
	curve := make([]float64, len(pts))
	for i, p := range pts {
		curve[i] = math.Round(p.Nav/base*1000) / 1000
	}
	return curve
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
