package fetcher

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

var (
	reHoldingsContent = regexp.MustCompile(`(?s)content:"(.*?)",arryear:`)
	reReportDate      = regexp.MustCompile(`截止至：<font[^>]*>([0-9]{4}-[0-9]{2}-[0-9]{2})</font>`)
	reTableBody       = regexp.MustCompile(`(?s)<table[^>]*>(.*?)</table>`)
	reTR              = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	reTD              = regexp.MustCompile(`(?s)<t[dh][^>]*>(.*?)</t[dh]>`)
	reHTMLTag         = regexp.MustCompile(`<[^>]+>`)
	reStockCode       = regexp.MustCompile(`\b[036]\d{5}\b`)
)

type holdingsAPIResp struct {
	Content string `json:"content"`
}

// FetchFundHoldings 抓取基金前十重仓股和行业分布
func FetchFundHoldings(db *sqlx.DB, code string) error {
	// 跳过债券型基金
	var category string
	if err := db.Get(&category, `SELECT COALESCE(category,'') FROM funds WHERE code=?`, code); err == nil {
		if category == "bond" {
			return nil
		}
	}

	url := fmt.Sprintf(
		"https://fundf10.eastmoney.com/FundArchivesDatas.aspx?type=jjcc&code=%s&topline=10&year=&month=",
		code,
	)
	body, err := get(url)
	if err != nil {
		return fmt.Errorf("fetch holdings %s: %w", code, err)
	}

	// 接口返回 JS 赋值格式，提取 content 字段
	// 也可能直接是 JSON
	text := string(body)
	reportDate, stocks := parseHoldingsHTML(text)
	if reportDate == "" || len(stocks) == 0 {
		// 尝试直接 JSON 解析
		var resp holdingsAPIResp
		if err := json.Unmarshal(body, &resp); err == nil && resp.Content != "" {
			reportDate, stocks = parseHoldingsHTML(resp.Content)
		}
	}
	if reportDate == "" {
		return nil // 数据暂无，跳过
	}

	// 补每只股票的申万行业（f127）。行业分布就是靠它聚合出来的，
	// 不补的话 fund_industry 永远是空的。
	for i := range stocks {
		stocks[i].industry = resolveStockIndustry(db, stocks[i].stockCode)
	}

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, s := range stocks {
		// REPLACE 而非 IGNORE：早期版本写进过一批坏行（名称=代码、权重=市值），
		// IGNORE 会让它们永远留在库里，重抓也修不掉。
		var industry interface{}
		if s.industry != "" {
			industry = s.industry
		}
		_, err := tx.Exec(`
			INSERT OR REPLACE INTO fund_holdings (code, stock_code, stock_name, weight, industry, report_date)
			VALUES (?, ?, ?, ?, ?, ?)`,
			code, s.stockCode, s.stockName, s.weight, industry, reportDate,
		)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	return AggregateFundIndustry(db, code)
}

// industryMemo 进程内缓存，避免同一轮刷新里重复查同一只股票
var (
	industryMemo   = map[string]string{}
	industryMemoMu sync.Mutex
)

// resolveStockIndustry 取一只股票的行业，优先用已有数据，实在没有才联网。
//
// 为什么不像 Python 那样每只股票都直接请求：一只基金要查 10 只股票，
// 刷 20 只基金就是 200 个请求，实测会被东财限流（push2 直接返回空，且持续数分钟）。
// 而行业信息几乎不变，且不同基金大量重仓同一批股票，所以：
//  1. 先查进程内缓存
//  2. 再查库里任意一条已知该股票行业的记录（跨基金、跨报告期复用）
//  3. 都没有才联网，且放慢节奏
//
// 查不到返回空串——行业只是补充信息，不该因为它失败而丢掉整条持仓；
// 下次刷新会再试着补上（AggregateFundIndustry 能用部分行业数据工作）。
func resolveStockIndustry(db *sqlx.DB, stockCode string) string {
	if stockCode == "" {
		return ""
	}

	industryMemoMu.Lock()
	if v, ok := industryMemo[stockCode]; ok {
		industryMemoMu.Unlock()
		return v
	}
	industryMemoMu.Unlock()

	var known string
	if err := db.Get(&known, `
		SELECT industry FROM fund_holdings
		WHERE stock_code=? AND COALESCE(industry,'')!=''
		LIMIT 1`, stockCode); err == nil && known != "" {
		industryMemoMu.Lock()
		industryMemo[stockCode] = known
		industryMemoMu.Unlock()
		return known
	}

	v := fetchStockIndustry(stockCode)
	if v != "" {
		industryMemoMu.Lock()
		industryMemo[stockCode] = v
		industryMemoMu.Unlock()
	}
	return v
}

// fetchStockIndustry 联网查一只股票的申万行业（东财 f127 字段）。
func fetchStockIndustry(stockCode string) string {
	if stockCode == "" {
		return ""
	}
	// secid 前缀：6 开头为沪市(1.)，其余为深市(0.)
	market := "0"
	if strings.HasPrefix(stockCode, "6") {
		market = "1"
	}
	url := fmt.Sprintf(
		"https://push2.eastmoney.com/api/qt/stock/get?fltt=2&invt=2&fields=f127&secid=%s.%s",
		market, stockCode,
	)
	// Referer 用 quote 域（get() 里写死的是 fundf10，故单独发请求）。
	// 依据：Python 版对这个接口就是特意换成 quote 的；手工用 quote 域实测可取到数据。
	// 注意：Go 这条路径尚未在真机端到端跑通过——首次联调时对方开始限流（连续返回空且持续数分钟），
	// 因此「fundf10 拿不到数据」是推断而非定论，也可能当时已经在限流。
	// 下次能正常访问时应复核一次：若 fundf10 域同样可用，这里可以合并回 get()。
	body, err := getWithReferer(url, "https://quote.eastmoney.com/")
	// 限流时对方直接断连或返回空，这里退让一次；再不行就放弃，交给下次刷新补
	time.Sleep(600 * time.Millisecond)
	if err != nil || len(body) == 0 {
		return ""
	}
	var resp struct {
		Data struct {
			F127 string `json:"f127"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return ""
	}
	return strings.TrimSpace(resp.Data.F127)
}

// AggregateFundIndustry 由最新一期持仓股的行业聚合出基金的行业分布，写入 fund_industry。
//
// 东财的 hycc（行业配置）接口早已失效，Python 版就是改用这个口径的；
// Go 重写时整块漏掉了，导致 fund_industry 表从来没有被写过一次。
func AggregateFundIndustry(db *sqlx.DB, code string) error {
	var category string
	if err := db.Get(&category, `SELECT COALESCE(category,'') FROM funds WHERE code=?`, code); err == nil {
		if category == "bond" {
			return nil // 债基不做行业穿透
		}
	}

	var reportDate string
	if err := db.Get(&reportDate, `SELECT COALESCE(MAX(report_date),'') FROM fund_holdings WHERE code=?`, code); err != nil || reportDate == "" {
		return nil
	}

	var rows []struct {
		Industry string  `db:"industry"`
		Weight   float64 `db:"weight"`
	}
	if err := db.Select(&rows, `
		SELECT industry, COALESCE(weight,0) AS weight
		FROM fund_holdings
		WHERE code=? AND report_date=? AND COALESCE(industry,'')!=''`, code, reportDate); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	agg := map[string]float64{}
	for _, r := range rows {
		agg[r.Industry] += r.Weight
	}

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 先清掉这只基金这一期的旧行业数据，避免上一次的残留和本次混在一起
	if _, err := tx.Exec(`DELETE FROM fund_industry WHERE code=? AND report_date=?`, code, reportDate); err != nil {
		return err
	}
	for industry, weight := range agg {
		if _, err := tx.Exec(`
			INSERT OR REPLACE INTO fund_industry (code, industry, weight, report_date)
			VALUES (?, ?, ?, ?)`,
			code, industry, math.Round(weight*100)/100, reportDate,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type stockItem struct {
	stockCode string
	stockName string
	weight    float64
	industry  string
}

// parseHoldingsHTML 解析季报持仓表。
//
// 按表头文字定位列，而不是写死下标——之前正是写死下标出的事：
// 代码取了 cells[1]、名称也取 cells[1]、权重取最后一列，而东财的真实表头是
//
//	序号 | 股票代码 | 股票名称 | 最新价 | 涨跌幅 | 相关资讯 | 占净值比例 | 持股数(万股) | 持仓市值(万元)
//
// 于是 stock_name 落成了股票代码，权重落成了持仓市值（带千分位逗号，解析失败就是 0，
// 侥幸解析成功的更糟——出现「某只股票占基金净值 876%」）。
// 认表头之后，东财再调整列顺序也不会把数据写歪。
func parseHoldingsHTML(html string) (reportDate string, stocks []stockItem) {
	// 提取报告日期
	if m := reReportDate.FindStringSubmatch(html); len(m) >= 2 {
		reportDate = m[1]
	}
	if reportDate == "" {
		return
	}

	// 找第一个 table
	tableMatch := reTableBody.FindStringSubmatch(html)
	if tableMatch == nil {
		return
	}

	rows := reTR.FindAllStringSubmatch(tableMatch[1], -1)

	// 先从表头确定各列位置
	codeIdx, nameIdx, weightIdx := -1, -1, -1
	for _, row := range rows {
		cells := extractCells(row[1])
		if len(cells) < 3 {
			continue
		}
		// 表头行：第一格不是数字
		if _, err := strconv.Atoi(strings.TrimSpace(cleanCell(cells[0]))); err == nil {
			continue
		}
		for i, c := range cells {
			switch h := cleanCell(c); {
			case strings.Contains(h, "股票代码"), h == "代码":
				codeIdx = i
			case strings.Contains(h, "股票名称"), h == "名称":
				nameIdx = i
			case strings.Contains(h, "占净值"):
				weightIdx = i
			}
		}
		break
	}

	for _, row := range rows {
		cells := extractCells(row[1])
		if len(cells) < 3 {
			continue
		}
		// 跳过表头：数据行第一格是序号
		if _, err := strconv.Atoi(strings.TrimSpace(cleanCell(cells[0]))); err != nil {
			continue
		}

		var code, name string
		var weight float64

		if codeIdx >= 0 && codeIdx < len(cells) {
			code = cleanCell(cells[codeIdx])
		}
		if nameIdx >= 0 && nameIdx < len(cells) {
			name = cleanCell(cells[nameIdx])
		}
		if weightIdx >= 0 && weightIdx < len(cells) {
			weight = parseWeight(cleanCell(cells[weightIdx]))
		}

		// 表头缺失时的兜底：从整行里找一个像股票代码的数字，权重找带 % 的格子
		if code == "" {
			code = reStockCode.FindString(row[1])
		}
		if weightIdx < 0 {
			for _, c := range cells {
				if v := cleanCell(c); strings.HasSuffix(v, "%") {
					weight = parseWeight(v)
					break
				}
			}
		}
		if code == "" {
			continue
		}
		if name == "" {
			name = code // 实在没有名称时退回代码，至少不丢这条持仓
		}

		stocks = append(stocks, stockItem{
			stockCode: code,
			stockName: name,
			weight:    weight,
		})
	}
	return
}

// cleanCell 去掉单元格里的标签与空白（含 &nbsp;）
func cleanCell(s string) string {
	s = reHTMLTag.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	return strings.TrimSpace(s)
}

// parseWeight 解析「9.52%」这类占比；千分位逗号要去掉，否则解析失败落 0
func parseWeight(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	s = strings.ReplaceAll(s, ",", "")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func extractCells(rowHTML string) []string {
	matches := reTD.FindAllStringSubmatch(rowHTML, -1)
	cells := make([]string, 0, len(matches))
	for _, m := range matches {
		cells = append(cells, m[1])
	}
	return cells
}

// FetchFundHoldingsAll 批量抓取（3 workers）
func FetchFundHoldingsAll(db *sqlx.DB) error {
	codes, err := allFundCodes(db)
	if err != nil {
		return err
	}
	log.Printf("[fetcher] FetchFundHoldingsAll: %d funds", len(codes))
	errs := workerPool(codes, 3, 500*time.Millisecond, func(code string) (struct{}, error) {
		return struct{}{}, FetchFundHoldings(db, code)
	})
	for _, e := range errs {
		log.Printf("[fetcher] holdings error: %v", e)
	}
	return nil
}
