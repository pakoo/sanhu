package scoring

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
)

const riskFreeRate = 0.02 // 2% 年化无风险利率

// ─── 数据类型 ──────────────────────────────────────────────────────────────────

type FundMetrics struct {
	Sharpe      float64
	MaxDrawdown float64 // 正数，如 18.5 表示 -18.5%
	AnnualReturn float64
	Volatility   float64
}

type FundScore struct {
	Code         string  `db:"code" json:"code"`
	Date         string  `db:"date" json:"date"`
	TotalScore   float64 `db:"total_score" json:"total_score"`
	SharpeRaw    float64 `db:"sharpe_raw" json:"sharpe_raw"`
	DrawdownRaw  float64 `db:"max_drawdown_raw" json:"drawdown_raw"`
	ReturnRaw    float64 `db:"annualized_return_raw" json:"return_raw"`
	VolatilityRaw float64 `db:"volatility_raw" json:"volatility_raw"`
}

// ─── 公开接口 ──────────────────────────────────────────────────────────────────

// CalculateFundMetrics 计算基金指标（Sharpe / 最大回撤 / 年化收益 / 波动率）
// 使用近 days 天净值数据，数据不足 30 条返回 nil
func CalculateFundMetrics(db *sqlx.DB, code string, days int) (*FundMetrics, error) {
	cutoff := time.Now().AddDate(0, 0, -days).Format("2006-01-02")

	type row struct {
		Nav float64 `db:"nav"`
	}
	var navs []row
	if err := db.Select(&navs, `
		SELECT nav FROM nav_history
		WHERE code=? AND date>=? AND nav>0
		ORDER BY date ASC`, code, cutoff); err != nil {
		return nil, fmt.Errorf("query nav %s: %w", code, err)
	}
	if len(navs) < 30 {
		return nil, nil
	}

	vals := make([]float64, len(navs))
	for i, r := range navs {
		vals[i] = r.Nav
	}

	returns := dailyReturns(vals)
	if len(returns) == 0 {
		return nil, nil
	}

	annReturn := annualizedReturn(vals)
	vol := volatility(returns) * math.Sqrt(250) // 年化
	sharpe := 0.0
	if vol > 0 {
		sharpe = (annReturn - riskFreeRate) / vol
	}
	maxDD := maxDrawdown(vals)

	return &FundMetrics{
		Sharpe:       sharpe,
		MaxDrawdown:  maxDD,
		AnnualReturn: annReturn * 100,
		Volatility:   vol * 100,
	}, nil
}

// CalculateAllScores 批量计算评分并写入 fund_scores 表
// 今天已有记录则跳过（缓存）
func CalculateAllScores(db *sqlx.DB, codes []string) error {
	today := time.Now().Format("2006-01-02")

	// 过滤掉今天已算过的
	toCalc := make([]string, 0, len(codes))
	for _, code := range codes {
		var cnt int
		db.Get(&cnt, `SELECT COUNT(*) FROM fund_scores WHERE code=? AND date=?`, code, today)
		if cnt == 0 {
			toCalc = append(toCalc, code)
		}
	}
	if len(toCalc) == 0 {
		return nil
	}

	// 计算原始指标
	type entry struct {
		code    string
		metrics *FundMetrics
	}
	entries := make([]entry, 0, len(toCalc))
	for _, code := range toCalc {
		m, _ := CalculateFundMetrics(db, code, 365)
		if m != nil {
			entries = append(entries, entry{code, m})
		}
	}
	if len(entries) == 0 {
		return nil
	}

	// 从指标中提取各维度值列表
	sharpes := make([]float64, len(entries))
	drawdowns := make([]float64, len(entries))
	returns := make([]float64, len(entries))
	vols := make([]float64, len(entries))
	for i, e := range entries {
		sharpes[i] = e.metrics.Sharpe
		drawdowns[i] = e.metrics.MaxDrawdown
		returns[i] = e.metrics.AnnualReturn
		vols[i] = e.metrics.Volatility
	}

	// 转百分位评分（0-100）
	sharpeScores := percentileScores(sharpes, true)    // 高好
	drawdownScores := percentileScores(drawdowns, false) // 低好（回撤小=好）
	returnScores := percentileScores(returns, true)    // 高好
	volScores := percentileScores(vols, false)         // 低好

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for i, e := range entries {
		// 权重：夏普30% + 回撤25% + 收益25% + 波动20%
		total := sharpeScores[i]*0.30 + drawdownScores[i]*0.25 +
			returnScores[i]*0.25 + volScores[i]*0.20

		_, err := tx.Exec(`
			INSERT OR REPLACE INTO fund_scores
			  (code, date, total_score, sharpe_raw, max_drawdown_raw, annualized_return_raw, volatility_raw)
			VALUES (?,?,?,?,?,?,?)`,
			e.code, today,
			math.Round(total*100)/100,
			math.Round(e.metrics.Sharpe*1000)/1000,
			math.Round(e.metrics.MaxDrawdown*100)/100,
			math.Round(e.metrics.AnnualReturn*100)/100,
			math.Round(e.metrics.Volatility*100)/100,
		)
		if err != nil {
			return fmt.Errorf("insert score %s: %w", e.code, err)
		}
	}
	return tx.Commit()
}

// GetLatestScores 取所有基金最新评分
func GetLatestScores(db *sqlx.DB) ([]FundScore, error) {
	var scores []FundScore
	err := db.Select(&scores, `
		SELECT s.code, s.date, s.total_score,
		       COALESCE(s.sharpe_raw,0) AS sharpe_raw, COALESCE(s.max_drawdown_raw,0) AS max_drawdown_raw, COALESCE(s.annualized_return_raw,0) AS annualized_return_raw, COALESCE(s.volatility_raw,0) AS volatility_raw
		FROM fund_scores s
		INNER JOIN (
			SELECT code, MAX(date) AS max_date FROM fund_scores GROUP BY code
		) latest ON s.code=latest.code AND s.date=latest.max_date
		ORDER BY s.total_score DESC`)
	return scores, err
}

// GetFundScore 取单只基金最新评分（找不到返回 nil）
func GetFundScore(db *sqlx.DB, code string) (*FundScore, error) {
	var s FundScore
	err := db.Get(&s, `
		SELECT code, date, total_score, COALESCE(sharpe_raw,0) AS sharpe_raw, COALESCE(max_drawdown_raw,0) AS max_drawdown_raw, COALESCE(annualized_return_raw,0) AS annualized_return_raw, COALESCE(volatility_raw,0) AS volatility_raw
		FROM fund_scores WHERE code=? ORDER BY date DESC LIMIT 1`, code)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ─── 内部辅助 ──────────────────────────────────────────────────────────────────

// dailyReturns 计算逐日收益率序列
func dailyReturns(navs []float64) []float64 {
	if len(navs) < 2 {
		return nil
	}
	ret := make([]float64, len(navs)-1)
	for i := 1; i < len(navs); i++ {
		if navs[i-1] > 0 {
			ret[i-1] = (navs[i] - navs[i-1]) / navs[i-1]
		}
	}
	return ret
}

// annualizedReturn 以首末净值计算年化收益率（复利，按 250 交易日/年）
func annualizedReturn(navs []float64) float64 {
	n := len(navs)
	if n < 2 || navs[0] == 0 {
		return 0
	}
	total := navs[n-1] / navs[0]
	years := float64(n) / 250.0
	if years <= 0 {
		return 0
	}
	return math.Pow(total, 1/years) - 1
}

// volatility 计算日收益率标准差
func volatility(returns []float64) float64 {
	if len(returns) == 0 {
		return 0
	}
	sum := 0.0
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))
	varSum := 0.0
	for _, r := range returns {
		d := r - mean
		varSum += d * d
	}
	return math.Sqrt(varSum / float64(len(returns)))
}

// maxDrawdown 计算最大回撤（正数，百分比 0-100）
func maxDrawdown(navs []float64) float64 {
	if len(navs) == 0 {
		return 0
	}
	peak := navs[0]
	maxDD := 0.0
	for _, v := range navs {
		if v > peak {
			peak = v
		}
		if peak > 0 {
			dd := (peak - v) / peak
			if dd > maxDD {
				maxDD = dd
			}
		}
	}
	return maxDD * 100
}

// percentileScores 将一组值转为 0-100 百分位分
// higherIsBetter=true 表示值越大分越高
func percentileScores(values []float64, higherIsBetter bool) []float64 {
	n := len(values)
	if n == 0 {
		return nil
	}
	// 生成排序后的索引
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		return values[idx[a]] < values[idx[b]]
	})

	scores := make([]float64, n)
	for rank, origIdx := range idx {
		pct := float64(rank) / float64(n-1) * 100 // 0..100
		if higherIsBetter {
			scores[origIdx] = pct
		} else {
			scores[origIdx] = 100 - pct
		}
	}
	return scores
}
