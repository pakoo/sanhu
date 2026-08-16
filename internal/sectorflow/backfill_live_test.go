package sectorflow

import (
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/fetcher"
	_ "modernc.org/sqlite"
)

// 手动补抓当日分时留档。**默认跳过**，需要时：
//
//	SANHU_BACKFILL=/path/to/sanhu.db go test ./internal/sectorflow/ -run Backfill -v -timeout 20m
//
// 存在的理由：收盘留档由 19:00 的定时任务负责，但功能刚上线、或那天程序没开着，
// 就会缺一天。而东财**只提供当日分时**，错过当天就永久补不回来 ——
// 所以需要一个「今天之内还来得及」的手动补救入口。
//
// 它跑的是**生产同一套代码**（SnapshotMinuteTop），不是另写一份逻辑，
// 这样补出来的数据和定时任务存的完全一致。
//
// 注意耗时：Top50 串行 + 1.5 秒间隔 ≈ 75 秒，别中途打断。
func TestBackfillMinuteToday(t *testing.T) {
	dsn := os.Getenv("SANHU_BACKFILL")
	if dsn == "" {
		t.Skip("需要 SANHU_BACKFILL=<sanhu.db 路径> 才跑（会真实请求东财，约 75 秒）")
	}
	db, err := sqlx.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// 先补一次榜单，保证日线表里有这些板块的名字（回放要靠它取名）
	if n, err := Snapshot(db, fetcher.KindIndustry); err != nil {
		t.Fatalf("榜单快照失败: %v", err)
	} else {
		t.Logf("榜单已落库：%d 个板块", n)
	}

	n, err := SnapshotMinuteTop(db, fetcher.KindIndustry)
	if err != nil {
		t.Fatalf("分时留档失败: %v", err)
	}
	t.Logf("分时留档完成：%d 个板块", n)
	if n < MinuteKeepTop/2 {
		t.Errorf("只成功了 %d 个（目标 %d）—— 多半是中途被限流，隔一会儿再跑一次",
			n, MinuteKeepTop)
	}
}
