package fetcher

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"
)

type navAPIResp struct {
	Data struct {
		LSJZList []struct {
			FSRQ   string `json:"FSRQ"`   // 日期
			DWJZ   string `json:"DWJZ"`   // 单位净值
			LJJZ   string `json:"LJJZ"`   // 累计净值
			JZZZL  string `json:"JZZZL"`  // 日涨跌幅%
		} `json:"LSJZList"`
		TotalCount int `json:"TotalCount"`
	} `json:"Data"`
}

// FetchNav 拉取基金近 days 天净值，INSERT OR IGNORE 到 nav_history
func FetchNav(db *sqlx.DB, code string, days int) error {
	pageSize := days
	if pageSize > 200 {
		pageSize = 200
	}
	url := fmt.Sprintf(
		"https://api.fund.eastmoney.com/f10/lsjz?fundCode=%s&pageIndex=1&pageSize=%d&startDate=&endDate=&_=%d",
		code, pageSize, time.Now().UnixMilli(),
	)

	body, err := get(url)
	if err != nil {
		return fmt.Errorf("fetch nav %s: %w", code, err)
	}

	var resp navAPIResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("parse nav %s: %w", code, err)
	}

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, item := range resp.Data.LSJZList {
		nav, _ := strconv.ParseFloat(item.DWJZ, 64)
		accNav, _ := strconv.ParseFloat(item.LJJZ, 64)
		dailyReturn, _ := strconv.ParseFloat(item.JZZZL, 64)
		if nav == 0 {
			continue
		}
		_, err := tx.Exec(
			`INSERT OR IGNORE INTO nav_history (code, date, nav, acc_nav, daily_return)
			 VALUES (?, ?, ?, ?, ?)`,
			code, item.FSRQ, nav, accNav, dailyReturn,
		)
		if err != nil {
			return fmt.Errorf("insert nav %s %s: %w", code, item.FSRQ, err)
		}
	}
	return tx.Commit()
}

// FetchNavAll 对 holdings + watchlist 中所有基金批量抓取净值（3 workers）
func FetchNavAll(db *sqlx.DB, days int) error {
	codes, err := allFundCodes(db)
	if err != nil {
		return err
	}
	log.Printf("[fetcher] FetchNavAll: %d funds", len(codes))
	errs := workerPool(codes, 3, 300*time.Millisecond, func(code string) (struct{}, error) {
		return struct{}{}, FetchNav(db, code, days)
	})
	for _, e := range errs {
		log.Printf("[fetcher] nav error: %v", e)
	}
	return nil
}

// allFundCodes 获取 holdings + watchlist 里所有去重的基金代码
func allFundCodes(db *sqlx.DB) ([]string, error) {
	var codes []string
	err := db.Select(&codes, `
		SELECT DISTINCT code FROM (
			SELECT code FROM holdings
			UNION
			SELECT code FROM watchlist
		)`)
	return codes, err
}
