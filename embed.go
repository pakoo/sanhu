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
