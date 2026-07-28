package fetcher

import (
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE funds (code TEXT PRIMARY KEY, name TEXT, category TEXT);
CREATE TABLE fund_holdings (code TEXT, stock_code TEXT, stock_name TEXT, weight REAL, industry TEXT, report_date TEXT, PRIMARY KEY(code,stock_code,report_date));
CREATE TABLE fund_industry (code TEXT, industry TEXT, weight REAL, report_date TEXT, PRIMARY KEY(code,industry,report_date));
`

func TestResolveStockIndustryUsesDBBeforeNetwork(t *testing.T) {
	db, _ := sqlx.Open("sqlite", ":memory:")
	defer db.Close()
	db.MustExec(schema)
	// 另一只基金早就记过这只股票的行业
	db.MustExec(`INSERT INTO fund_holdings VALUES ('000001','300750','宁德时代',5.0,'电池','2025-12-31')`)

	industryMemo = map[string]string{} // 清掉进程缓存，确保走库
	got := resolveStockIndustry(db, "300750")
	if got != "电池" {
		t.Fatalf("期望从库里复用到「电池」，得到 %q（说明白白发了一次网络请求）", got)
	}
}

func TestAggregateFundIndustry(t *testing.T) {
	db, _ := sqlx.Open("sqlite", ":memory:")
	defer db.Close()
	db.MustExec(schema)
	db.MustExec(`INSERT INTO funds VALUES ('006195','测试基金','equity')`)
	db.MustExec(`
		INSERT INTO fund_holdings VALUES
		('006195','300750','宁德时代',3.0,'电池','2026-06-30'),
		('006195','002460','赣锋锂业',2.0,'电池','2026-06-30'),
		('006195','000725','京东方A',1.5,'光学光电子','2026-06-30'),
		('006195','600519','贵州茅台',1.0,NULL,'2026-06-30'),
		('006195','300014','亿纬锂能',9.9,'电池','2025-12-31')`)

	if err := AggregateFundIndustry(db, "006195"); err != nil {
		t.Fatalf("AggregateFundIndustry: %v", err)
	}

	var rows []struct {
		Industry   string  `db:"industry"`
		Weight     float64 `db:"weight"`
		ReportDate string  `db:"report_date"`
	}
	db.Select(&rows, `SELECT industry, weight, report_date FROM fund_industry WHERE code='006195'`)

	got := map[string]float64{}
	for _, r := range rows {
		if r.ReportDate != "2026-06-30" {
			t.Errorf("聚合了非最新一期的数据: %s", r.ReportDate)
		}
		got[r.Industry] = r.Weight
	}
	if got["电池"] != 5.0 {
		t.Errorf("电池 = %v，期望 5.0（3.0+2.0，且不含上一期的 9.9）", got["电池"])
	}
	if got["光学光电子"] != 1.5 {
		t.Errorf("光学光电子 = %v，期望 1.5", got["光学光电子"])
	}
	if len(got) != 2 {
		t.Errorf("期望 2 个行业（行业为空的那条应被跳过），得到 %d: %v", len(got), got)
	}
}

func TestAggregateFundIndustrySkipsBond(t *testing.T) {
	db, _ := sqlx.Open("sqlite", ":memory:")
	defer db.Close()
	db.MustExec(schema)
	db.MustExec(`INSERT INTO funds VALUES ('007171','某债基','bond')`)
	db.MustExec(`INSERT INTO fund_holdings VALUES ('007171','300750','宁德时代',3.0,'电池','2026-06-30')`)

	if err := AggregateFundIndustry(db, "007171"); err != nil {
		t.Fatalf("AggregateFundIndustry: %v", err)
	}
	var n int
	db.Get(&n, `SELECT COUNT(*) FROM fund_industry`)
	if n != 0 {
		t.Errorf("债基不该做行业穿透，却写了 %d 行", n)
	}
}
