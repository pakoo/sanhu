package fetcher

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
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

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, s := range stocks {
		_, err := tx.Exec(`
			INSERT OR IGNORE INTO fund_holdings (code, stock_code, stock_name, weight, industry, report_date)
			VALUES (?, ?, ?, ?, ?, ?)`,
			code, s.stockCode, s.stockName, s.weight, s.industry, reportDate,
		)
		if err != nil {
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
	for _, row := range rows {
		cells := extractCells(row[1])
		if len(cells) < 3 {
			continue
		}
		// 第1列：序号（数字），第2列：股票名称+代码，第3列：...，最后列：占净值比例%
		first := cells[0]
		if _, err := strconv.Atoi(strings.TrimSpace(first)); err != nil {
			continue // 跳过表头
		}
		nameCell := cells[1]
		name := reHTMLTag.ReplaceAllString(nameCell, "")
		name = strings.TrimSpace(name)

		code := ""
		if m := reStockCode.FindString(nameCell); m != "" {
			code = m
		}
		if code == "" {
			continue
		}

		// 取最后一列作为权重（占净值比例）
		weightStr := reHTMLTag.ReplaceAllString(cells[len(cells)-1], "")
		weightStr = strings.TrimSuffix(strings.TrimSpace(weightStr), "%")
		weight, _ := strconv.ParseFloat(weightStr, 64)

		stocks = append(stocks, stockItem{
			stockCode: code,
			stockName: name,
			weight:    weight,
		})
	}
	return
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
