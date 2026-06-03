package db

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

const bootstrapMigrationsTable = `
CREATE TABLE IF NOT EXISTS user_migrations (
	filename TEXT PRIMARY KEY,
	applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

func Open(dataDir string) (*sqlx.DB, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "sanhu.db")
	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite db: %w", err)
	}

	if _, err := db.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}

	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	return db, nil
}

func RunMigrations(db *sqlx.DB, migrationsFS fs.FS) error {
	if _, err := db.Exec(bootstrapMigrationsTable); err != nil {
		return fmt.Errorf("ensure user_migrations: %w", err)
	}

	var filenames []string
	if err := fs.WalkDir(migrationsFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".sql") {
			filenames = append(filenames, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk migrations: %w", err)
	}

	sort.Strings(filenames)

	for _, filename := range filenames {
		var applied bool
		if err := db.Get(&applied, `SELECT EXISTS(SELECT 1 FROM user_migrations WHERE filename = ?)`, filename); err != nil {
			return fmt.Errorf("check migration %s: %w", filename, err)
		}
		if applied {
			continue
		}

		script, err := fs.ReadFile(migrationsFS, filename)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", filename, err)
		}

		tx, err := db.Beginx()
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", filename, err)
		}

		if _, err := tx.Exec(string(script)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("execute migration %s: %w", filename, err)
		}

		if _, err := tx.Exec(`INSERT INTO user_migrations (filename) VALUES (?)`, filename); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", filename, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", filename, err)
		}
	}

	return nil
}
