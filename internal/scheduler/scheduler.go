// Package scheduler 定义 10 个定时任务，使用 robfig/cron/v3。
// 任务清单：
//  1. 每日 06:00 — 刷新所有基金净值（FetchNavAll，近 90 天）
//  2. 每日 06:30 — 刷新基金基本信息（FetchFundInfoAll）
//  3. 每日 07:00 — 刷新指数 PE 历史（FetchIndexPEAll）
//  4. 每日 07:30 — 重新计算所有基金评分（CalculateAllScores）
//  5. 每日 08:00 — 检查决策止盈止损触发（CheckTPSLTriggers）
//  6. 每日 08:30 — 推进所有 forward 模拟（AdvanceAllForward）
//  7. 每周日 03:00 — 刷新基金持仓数据（FetchFundHoldingsAll）
//  8. 每周日 04:00 — 刷新关注列表缺失持仓（RefreshWatchlistMissingHoldings）
//  9. 交易时段每 30 分钟 — 板块资金流榜单快照并落库
// 10. 每日 19:00 — 板块资金流收盘落定 + 热门板块分时留档

package scheduler

import (
	"log"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/decisions"
	"github.com/pakoo/sanhu/internal/fetcher"
	"github.com/pakoo/sanhu/internal/scoring"
	"github.com/pakoo/sanhu/internal/sectorflow"
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

	// 9. 交易时段内每 30 分钟 — 刷新板块资金流榜单并落库
	//
	// cron 只管在 :15 和 :45 唤醒，具体跑不跑由 InTradingSession 判断 ——
	// 交易时段是「上午 09:45–11:35 + 下午 13:00–15:05」这种不规则区间，
	// 塞进 cron 表达式会写成一长串难以校对的东西。
	//
	// 每次都落库（而不是只在收盘后写一次）：sanhu 是桌面程序，用户想开才开，
	// 只在 19:00 写的话「今天开过但晚上没开」这一天就永久没数据，而东财历史
	// 只有 120 天、补不回来。详见 sectorflow.Snapshot 的注释。
	c.AddFunc("15,45 9-15 * * 1-5", func() {
		if !sectorflow.InTradingSession(time.Now()) {
			return
		}
		for _, kind := range []fetcher.SectorKind{fetcher.KindIndustry, fetcher.KindConcept} {
			n, err := sectorflow.Snapshot(db, kind)
			if err != nil {
				// 限流是预期内的常态，不是异常。这里只记一行，**不重试** ——
				// 重试会延长封锁（实测），下一个 30 分钟周期自然会再来。
				log.Printf("[cron] task9: %s 快照跳过: %v", kind, err)
				continue
			}
			log.Printf("[cron] task9: %s 快照完成，%d 个板块", kind, n)
		}
	})

	// 10. 每日 19:00 — 收盘后落定，并存热门板块的分时曲线
	//
	// 为什么是 19:00 而不是 15:05：实测 8-14 的数据时间戳是 15:39:31，
	// 收盘 40 分钟后东财还在更新。定早了会把未结算完的中间值当成收盘值存进库。
	c.AddFunc("0 19 * * 1-5", func() {
		for _, kind := range []fetcher.SectorKind{fetcher.KindIndustry, fetcher.KindConcept} {
			if n, err := sectorflow.Snapshot(db, kind); err != nil {
				log.Printf("[cron] task10: %s 收盘快照失败: %v", kind, err)
			} else {
				log.Printf("[cron] task10: %s 收盘快照完成，%d 个板块", kind, n)
			}
		}
		// 分时只存行业板块的热门前 20：概念板块和行业板块高度重叠，
		// 两边都存等于把同一批钱的曲线存两遍，不值当。
		if n, err := sectorflow.SnapshotMinuteTop(db, fetcher.KindIndustry); err != nil {
			log.Printf("[cron] task10: 分时留档失败: %v", err)
		} else {
			log.Printf("[cron] task10: 分时留档完成，%d 个板块", n)
		}
	})

	c.Start()
	log.Println("[cron] scheduler started, 10 tasks registered")
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
