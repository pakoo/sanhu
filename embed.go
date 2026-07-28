package sanhugo

import "embed"

// StaticFS embeds the frontend static assets.
//
//go:embed static
var StaticFS embed.FS

// PromptsFS embeds AI prompt templates.
//
//go:embed prompts
var PromptsFS embed.FS

// MigrationsFS embeds SQL migration files.
//
//go:embed migrations
var MigrationsFS embed.FS

// SeedSQLGz embeds the gzipped initial reference dataset (~1.3MB compressed).
// 内嵌而非下载：首启零联网，避免 GitHub 不可达时用户看到空库。
//
//go:embed seed/seed.sql.gz
var SeedSQLGz []byte
