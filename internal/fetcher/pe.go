package fetcher

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"time"

	"github.com/jmoiron/sqlx"
)

// eastmoney datacenter API 返回格式
type peAPIResp struct {
	Result struct {
		Data []struct {
			TradeDate string  `json:"TRADE_DATE"`
			PE        float64 `json:"PE"`
			PB        float64 `json:"PB"`
		} `json:"data"`
	} `json:"result"`
}

// FetchIndexPE 抓取指数（如沪深300=000300）历史 PE/PB，INSERT OR IGNORE 到 index_pe_history
func FetchIndexPE(db *sqlx.DB, indexCode string, pageSize int) error {
	if pageSize <= 0 {
		pageSize = 500
	}
	apiURL := fmt.Sprintf(
		"https://datacenter.eastmoney.com/securities/api/data/v1/get"+
			"?reportName=RPT_VALUATIONNEW_OVERALL"+
			"&columns=TRADE_DATE,PE,PB"+
			"&filter=(SECURITY_CODE=%q)"+
			"&sortColumns=TRADE_DATE&sortTypes=1"+
			"&pageNumber=1&pageSize=%d",
		indexCode, pageSize,
	)
	// filter 参数要用 URL encode 后才能正确传
	filterVal := fmt.Sprintf(`(SECURITY_CODE="%s")`, indexCode)
	apiURL = "https://datacenter.eastmoney.com/securities/api/data/v1/get" +
		"?reportName=RPT_VALUATIONNEW_OVERALL" +
		"&columns=TRADE_DATE,PE,PB" +
		"&filter=" + url.QueryEscape(filterVal) +
		"&sortColumns=TRADE_DATE&sortTypes=1" +
		fmt.Sprintf("&pageNumber=1&pageSize=%d", pageSize)

	body, err := get(apiURL)
	if err != nil {
		return fmt.Errorf("fetch pe %s: %w", indexCode, err)
	}

	var resp peAPIResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("parse pe %s: %w", indexCode, err)
	}

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, item := range resp.Result.Data {
		if item.TradeDate == "" || item.PE == 0 {
			continue
		}
		// TRADE_DATE 可能带时间部分，只取日期
		date := item.TradeDate
		if len(date) > 10 {
			date = date[:10]
		}
		_, err := tx.Exec(`
			INSERT OR IGNORE INTO index_pe_history (index_code, date, pe, pb)
			VALUES (?, ?, ?, ?)`,
			indexCode, date, item.PE, item.PB,
		)
		if err != nil {
			return fmt.Errorf("insert pe %s %s: %w", indexCode, date, err)
		}
	}
	return tx.Commit()
}

// CalcPEPercentile 计算 indexCode 当前 PE 在近 years 年历史中的百分位
// 返回 0-100，越高表示当前估值越贵
func CalcPEPercentile(db *sqlx.DB, indexCode string, years int) (float64, error) {
	if years <= 0 {
		years = 5
	}
	cutoff := time.Now().AddDate(-years, 0, 0).Format("2006-01-02")

	// 取最新一天的 PE
	var currentPE float64
	if err := db.Get(&currentPE,
		`SELECT pe FROM index_pe_history WHERE index_code=? AND pe>0 ORDER BY date DESC LIMIT 1`,
		indexCode,
	); err != nil {
		return 0, fmt.Errorf("get current pe %s: %w", indexCode, err)
	}

	// 统计历史中低于等于当前 PE 的天数 / 总天数
	var belowCount, totalCount int
	if err := db.Get(&totalCount,
		`SELECT COUNT(*) FROM index_pe_history WHERE index_code=? AND date>=? AND pe>0`,
		indexCode, cutoff,
	); err != nil || totalCount == 0 {
		return 0, fmt.Errorf("no pe history for %s since %s", indexCode, cutoff)
	}
	if err := db.Get(&belowCount,
		`SELECT COUNT(*) FROM index_pe_history WHERE index_code=? AND date>=? AND pe>0 AND pe<=?`,
		indexCode, cutoff, currentPE,
	); err != nil {
		return 0, fmt.Errorf("count below pe %s: %w", indexCode, err)
	}

	return float64(belowCount) / float64(totalCount) * 100, nil
}

// FetchIndexPEAll 批量抓取常见指数 PE 历史（单 worker，间隔 500ms）
func FetchIndexPEAll(db *sqlx.DB) error {
	indices := []string{
		"000300", // 沪深300
		"000905", // 中证500
		"000852", // 中证1000
		"000016", // 上证50
		"399006", // 创业板指
		"399300", // 深证300
	}
	log.Printf("[fetcher] FetchIndexPEAll: %d indices", len(indices))
	errs := workerPool(indices, 1, 500*time.Millisecond, func(code string) (struct{}, error) {
		return struct{}{}, FetchIndexPE(db, code, 500)
	})
	for _, e := range errs {
		log.Printf("[fetcher] pe error: %v", e)
	}
	return nil
}
