package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Version is injected at build time via -ldflags.
var Version = "dev"

type Config struct {
	Port    int
	DataDir string
	Debug   bool
	Version string
	SeedURL string
}

// defaultSeedURL 指向公开仓库 release 的初始数据资产；首启空库时下载。
// 可用环境变量 SANHU_SEED_URL 覆盖（内网镜像 / 本地测试）。
const defaultSeedURL = "https://github.com/pakoo/sanhu/releases/latest/download/seed.sql.gz"

func Load() (*Config, error) {
	port := 8000
	if rawPort := strings.TrimSpace(os.Getenv("SANHU_PORT")); rawPort != "" {
		parsedPort, err := strconv.Atoi(rawPort)
		if err != nil {
			return nil, fmt.Errorf("parse SANHU_PORT: %w", err)
		}
		port = parsedPort
	}

	dataDir := strings.TrimSpace(os.Getenv("SANHU_DATA_DIR"))
	if dataDir == "" {
		dataDir = "./data"
	}

	debug := false
	if rawDebug := strings.TrimSpace(os.Getenv("SANHU_DEBUG")); rawDebug != "" {
		parsedDebug, err := strconv.ParseBool(rawDebug)
		if err != nil {
			return nil, fmt.Errorf("parse SANHU_DEBUG: %w", err)
		}
		debug = parsedDebug
	}

	seedURL := strings.TrimSpace(os.Getenv("SANHU_SEED_URL"))
	if seedURL == "" {
		seedURL = defaultSeedURL
	}

	return &Config{
		Port:    port,
		DataDir: dataDir,
		Debug:   debug,
		Version: Version,
		SeedURL: seedURL,
	}, nil
}
