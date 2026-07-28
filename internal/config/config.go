package config

import (
	"fmt"
	"os"
	"path/filepath"
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

// 初始数据默认走二进制内嵌（见 embed.go SeedSQLGz），首启不联网。
// SANHU_SEED_URL 非空时改为从该 URL 下载，覆盖内嵌数据——留给内网镜像 / 临时换数据集。

// defaultDataDir 决定数据库落在哪里。
//
// 原来是 "./data"（相对当前工作目录），单文件程序里这是个雷：从不同目录启动就换了一个库，
// macOS 从 Finder 双击时 cwd 是 "/"，建库直接失败退出。
// 现在按「可执行文件同级 data/」——解压到哪就在哪存数据，符合绿色软件的直觉，也便于用户备份/搬迁。
//
// 两个例外：
//   - go run：可执行文件在系统临时目录，回退到 ./data，保持开发时用仓库里的库
//   - 目录不可写（装到 /Applications、只读挂载等）：回退到用户配置目录
func defaultDataDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "./data"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	exeDir := filepath.Dir(exe)

	// go run 把二进制丢在临时目录，此时用 ./data 而不是临时目录
	if tmp, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		if rel, err := filepath.Rel(tmp, exeDir); err == nil && !strings.HasPrefix(rel, "..") {
			return "./data"
		}
	}

	// macOS .app 包：数据不能写进 bundle 内部。
	// 一来 bundle 应当视为只读，二来用新版本覆盖 .app 时会连数据一起删掉。
	// 统一放到 ~/Library/Application Support/sanhu。
	if strings.Contains(exeDir, ".app/Contents/MacOS") {
		if cfgDir, err := os.UserConfigDir(); err == nil {
			return filepath.Join(cfgDir, "sanhu")
		}
	}

	candidate := filepath.Join(exeDir, "data")
	if err := os.MkdirAll(candidate, 0755); err == nil && isWritable(candidate) {
		return candidate
	}

	// 兜底：~/Library/Application Support/sanhu、%AppData%\sanhu、~/.config/sanhu
	if cfgDir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(cfgDir, "sanhu")
	}
	return "./data"
}

func isWritable(dir string) bool {
	probe := filepath.Join(dir, ".write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(probe)
	return true
}

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
		dataDir = defaultDataDir()
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

	return &Config{
		Port:    port,
		DataDir: dataDir,
		Debug:   debug,
		Version: Version,
		SeedURL: seedURL,
	}, nil
}
