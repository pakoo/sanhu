package bootstrap

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

const testSchema = `
CREATE TABLE funds (code TEXT PRIMARY KEY, name TEXT, fund_type TEXT, category TEXT);
`

func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func newDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(testSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func noopLog(string, ...any) {}

// 首启空库 → 下载 gzip seed → 灌库成功。
func TestEnsureSeedDownloadsAndLoads(t *testing.T) {
	seed := `INSERT INTO funds (code, name) VALUES ('000001','测试基金');`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(gzipBytes(t, seed))
	}))
	defer srv.Close()

	db := newDB(t)
	if err := EnsureSeed(context.Background(), db, nil, srv.URL+"/seed.sql.gz", noopLog); err != nil {
		t.Fatalf("EnsureSeed: %v", err)
	}

	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM funds`); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("funds = %d, want 1", n)
	}
}

// 库内已有数据 → 不再下载（指向不可达 URL 也应成功返回）。
func TestEnsureSeedIdempotent(t *testing.T) {
	db := newDB(t)
	if _, err := db.Exec(`INSERT INTO funds (code, name) VALUES ('x','x')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := EnsureSeed(context.Background(), db, nil, "http://127.0.0.1:1/unreachable.gz", noopLog); err != nil {
		t.Fatalf("EnsureSeed should skip on non-empty db, got: %v", err)
	}
}

// 无内嵌数据且无 URL → 跳过，不报错。
func TestEnsureSeedEmptyURL(t *testing.T) {
	db := newDB(t)
	if err := EnsureSeed(context.Background(), db, nil, "", noopLog); err != nil {
		t.Fatalf("EnsureSeed empty url: %v", err)
	}
}

// 首启无网络：只给内嵌 gzip 数据，也应灌库成功（交付路径的主场景）。
func TestEnsureSeedUsesEmbeddedWithoutNetwork(t *testing.T) {
	seed := `INSERT INTO funds (code, name, fund_type, category) VALUES ('000001','测试基金','股票型','mixed');`
	db := newDB(t)
	if err := EnsureSeed(context.Background(), db, gzipBytes(t, seed), "", noopLog); err != nil {
		t.Fatalf("EnsureSeed embedded: %v", err)
	}

	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM funds`); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("funds = %d, want 1", n)
	}

	// seed 里 category 是错的 mixed，灌库后应按 fund_type 归一化成 equity
	var cat string
	if err := db.Get(&cat, `SELECT category FROM funds WHERE code='000001'`); err != nil {
		t.Fatalf("category: %v", err)
	}
	if cat != "equity" {
		t.Fatalf("category = %q, want equity（股票型应归一化）", cat)
	}
}

// 显式配置 URL 时覆盖内嵌数据（内网镜像场景）。
func TestEnsureSeedURLOverridesEmbedded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(gzipBytes(t, `INSERT INTO funds (code, name) VALUES ('from-url','U');`))
	}))
	defer srv.Close()

	db := newDB(t)
	embedded := gzipBytes(t, `INSERT INTO funds (code, name) VALUES ('from-embed','E');`)
	if err := EnsureSeed(context.Background(), db, embedded, srv.URL+"/seed.sql.gz", noopLog); err != nil {
		t.Fatalf("EnsureSeed: %v", err)
	}

	var code string
	if err := db.Get(&code, `SELECT code FROM funds LIMIT 1`); err != nil {
		t.Fatalf("query: %v", err)
	}
	if code != "from-url" {
		t.Fatalf("code = %q, want from-url（URL 应覆盖内嵌）", code)
	}
}
