package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	sanhugo "github.com/pakoo/sanhu"
	"github.com/pakoo/sanhu/internal/db"
	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	database, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.RunMigrations(database, sanhugo.MigrationsFS); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return database
}

func TestNewRouterServesIndexAndHealth(t *testing.T) {
	staticFS, err := fs.Sub(sanhugo.StaticFS, "static")
	if err != nil {
		t.Fatalf("fs.Sub(static): %v", err)
	}

	router := newRouter(staticFS, false, newTestDB(t))

	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexResp := httptest.NewRecorder()
	router.ServeHTTP(indexResp, indexReq)

	if indexResp.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", indexResp.Code, http.StatusOK)
	}
	if !strings.Contains(indexResp.Body.String(), "<!DOCTYPE html>") {
		t.Fatalf("GET / body does not look like index.html")
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	healthResp := httptest.NewRecorder()
	router.ServeHTTP(healthResp, healthReq)

	if healthResp.Code != http.StatusOK {
		t.Fatalf("GET /api/health status = %d, want %d", healthResp.Code, http.StatusOK)
	}
	if !strings.Contains(healthResp.Body.String(), "ok") {
		t.Fatalf("GET /api/health body = %q", healthResp.Body.String())
	}
}
