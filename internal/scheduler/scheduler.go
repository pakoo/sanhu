// Package scheduler 定义 8 个定时任务，使用 robfig/cron/v3。
// 任务清单：
//  1. 每日 06:00 — 刷新所有基金净值（FetchNavAll，近 90 天）
//  2. 每日 06:30 — 刷新基金基本信息（FetchFundInfoAll）
//  3. 每日 07:00 — 刷新指数 PE 历史（FetchIndexPEAll）
//  4. 每日 07:30 — 重新计算所有基金评分（CalculateAllScores）
//  5. 每日 08:00 — 检查决策止盈止损触发（CheckTPSLTriggers）
//  6. 每日 08:30 — 推进所有 forward 模拟（AdvanceAllForward）
//  7. 每周日 03:00 — 刷新基金持仓数据（FetchFundHoldingsAll）
//  8. 每周日 04:00 — 刷新关注列表缺失持仓（RefreshWatchlistMissingHoldings）

package scheduler

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/decisions"
	"github.com/pakoo/sanhu/internal/fetcher"
	"github.com/pakoo/sanhu/internal/scoring"
	"github.com/pakoo/sanhu/internal/simulation"
	"github.com/robfig/cron/v3"
)

// Start 启动所有定时任务，返回 *cron.Cron 供调用方在退出时 Stop()
func Start(db *sqlx.DB) *cron.Cron {
	c := cron.New()

	// 1. 每日 06:00 — 刷新净值（近 90 天）
	c.AddFunc("0 6 * * *", func() {
		log.Println("[cron] task1: FetchNavAll start")
		if err := fetcher.FetchNavAll(db, 90); err != nil {
			log.Printf("[cron] task1 error: %v", err)
		} else {
			log.Println("[cron] task1: FetchNavAll done")
		}
	})

	// 2. 每日 06:30 — 刷新基金基本信息
	c.AddFunc("30 6 * * *", func() {
		log.Println("[cron] task2: FetchFundInfoAll start")
		if err := fetcher.FetchFundInfoAll(db); err != nil {
			log.Printf("[cron] task2 error: %v", err)
		} else {
			log.Println("[cron] task2: FetchFundInfoAll done")
		}
	})

	// 3. 每日 07:00 — 刷新指数 PE 历史
	c.AddFunc("0 7 * * *", func() {
		log.Println("[cron] task3: FetchIndexPEAll start")
		if err := fetcher.FetchIndexPEAll(db); err != nil {
			log.Printf("[cron] task3 error: %v", err)
		} else {
			log.Println("[cron] task3: FetchIndexPEAll done")
		}
	})

	// 4. 每日 07:30 — 重新计算所有基金评分
	c.AddFunc("30 7 * * *", func() {
		log.Println("[cron] task4: CalculateAllScores start")
		codes := allFundCodes(db)
		if err := scoring.CalculateAllScores(db, codes); err != nil {
			log.Printf("[cron] task4 error: %v", err)
		} else {
			log.Printf("[cron] task4: CalculateAllScores done, %d funds", len(codes))
		}
	})

	// 5. 每日 08:00 — 检查决策止盈止损
	c.AddFunc("0 8 * * *", func() {
		log.Println("[cron] task5: CheckTPSLTriggers start")
		if err := decisions.CheckTPSLTriggers(db); err != nil {
			log.Printf("[cron] task5 error: %v", err)
		} else {
			log.Println("[cron] task5: CheckTPSLTriggers done")
		}
	})

	// 6. 每日 08:30 — 推进所有 forward 模拟
	c.AddFunc("30 8 * * *", func() {
		log.Println("[cron] task6: AdvanceAllForward start")
		if err := simulation.AdvanceAllForward(db); err != nil {
			log.Printf("[cron] task6 error: %v", err)
		} else {
			log.Println("[cron] task6: AdvanceAllForward done")
		}
	})

	// 7. 每周日 03:00 — 刷新基金持仓（季报数据，每周跑一次够了）
	c.AddFunc("0 3 * * 0", func() {
		log.Println("[cron] task7: FetchFundHoldingsAll start")
		if err := fetcher.FetchFundHoldingsAll(db); err != nil {
			log.Printf("[cron] task7 error: %v", err)
		} else {
			log.Println("[cron] task7: FetchFundHoldingsAll done")
		}
	})

	// 8. 每周日 04:00 — 刷新关注列表（补抓净值曲线，让 sparkline 保持最新）
	c.AddFunc("0 4 * * 0", func() {
		log.Println("[cron] task8: watchlist nav refresh start")
		var watchCodes []string
		db.Select(&watchCodes, `SELECT code FROM watchlist`)
		for _, code := range watchCodes {
			_ = fetcher.FetchNav(db, code, 30)
		}
		log.Printf("[cron] task8: watchlist nav refresh done, %d codes", len(watchCodes))
	})

	c.Start()
	log.Println("[cron] scheduler started, 8 tasks registered")
	return c
}

// allFundCodes 取 holdings ∪ watchlist 所有基金代码
func allFundCodes(db *sqlx.DB) []string {
	var codes []string
	db.Select(&codes, `
		SELECT DISTINCT code FROM (
			SELECT code FROM holdings
			UNION
			SELECT code FROM watchlist
		)`)
	return codes
}
