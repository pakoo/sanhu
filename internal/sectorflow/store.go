package sectorflow

import (
	"fmt"
	"sort"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/fetcher"
)

// 落库层。表结构和「为什么破例落库」写在 migrations/005_sector_fund_flow.sql。

// MinuteKeepTop 每日保留分时明细的板块数
// （sector-fund-flow.html Q4 定 20，fundflow-replay.html Q2 扩到 50）。
//
// 全量存不起：1000 板块 × 240 点 × 250 交易日 ≈ 6000 万行/年。
// 取 50：50 × 240 × 250 ≈ 300 万行/年，约 125 MB。
//
// **这个数字决定了回放的天花板，而且不可回溯** —— 东财只提供当日分时，
// 今天没存的板块，明天就再也补不回来了。扩容要趁早，缩容随时可以。
//
// 成本：收盘任务多跑 30 个请求。串行 + 1.5 秒间隔 ≈ 75 秒，
// 19:00 跑，没人等，且离限流量级很远。
const MinuteKeepTop = 50

// SaveDaily 把一天的板块榜单写进库，重复执行覆盖同日同板块（按主键 upsert）。
//
// 一次事务写完：盘中每 30 分钟会调一次，中途失败留下半张榜比没有更糟 ——
// 榜单是要拿来横向比较的，缺一半的榜会得出错误结论。
func SaveDaily(db *sqlx.DB, date string, rows []fetcher.SectorFlow) error {
	if len(rows) == 0 {
		return fmt.Errorf("没有数据可写入（%s）", date)
	}
	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("开启事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const q = `
INSERT INTO sector_fund_flow_daily
 (date,bk_code,kind,name,main_net,main_pct,xl_net,xl_pct,lg_net,lg_pct,
  md_net,md_pct,sm_net,sm_pct,close_point,change_pct,top_stock,top_stock_code)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(date,bk_code) DO UPDATE SET
 kind=excluded.kind, name=excluded.name,
 main_net=excluded.main_net, main_pct=excluded.main_pct,
 xl_net=excluded.xl_net, xl_pct=excluded.xl_pct,
 lg_net=excluded.lg_net, lg_pct=excluded.lg_pct,
 md_net=excluded.md_net, md_pct=excluded.md_pct,
 sm_net=excluded.sm_net, sm_pct=excluded.sm_pct,
 close_point=excluded.close_point, change_pct=excluded.change_pct,
 top_stock=excluded.top_stock, top_stock_code=excluded.top_stock_code`

	for _, r := range rows {
		if _, err := tx.Exec(q, date, r.Code, r.Kind, r.Name,
			r.MainNet, r.MainPct, r.XLNet, r.XLPct, r.LGNet, r.LGPct,
			r.MDNet, r.MDPct, r.SMNet, r.SMPct, r.Price, r.ChangePct,
			r.TopStock, r.TopStockCode); err != nil {
			return fmt.Errorf("写入 %s/%s: %w", date, r.Code, err)
		}
	}
	return tx.Commit()
}

// SaveMinute 存某板块某日的分时曲线。
//
// 东财只提供**当日**分时，收盘后就查不到昨天的了 —— 所以这一步是不可补的：
// 今天没存，这一天的分钟曲线就永久丢失。
func SaveMinute(db *sqlx.DB, date, bkCode string, pts []fetcher.FlowPoint) error {
	if len(pts) == 0 {
		return fmt.Errorf("没有分时数据可写入（%s %s）", date, bkCode)
	}
	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("开启事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const q = `
INSERT INTO sector_fund_flow_minute (date,bk_code,time,main_net,xl_net,lg_net,md_net,sm_net)
VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(date,bk_code,time) DO UPDATE SET
 main_net=excluded.main_net, xl_net=excluded.xl_net,
 lg_net=excluded.lg_net, md_net=excluded.md_net, sm_net=excluded.sm_net`

	for _, p := range pts {
		if _, err := tx.Exec(q, date, bkCode, p.Time,
			p.MainNet, p.XLNet, p.LGNet, p.MDNet, p.SMNet); err != nil {
			return fmt.Errorf("写入分时 %s/%s/%s: %w", date, bkCode, p.Time, err)
		}
	}
	return tx.Commit()
}

type dailyRow struct {
	Date         string  `db:"date"`
	Code         string  `db:"bk_code"`
	Kind         string  `db:"kind"`
	Name         string  `db:"name"`
	MainNet      float64 `db:"main_net"`
	MainPct      float64 `db:"main_pct"`
	XLNet        float64 `db:"xl_net"`
	XLPct        float64 `db:"xl_pct"`
	LGNet        float64 `db:"lg_net"`
	LGPct        float64 `db:"lg_pct"`
	MDNet        float64 `db:"md_net"`
	MDPct        float64 `db:"md_pct"`
	SMNet        float64 `db:"sm_net"`
	SMPct        float64 `db:"sm_pct"`
	ClosePoint   float64 `db:"close_point"`
	ChangePct    float64 `db:"change_pct"`
	TopStock     string  `db:"top_stock"`
	TopStockCode string  `db:"top_stock_code"`
}

func (d dailyRow) toFlow() fetcher.SectorFlow {
	return fetcher.SectorFlow{
		Code: d.Code, Name: d.Name, Kind: d.Kind,
		Price: d.ClosePoint, ChangePct: d.ChangePct,
		MainNet: d.MainNet, MainPct: d.MainPct,
		XLNet: d.XLNet, XLPct: d.XLPct,
		LGNet: d.LGNet, LGPct: d.LGPct,
		MDNet: d.MDNet, MDPct: d.MDPct,
		SMNet: d.SMNet, SMPct: d.SMPct,
		TopStock: d.TopStock, TopStockCode: d.TopStockCode,
	}
}

// LoadRank 读某日某类板块的榜单。date 传空串表示「库里最新的那一天」。
//
// 这是限流时的兜底来源：抓不到就退回库里最近一次的结果，
// 前端标注「数据截至 X」而不是给一张空白页。
func LoadRank(db *sqlx.DB, kind fetcher.SectorKind, date string) ([]fetcher.SectorFlow, string, error) {
	if db == nil {
		return nil, "", nil
	}
	if date == "" {
		if err := db.Get(&date,
			`SELECT COALESCE(MAX(date),'') FROM sector_fund_flow_daily WHERE kind = ?`,
			string(kind)); err != nil {
			return nil, "", fmt.Errorf("查最新日期: %w", err)
		}
		if date == "" {
			return nil, "", nil // 库里还没有任何数据，不是错误
		}
	}
	var rows []dailyRow
	if err := db.Select(&rows,
		`SELECT * FROM sector_fund_flow_daily WHERE kind = ? AND date = ? ORDER BY main_net DESC`,
		string(kind), date); err != nil {
		return nil, "", fmt.Errorf("读榜单 %s/%s: %w", kind, date, err)
	}
	out := make([]fetcher.SectorFlow, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toFlow())
	}
	return out, date, nil
}

// LoadDaily 读某板块的历史日线。limit<=0 表示不限。
//
// 返回按日期升序（画图要的顺序）。库里存的可能比东财的 120 天更长 ——
// 那正是落库的意义。
func LoadDaily(db *sqlx.DB, bkCode string, limit int) ([]fetcher.FlowDay, error) {
	if db == nil {
		return nil, nil
	}
	q := `SELECT date,main_net,main_pct,xl_net,lg_net,md_net,sm_net,close_point,change_pct
	      FROM sector_fund_flow_daily WHERE bk_code = ? ORDER BY date DESC`
	args := []interface{}{bkCode}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	var rows []struct {
		Date       string  `db:"date"`
		MainNet    float64 `db:"main_net"`
		MainPct    float64 `db:"main_pct"`
		XLNet      float64 `db:"xl_net"`
		LGNet      float64 `db:"lg_net"`
		MDNet      float64 `db:"md_net"`
		SMNet      float64 `db:"sm_net"`
		ClosePoint float64 `db:"close_point"`
		ChangePct  float64 `db:"change_pct"`
	}
	if err := db.Select(&rows, q, args...); err != nil {
		return nil, fmt.Errorf("读日线 %s: %w", bkCode, err)
	}
	// 查询用 DESC 取最近 N 条，返回前翻转成升序供画图
	out := make([]fetcher.FlowDay, len(rows))
	for i, r := range rows {
		out[len(rows)-1-i] = fetcher.FlowDay{
			Date: r.Date, MainNet: r.MainNet, MainPct: r.MainPct,
			XLNet: r.XLNet, LGNet: r.LGNet, MDNet: r.MDNet, SMNet: r.SMNet,
			Close: r.ClosePoint, ChangePct: r.ChangePct,
		}
	}
	return out, nil
}

// LatestMinuteDate 该板块在库里最近有分时记录的交易日，没有则返回空串。
//
// 用于降级：东财限流或今天休市时，「今天」查不到分时，但库里可能存着上一个
// 交易日的完整曲线。给用户看上一交易日 + 标明日期，比给一张空图有用得多 ——
// 榜单那边已经是这个逻辑，分时不跟上就会出现「榜单有数据、点进去却是空的」。
func LatestMinuteDate(db *sqlx.DB, bkCode string) (string, error) {
	if db == nil {
		return "", nil
	}
	var date string
	if err := db.Get(&date,
		`SELECT COALESCE(MAX(date),'') FROM sector_fund_flow_minute WHERE bk_code = ?`,
		bkCode); err != nil {
		return "", fmt.Errorf("查 %s 最近分时日期: %w", bkCode, err)
	}
	return date, nil
}

// LoadMinute 读某板块某日的分时曲线（按时间升序）
func LoadMinute(db *sqlx.DB, bkCode, date string) ([]fetcher.FlowPoint, error) {
	if db == nil {
		return nil, nil
	}
	var rows []struct {
		Time    string  `db:"time"`
		MainNet float64 `db:"main_net"`
		XLNet   float64 `db:"xl_net"`
		LGNet   float64 `db:"lg_net"`
		MDNet   float64 `db:"md_net"`
		SMNet   float64 `db:"sm_net"`
	}
	if err := db.Select(&rows,
		`SELECT time,main_net,xl_net,lg_net,md_net,sm_net FROM sector_fund_flow_minute
		 WHERE bk_code = ? AND date = ? ORDER BY time`, bkCode, date); err != nil {
		return nil, fmt.Errorf("读分时 %s/%s: %w", bkCode, date, err)
	}
	out := make([]fetcher.FlowPoint, len(rows))
	for i, r := range rows {
		out[i] = fetcher.FlowPoint{
			Time: r.Time, MainNet: r.MainNet,
			XLNet: r.XLNet, LGNet: r.LGNet, MDNet: r.MDNet, SMNet: r.SMNet,
		}
	}
	return out, nil
}

// PurgeBefore 删掉 date 之前的数据，防止多年后数据库无声膨胀（产品稿 Q4 建议保留 3 年）。
// 返回删除的行数（日线 + 分时）。
func PurgeBefore(db *sqlx.DB, date string) (int64, error) {
	var total int64
	for _, t := range []string{"sector_fund_flow_daily", "sector_fund_flow_minute"} {
		res, err := db.Exec(`DELETE FROM `+t+` WHERE date < ?`, date)
		if err != nil {
			return total, fmt.Errorf("清理 %s: %w", t, err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// ReplayDay 一天的回放数据。
//
// 时间轴**共享**、数值只存数组，而不是每个点一个 {time,value} 对象：
// 50 个板块 × 240 点，前者约 120 KB，后者约 600 KB。回放要一次拿全，
// 差距直接体现在打开回放的等待时间上。
type ReplayDay struct {
	Date    string          `json:"date"`
	Times   []string        `json:"times"`   // 共享时间轴，如 09:31 … 15:00
	Sectors []ReplaySectors `json:"sectors"` // 按收盘净额绝对值降序
}

// ReplaySectors 一个板块在这一天的完整曲线
type ReplaySectors struct {
	Code   string    `json:"code"`
	Name   string    `json:"name"`
	Values []float64 `json:"values"` // 与 Times 一一对应，主力累计净流入（元）
}

// LoadReplay 读某日全部留档板块的分时，供回放一次性加载。
//
// date 传空表示「库里最近一个有分时留档的交易日」。
// 板块名从日线表带出来（分时表只存代码，不重复存名字）。
func LoadReplay(db *sqlx.DB, date string) (*ReplayDay, error) {
	if db == nil {
		return nil, nil
	}
	if date == "" {
		if err := db.Get(&date, `SELECT COALESCE(MAX(date),'') FROM sector_fund_flow_minute`); err != nil {
			return nil, fmt.Errorf("查最近回放日期: %w", err)
		}
		if date == "" {
			return nil, nil // 还没有任何留档，不是错误
		}
	}

	var rows []struct {
		Code    string  `db:"bk_code"`
		Time    string  `db:"time"`
		MainNet float64 `db:"main_net"`
		Name    string  `db:"name"`
	}
	// 左连日线表取名字：分时表没有名字列，而回放界面要显示板块名。
	// 用 MAX(name) 是因为同一天同一代码在日线表里只有一行，聚合只是为了能 GROUP。
	if err := db.Select(&rows, `
		SELECT m.bk_code, m.time, m.main_net,
		       COALESCE((SELECT d.name FROM sector_fund_flow_daily d
		                 WHERE d.bk_code = m.bk_code AND d.date = m.date LIMIT 1), m.bk_code) AS name
		FROM sector_fund_flow_minute m
		WHERE m.date = ?
		ORDER BY m.bk_code, m.time`, date); err != nil {
		return nil, fmt.Errorf("读回放数据 %s: %w", date, err)
	}
	if len(rows) == 0 {
		return &ReplayDay{Date: date}, nil
	}

	// 时间轴取所有板块时间点的并集（正常情况下每个板块都是同样的 240 个点，
	// 但抓取中途失败可能让某个板块少几个点 —— 用并集能让它在缺失处保持前值）
	timeSet := map[string]bool{}
	for _, r := range rows {
		timeSet[r.Time] = true
	}
	times := make([]string, 0, len(timeSet))
	for t := range timeSet {
		times = append(times, t)
	}
	sort.Strings(times)
	idx := make(map[string]int, len(times))
	for i, t := range times {
		idx[t] = i
	}

	type acc struct {
		name string
		vals []float64
		got  []bool
	}
	byCode := map[string]*acc{}
	var order []string
	for _, r := range rows {
		a := byCode[r.Code]
		if a == nil {
			a = &acc{name: r.Name, vals: make([]float64, len(times)), got: make([]bool, len(times))}
			byCode[r.Code] = a
			order = append(order, r.Code)
		}
		i := idx[r.Time]
		a.vals[i] = r.MainNet
		a.got[i] = true
	}
	// 缺失点用前一个值填充：分时是**累计值**，缺一个点不代表归零，
	// 补 0 会在曲线上画出一道假的深谷。
	for _, a := range byCode {
		last := 0.0
		for i := range a.vals {
			if a.got[i] {
				last = a.vals[i]
			} else {
				a.vals[i] = last
			}
		}
	}

	out := &ReplayDay{Date: date, Times: times}
	for _, code := range order {
		a := byCode[code]
		out.Sectors = append(out.Sectors, ReplaySectors{Code: code, Name: a.name, Values: a.vals})
	}
	// 按收盘净额绝对值降序：回放里最该被看见的排前面
	sort.Slice(out.Sectors, func(i, j int) bool {
		vi := out.Sectors[i].Values[len(out.Sectors[i].Values)-1]
		vj := out.Sectors[j].Values[len(out.Sectors[j].Values)-1]
		if vi < 0 {
			vi = -vi
		}
		if vj < 0 {
			vj = -vj
		}
		return vi > vj
	})
	return out, nil
}

// ReplayDates 列出库里有分时留档的日期（倒序），供回放的日期选择器用。
func ReplayDates(db *sqlx.DB, limit int) ([]string, error) {
	if db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 60
	}
	var dates []string
	if err := db.Select(&dates,
		`SELECT DISTINCT date FROM sector_fund_flow_minute ORDER BY date DESC LIMIT ?`, limit); err != nil {
		return nil, fmt.Errorf("列回放日期: %w", err)
	}
	return dates, nil
}
