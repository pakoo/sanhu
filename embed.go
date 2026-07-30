package sanhugo

import "embed"

// StaticFS embeds the frontend static assets.
//
//go:embed static
var StaticFS embed.FS

// AI 提示词曾经外置在 prompts/*.md 里，由占位符替换渲染。
// 按产品稿 Q5=C 已全部搬进 internal/aiprompt 的 Go 源码，
// 所以这里不再需要 PromptsFS —— 留着只会让人以为模板还在用。

// MigrationsFS embeds SQL migration files.
//
//go:embed migrations
var MigrationsFS embed.FS

// SeedSQLGz embeds the gzipped initial reference dataset (~1.3MB compressed).
// 内嵌而非下载：首启零联网，避免 GitHub 不可达时用户看到空库。
//
//go:embed seed/seed.sql.gz
var SeedSQLGz []byte
