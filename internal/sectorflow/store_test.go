package sectorflow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/fetcher"
	_ "modernc.org/sqlite"
)

// 直接跑真实的 005 迁移建表，而不是在测试里另写一份 schema ——
// 手抄的 schema 会和迁移文件慢慢漂移，那样测试就失去了守护作用。
func testDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存库: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", "005_sector_fund_flow.sql"))
	if err != nil {
		t.Fatalf("读迁移文件: %v", err)
	}
	if _, err := db.Exec(string(sql)); err != nil {
		t.Fatalf("执行迁移 005: %v", err)
	}
	return db
}

func sample() []fetcher.SectorFlow {
	return []fetcher.SectorFlow{
		{Code: "BK1215", Name: "通信", Kind: "industry", Price: 7736.89, ChangePct: 2.06,
			MainNet: 13906735360, MainPct: 6.86, XLNet: 12222623488, XLPct: 6.03,
			LGNet: 1684111872, LGPct: 0.83, MDNet: -7204405760, MDPct: -3.55,
			SMNet: -6666871552, SMPct: -3.29, TopStock: "亨通光电", TopStockCode: "600487"},
		{Code: "BK1626", Name: "稀土", Kind: "industry", Price: 10383.28, ChangePct: 5.82,
			MainNet: 1494460832, MainPct: 10.97, TopStock: "中国稀土", TopStockCode: "000831"},
		{Code: "BK9999", Name: "某流出板块", Kind: "industry", MainNet: -9600000000},
	}
}

func TestSaveAndLoadRankRoundTrip(t *testing.T) {
	db := testDB(t)
	if err := SaveDaily(db, "2026-08-14", sample()); err != nil {
		t.Fatalf("SaveDaily: %v", err)
	}

	rows, date, err := LoadRank(db, fetcher.KindIndustry, "")
	if err != nil {
		t.Fatalf("LoadRank: %v", err)
	}
	if date != "2026-08-14" {
		t.Errorf("date 传空应回落到库里最新一天，得到 %q", date)
	}
	if len(rows) != 3 {
		t.Fatalf("应读回 3 条，得到 %d", len(rows))
	}
	// 按主力净额降序
	if rows[0].Code != "BK1215" || rows[len(rows)-1].Code != "BK9999" {
		t.Errorf("应按主力净额降序，得到 %s … %s", rows[0].Code, rows[len(rows)-1].Code)
	}

	// 逐字段核对，防止 INSERT 的占位符顺序写错 —— 那种错不会报错，只会把
	// 中单写进小单列，图上看起来还挺正常。
	g := rows[0]
	for _, c := range []struct {
		name     string
		got, exp float64
	}{
		{"main_net", g.MainNet, 13906735360}, {"main_pct", g.MainPct, 6.86},
		{"xl_net", g.XLNet, 12222623488}, {"xl_pct", g.XLPct, 6.03},
		{"lg_net", g.LGNet, 1684111872}, {"lg_pct", g.LGPct, 0.83},
		{"md_net", g.MDNet, -7204405760}, {"md_pct", g.MDPct, -3.55},
		{"sm_net", g.SMNet, -6666871552}, {"sm_pct", g.SMPct, -3.29},
		{"close_point", g.Price, 7736.89}, {"change_pct", g.ChangePct, 2.06},
	} {
		if c.got != c.exp {
			t.Errorf("%s 应为 %v，得到 %v —— 检查 INSERT 占位符顺序", c.name, c.exp, c.got)
		}
	}
	if g.TopStock != "亨通光电" || g.TopStockCode != "600487" {
		t.Errorf("龙头股字段错了：%s/%s", g.TopStock, g.TopStockCode)
	}
}

func TestSaveDailyIsIdempotent(t *testing.T) {
	db := testDB(t)
	// 盘中每 30 分钟写一次同一天，必须覆盖而不是累积成重复行
	if err := SaveDaily(db, "2026-08-14", sample()); err != nil {
		t.Fatalf("首次写入: %v", err)
	}
	updated := sample()
	updated[0].MainNet = 99999
	if err := SaveDaily(db, "2026-08-14", updated); err != nil {
		t.Fatalf("覆盖写入: %v", err)
	}

	rows, _, err := LoadRank(db, fetcher.KindIndustry, "2026-08-14")
	if err != nil {
		t.Fatalf("LoadRank: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("重复写入不该产生新行，应仍为 3 条，得到 %d", len(rows))
	}
	var got float64
	for _, r := range rows {
		if r.Code == "BK1215" {
			got = r.MainNet
		}
	}
	if got != 99999 {
		t.Errorf("第二次写入应覆盖旧值，期望 99999，得到 %v", got)
	}
}

func TestLoadRankOnEmptyDBIsNotAnError(t *testing.T) {
	db := testDB(t)
	rows, date, err := LoadRank(db, fetcher.KindIndustry, "")
	if err != nil {
		t.Fatalf("空库不该报错（首次启动就是这个状态）: %v", err)
	}
	if len(rows) != 0 || date != "" {
		t.Errorf("空库应返回空结果，得到 %d 条 / date=%q", len(rows), date)
	}
}

func TestLoadDailyReturnsAscendingAndRespectsLimit(t *testing.T) {
	db := testDB(t)
	for _, d := range []string{"2026-08-10", "2026-08-11", "2026-08-12", "2026-08-13"} {
		if err := SaveDaily(db, d, []fetcher.SectorFlow{
			{Code: "BK1215", Name: "通信", Kind: "industry", MainNet: 100},
		}); err != nil {
			t.Fatalf("写入 %s: %v", d, err)
		}
	}

	all, err := LoadDaily(db, "BK1215", 0)
	if err != nil {
		t.Fatalf("LoadDaily: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("应读回 4 天，得到 %d", len(all))
	}
	if all[0].Date != "2026-08-10" || all[3].Date != "2026-08-13" {
		t.Errorf("画图要升序，得到 %s → %s", all[0].Date, all[3].Date)
	}

	// limit 取的是**最近** N 天，然后仍按升序返回
	last2, err := LoadDaily(db, "BK1215", 2)
	if err != nil {
		t.Fatalf("LoadDaily(limit=2): %v", err)
	}
	if len(last2) != 2 || last2[0].Date != "2026-08-12" || last2[1].Date != "2026-08-13" {
		t.Errorf("limit=2 应返回最近两天且升序，得到 %+v", last2)
	}
}

func TestSaveAndLoadMinute(t *testing.T) {
	db := testDB(t)
	pts := []fetcher.FlowPoint{
		{Time: "09:31", MainNet: -92041408, XLNet: -47460592, LGNet: -44580816, MDNet: 89680960, SMNet: 1382224},
		{Time: "09:32", MainNet: -80000000, XLNet: -40000000, LGNet: -40000000},
		{Time: "15:00", MainNet: 13906735334, XLNet: 12222623417, LGNet: 1684111917},
	}
	if err := SaveMinute(db, "2026-08-14", "BK1215", pts); err != nil {
		t.Fatalf("SaveMinute: %v", err)
	}
	got, err := LoadMinute(db, "BK1215", "2026-08-14")
	if err != nil {
		t.Fatalf("LoadMinute: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("应读回 3 个点，得到 %d", len(got))
	}
	if got[0].Time != "09:31" || got[2].Time != "15:00" {
		t.Errorf("应按时间升序，得到 %s … %s", got[0].Time, got[2].Time)
	}
	if got[0].MainNet != -92041408 || got[0].MDNet != 89680960 {
		t.Errorf("首点字段错位：%+v", got[0])
	}
}

func TestSaveRejectsEmptyInput(t *testing.T) {
	db := testDB(t)
	// 空输入多半意味着「被限流拿到空响应」。静默写入空会把
	// 「今天抓失败」记成「今天没有板块」，图表变空白还查不出原因。
	if err := SaveDaily(db, "2026-08-14", nil); err == nil {
		t.Error("SaveDaily 收到空数据应当报错")
	}
	if err := SaveMinute(db, "2026-08-14", "BK1215", nil); err == nil {
		t.Error("SaveMinute 收到空数据应当报错")
	}
}

func TestPurgeBefore(t *testing.T) {
	db := testDB(t)
	for _, d := range []string{"2023-01-01", "2026-08-13", "2026-08-14"} {
		_ = SaveDaily(db, d, []fetcher.SectorFlow{{Code: "BK1", Name: "甲", Kind: "industry"}})
		_ = SaveMinute(db, d, "BK1", []fetcher.FlowPoint{{Time: "09:31"}})
	}
	n, err := PurgeBefore(db, "2026-01-01")
	if err != nil {
		t.Fatalf("PurgeBefore: %v", err)
	}
	if n != 2 { // 2023 那天的日线 1 行 + 分时 1 行
		t.Errorf("应删掉 2 行（日线+分时各 1），得到 %d", n)
	}
	rows, _, _ := LoadRank(db, fetcher.KindIndustry, "2026-08-14")
	if len(rows) != 1 {
		t.Errorf("清理不该误伤保留期内的数据，剩 %d 条", len(rows))
	}
}

func TestLatestMinuteDate(t *testing.T) {
	db := testDB(t)
	if d, err := LatestMinuteDate(db, "BK1215"); err != nil || d != "" {
		t.Fatalf("空库应返回空串且不报错，得到 %q / %v", d, err)
	}
	for _, day := range []string{"2026-08-12", "2026-08-14", "2026-08-13"} {
		if err := SaveMinute(db, day, "BK1215", []fetcher.FlowPoint{{Time: "09:31", MainNet: 1}}); err != nil {
			t.Fatalf("写入 %s: %v", day, err)
		}
	}
	// 别的板块不该干扰
	_ = SaveMinute(db, "2026-08-15", "BK9999", []fetcher.FlowPoint{{Time: "09:31"}})

	got, err := LatestMinuteDate(db, "BK1215")
	if err != nil {
		t.Fatalf("LatestMinuteDate: %v", err)
	}
	if got != "2026-08-14" {
		t.Errorf("应返回该板块最近有记录的交易日 2026-08-14，得到 %q", got)
	}
}

// ── 回放数据 ────────────────────────────────────────────────────────────────

func TestLoadReplayBuildsSharedTimeAxis(t *testing.T) {
	db := testDB(t)
	_ = SaveDaily(db, "2026-08-14", []fetcher.SectorFlow{
		{Code: "BK1215", Name: "通信", Kind: "industry", MainNet: 139e8},
		{Code: "BK0428", Name: "电力", Kind: "industry", MainNet: -46e8},
	})
	_ = SaveMinute(db, "2026-08-14", "BK1215", []fetcher.FlowPoint{
		{Time: "09:31", MainNet: -6e8}, {Time: "09:32", MainNet: 2e8}, {Time: "15:00", MainNet: 139e8},
	})
	_ = SaveMinute(db, "2026-08-14", "BK0428", []fetcher.FlowPoint{
		{Time: "09:31", MainNet: -1e8}, {Time: "09:32", MainNet: -3e8}, {Time: "15:00", MainNet: -46e8},
	})

	r, err := LoadReplay(db, "")
	if err != nil {
		t.Fatalf("LoadReplay: %v", err)
	}
	if r.Date != "2026-08-14" {
		t.Errorf("date 传空应回落到最近有留档的日子，得到 %q", r.Date)
	}
	if len(r.Times) != 3 || r.Times[0] != "09:31" || r.Times[2] != "15:00" {
		t.Fatalf("时间轴应为 3 个升序点，得到 %v", r.Times)
	}
	if len(r.Sectors) != 2 {
		t.Fatalf("应有 2 个板块，得到 %d", len(r.Sectors))
	}
	// 按收盘净额绝对值降序：通信 139 亿 > 电力 46 亿
	if r.Sectors[0].Code != "BK1215" {
		t.Errorf("应按收盘净额绝对值降序，首位得到 %s", r.Sectors[0].Code)
	}
	if r.Sectors[0].Name != "通信" {
		t.Errorf("板块名应从日线表带出来，得到 %q", r.Sectors[0].Name)
	}
	if len(r.Sectors[0].Values) != len(r.Times) {
		t.Errorf("每个板块的数值个数必须与共享时间轴一致：%d vs %d",
			len(r.Sectors[0].Values), len(r.Times))
	}
	if r.Sectors[0].Values[0] != -6e8 || r.Sectors[0].Values[2] != 139e8 {
		t.Errorf("数值错位：%v", r.Sectors[0].Values[:3])
	}
}

func TestLoadReplayFillsGapsWithPreviousValue(t *testing.T) {
	// 分时是**累计值**：某个点缺失不代表归零。补 0 会在曲线上画出一道假的深谷，
	// 而回放里这道谷看起来就像「资金突然撤走又回来」，是彻底的误导。
	db := testDB(t)
	_ = SaveDaily(db, "2026-08-14", []fetcher.SectorFlow{
		{Code: "A", Name: "甲", Kind: "industry", MainNet: 30},
		{Code: "B", Name: "乙", Kind: "industry", MainNet: 20},
	})
	_ = SaveMinute(db, "2026-08-14", "A", []fetcher.FlowPoint{
		{Time: "09:31", MainNet: 10}, {Time: "09:32", MainNet: 20}, {Time: "09:33", MainNet: 30},
	})
	// 乙缺了 09:32
	_ = SaveMinute(db, "2026-08-14", "B", []fetcher.FlowPoint{
		{Time: "09:31", MainNet: 15}, {Time: "09:33", MainNet: 20},
	})

	r, _ := LoadReplay(db, "2026-08-14")
	var b *ReplaySectors
	for i := range r.Sectors {
		if r.Sectors[i].Code == "B" {
			b = &r.Sectors[i]
		}
	}
	if b == nil {
		t.Fatal("没读到板块 B")
	}
	if len(b.Values) != 3 {
		t.Fatalf("应补齐到 3 个点，得到 %d", len(b.Values))
	}
	if b.Values[1] != 15 {
		t.Errorf("缺失点应沿用前值 15（累计值不会归零），得到 %v", b.Values[1])
	}
}

func TestLoadReplayEmptyDBIsNotAnError(t *testing.T) {
	db := testDB(t)
	r, err := LoadReplay(db, "")
	if err != nil {
		t.Fatalf("空库不该报错: %v", err)
	}
	if r != nil && len(r.Sectors) > 0 {
		t.Errorf("空库应返回空结果，得到 %+v", r)
	}
}

func TestReplayDates(t *testing.T) {
	db := testDB(t)
	for _, d := range []string{"2026-08-12", "2026-08-14", "2026-08-13"} {
		_ = SaveMinute(db, d, "BK1", []fetcher.FlowPoint{{Time: "09:31", MainNet: 1}})
	}
	got, err := ReplayDates(db, 10)
	if err != nil {
		t.Fatalf("ReplayDates: %v", err)
	}
	want := []string{"2026-08-14", "2026-08-13", "2026-08-12"}
	if len(got) != 3 {
		t.Fatalf("应返回 3 个日期，得到 %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("日期应倒序（最近的在前），得到 %v", got)
			break
		}
	}
}
