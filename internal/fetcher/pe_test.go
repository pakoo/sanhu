package fetcher

import (
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func openPETestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS index_pe_history (
			index_code TEXT NOT NULL,
			date TEXT NOT NULL,
			pe REAL,
			pb REAL,
			PRIMARY KEY (index_code, date)
		)
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func TestCalcPEPercentile_Basic(t *testing.T) {
	db := openPETestDB(t)
	defer db.Close()

	// Insert 10 data points, PE values 10..19
	// Current (most recent) PE = 19 → should be 100th percentile (highest)
	dates := []string{
		"2020-01-01", "2020-04-01", "2020-07-01", "2020-10-01",
		"2021-01-01", "2021-04-01", "2021-07-01", "2021-10-01",
		"2022-01-01", "2025-01-01", // most recent = highest PE
	}
	for i, d := range dates {
		db.Exec(`INSERT INTO index_pe_history (index_code, date, pe, pb) VALUES (?, ?, ?, ?)`,
			"000300", d, float64(10+i), 1.5)
	}

	pct, err := CalcPEPercentile(db, "000300", 10)
	if err != nil {
		t.Fatalf("CalcPEPercentile: %v", err)
	}
	// current PE=19, all 10 records are <=19, so percentile = 100
	if pct != 100.0 {
		t.Errorf("expected 100.0, got %f", pct)
	}
}

func TestCalcPEPercentile_Median(t *testing.T) {
	db := openPETestDB(t)
	defer db.Close()

	// 10 data points, PE 10-19; current (latest) PE = 14 (median)
	dates := []string{
		"2025-01-01", // most recent, PE=14
		"2020-01-01", "2020-04-01", "2020-07-01", "2020-10-01",
		"2021-01-01", "2021-04-01", "2021-07-01", "2021-10-01",
		"2022-01-01",
	}
	peVals := []float64{14, 10, 11, 12, 13, 15, 16, 17, 18, 19}
	for i, d := range dates {
		db.Exec(`INSERT INTO index_pe_history (index_code, date, pe, pb) VALUES (?, ?, ?, ?)`,
			"000300", d, peVals[i], 1.5)
	}

	pct, err := CalcPEPercentile(db, "000300", 10)
	if err != nil {
		t.Fatalf("CalcPEPercentile: %v", err)
	}
	// PE=14: 5 records (10,11,12,13,14) are <=14 out of 10 → 50%
	if pct != 50.0 {
		t.Errorf("expected 50.0, got %f", pct)
	}
}

func TestCalcPEPercentile_NoData(t *testing.T) {
	db := openPETestDB(t)
	defer db.Close()

	_, err := CalcPEPercentile(db, "999999", 5)
	if err == nil {
		t.Fatal("expected error for unknown index code")
	}
}

func TestCalcPEPercentile_ZeroPESkipped(t *testing.T) {
	db := openPETestDB(t)
	defer db.Close()

	// PE=0 entries should be ignored
	db.Exec(`INSERT INTO index_pe_history VALUES ('000300', '2025-01-01', 15, 1.5)`)
	db.Exec(`INSERT INTO index_pe_history VALUES ('000300', '2024-01-01', 0, 1.5)`)  // should skip
	db.Exec(`INSERT INTO index_pe_history VALUES ('000300', '2023-01-01', 10, 1.5)`)

	pct, err := CalcPEPercentile(db, "000300", 5)
	if err != nil {
		t.Fatalf("CalcPEPercentile: %v", err)
	}
	// current PE=15, valid records: [15, 10] (PE=0 excluded)
	// both <= 15 → 100%
	if pct != 100.0 {
		t.Errorf("expected 100.0, got %f", pct)
	}
}
