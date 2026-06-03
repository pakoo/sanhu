package rebalance

import (
	"fmt"
	"sort"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/scoring"
	"github.com/pakoo/sanhu/internal/timing"
)

// ─── 数据类型 ──────────────────────────────────────────────────────────────────

type TargetAllocation struct {
	Category  string  `db:"category" json:"category"`
	TargetPct float64 `db:"target_pct" json:"target_pct"`
}

type RebalanceSuggestion struct {
	Category      string   `json:"category"`
	CurrentPct    float64  `json:"current_pct"`
	TargetPct     float64  `json:"target_pct"`
	GapPct        float64  `json:"gap_pct"` // current - target（正=超配，负=欠配）
	Action        string   `json:"action"`  // buy / sell / hold
	AmountSuggest float64  `json:"amount_suggest"`
	Candidates    []string `json:"candidates"` // 推荐基金代码（买入时）
	Note          string   `json:"note"`
}

type MonthlyPlan struct {
	Month      int                `json:"month"`
	Allocation map[string]float64 `json:"allocation"` // category→value
	BuyAmount  map[string]float64 `json:"buy_amount"`
}

type GradualPlan struct {
	Months       int            `json:"months"`
	MonthlyInvest float64       `json:"monthly_invest"`
	Plans        []MonthlyPlan  `json:"plans"`
}

// ─── 公开接口 ──────────────────────────────────────────────────────────────────

// GetTargetAllocation 读取用户设置的目标配置
func GetTargetAllocation(db *sqlx.DB) ([]TargetAllocation, error) {
	var allocs []TargetAllocation
	err := db.Select(&allocs, `SELECT category, target_pct FROM target_allocation ORDER BY category`)
	return allocs, err
}

// SetTargetAllocation 批量写入目标配置（整体替换）
func SetTargetAllocation(db *sqlx.DB, allocs []TargetAllocation) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`DELETE FROM target_allocation`)
	if err != nil {
		return err
	}
	for _, a := range allocs {
		_, err = tx.Exec(`INSERT INTO target_allocation (category, target_pct) VALUES (?,?)`,
			a.Category, a.TargetPct)
		if err != nil {
			return fmt.Errorf("insert alloc %s: %w", a.Category, err)
		}
	}
	return tx.Commit()
}

// GetRebalanceSuggestions 根据当前持仓和目标配置生成调仓建议
func GetRebalanceSuggestions(db *sqlx.DB, holdings []holdingInput, totalValue float64) ([]RebalanceSuggestion, error) {
	targets, err := GetTargetAllocation(db)
	if err != nil {
		return nil, err
	}

	// 计算当前各类别占比
	catValue := map[string]float64{}
	for _, h := range holdings {
		catValue[h.Category] += h.Value
	}

	// 取各类别排名前 3 候选基金（用于买入推荐）
	allCodes := make([]string, 0, len(holdings))
	for _, h := range holdings {
		allCodes = append(allCodes, h.Code)
	}
	_ = scoring.CalculateAllScores(db, allCodes)
	scores, _ := scoring.GetLatestScores(db)

	scoreMap := map[string]float64{}
	for _, s := range scores {
		scoreMap[s.Code] = s.TotalScore
	}

	var suggestions []RebalanceSuggestion

	for _, t := range targets {
		curVal := catValue[t.Category]
		curPct := 0.0
		if totalValue > 0 {
			curPct = curVal / totalValue * 100
		}
		gapPct := curPct - t.TargetPct

		sug := RebalanceSuggestion{
			Category:   t.Category,
			CurrentPct: curPct,
			TargetPct:  t.TargetPct,
			GapPct:     gapPct,
		}

		const threshold = 3.0 // 偏差超过 3% 才触发建议
		if gapPct > threshold {
			sug.Action = "sell"
			sug.AmountSuggest = (gapPct / 100) * totalValue
			sug.Note = fmt.Sprintf("超配 %.1f%%，建议减仓 %.0f 元", gapPct, sug.AmountSuggest)
		} else if gapPct < -threshold {
			sug.Action = "buy"
			sug.AmountSuggest = (-gapPct / 100) * totalValue

			// 取该类别评分最高的候选
			sug.Candidates = topCandidates(db, t.Category, scoreMap, 3)

			// 结合估值信号调整建议语气
			sig, _ := timing.GetTimingSignal(db, "")
			switch {
			case sig != nil && sig.Signal == timing.SignalUnfavorable:
				sug.Note = fmt.Sprintf("欠配 %.1f%%，当前估值偏高，建议分批买入", -gapPct)
			case sig != nil && sig.Signal == timing.SignalFavorable:
				sug.Note = fmt.Sprintf("欠配 %.1f%%，估值低位，可积极补仓", -gapPct)
			default:
				sug.Note = fmt.Sprintf("欠配 %.1f%%，建议补仓 %.0f 元", -gapPct, sug.AmountSuggest)
			}
		} else {
			sug.Action = "hold"
			sug.Note = "配置比例接近目标，暂无需调整"
		}

		suggestions = append(suggestions, sug)
	}

	// 按 gap 绝对值降序
	sort.Slice(suggestions, func(i, j int) bool {
		return abs(suggestions[i].GapPct) > abs(suggestions[j].GapPct)
	})

	return suggestions, nil
}

// GradualTransitionPlan 生成 N 个月渐进调仓计划
// monthlyInvest 为每月新增资金（可为 0）
func GradualTransitionPlan(db *sqlx.DB, holdings []holdingInput, totalValue, monthlyInvest float64, months int) (*GradualPlan, error) {
	suggestions, err := GetRebalanceSuggestions(db, holdings, totalValue)
	if err != nil {
		return nil, err
	}

	plans := make([]MonthlyPlan, months)

	// 当前配置快照
	catValue := map[string]float64{}
	for _, h := range holdings {
		catValue[h.Category] += h.Value
	}
	curTotal := totalValue

	for m := 0; m < months; m++ {
		curTotal += monthlyInvest
		buy := map[string]float64{}

		// 贪心：优先填补欠配最大的
		// 重新计算 gap
		type gap struct {
			cat string
			pct float64
		}
		var gaps []gap
		for _, sug := range suggestions {
			if sug.Action == "buy" {
				curPct := catValue[sug.Category] / curTotal * 100
				g := sug.TargetPct - curPct
				if g > 0 {
					gaps = append(gaps, gap{sug.Category, g})
				}
			}
		}
		sort.Slice(gaps, func(i, j int) bool { return gaps[i].pct > gaps[j].pct })

		remain := monthlyInvest
		for _, g := range gaps {
			if remain <= 0 {
				break
			}
			needed := g.pct / 100 * curTotal
			allocate := min(needed, remain)
			if allocate > 0 {
				buy[g.cat] += allocate
				catValue[g.cat] += allocate
				remain -= allocate
			}
		}

		// 快照本月配置比例
		alloc := map[string]float64{}
		for cat, v := range catValue {
			alloc[cat] = v / curTotal * 100
		}
		plans[m] = MonthlyPlan{Month: m + 1, Allocation: alloc, BuyAmount: buy}
	}

	return &GradualPlan{
		Months:        months,
		MonthlyInvest: monthlyInvest,
		Plans:         plans,
	}, nil
}

// ─── 内部辅助 ──────────────────────────────────────────────────────────────────

type holdingInput struct {
	Code     string
	Value    float64
	Category string
}

// HoldingInput 供外部调用时使用（与 risk 包 interface 保持一致）
type HoldingInput = holdingInput

func topCandidates(db *sqlx.DB, category string, scoreMap map[string]float64, n int) []string {
	type row struct {
		Code string `db:"code"`
	}
	var rows []row
	_ = db.Select(&rows, `
		SELECT code FROM funds WHERE category=? AND code NOT IN (SELECT code FROM holdings)
		LIMIT 50`, category)

	type scored struct {
		code  string
		score float64
	}
	var candidates []scored
	for _, r := range rows {
		candidates = append(candidates, scored{r.Code, scoreMap[r.Code]})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })

	out := make([]string, 0, n)
	for i, c := range candidates {
		if i >= n {
			break
		}
		out = append(out, c.code)
	}
	return out
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
