package main

// 这两个变量由 GitHub Actions 构建时通过 -ldflags 注入：
//
//	go build -ldflags "-X main.version=v1.0.0 -X main.gitTag=v1.0.0" ./cmd/sanhu/
//
// 本地开发时保持 "dev"。
var (
	version = "dev"
	gitTag  = ""
)
