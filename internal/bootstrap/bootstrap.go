// Package bootstrap 负责首次启动时的初始数据装载。
//
// 设计目标（对应"交付方便"）：初始参考数据以 gzip 形式内嵌在二进制里（压缩后约 1.3MB），
// fresh 安装、数据库为空时直接解压灌库——首启零联网，不依赖 GitHub 可达性。
// 保留 SANHU_SEED_URL 作为逃生口：显式配置时改从该 URL 拉取（内网镜像 / 临时换数据）。
package bootstrap

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/fundcat"
)

// downloadTimeout 仅在显式配置了 seedURL 时生效。
const downloadTimeout = 120 * time.Second

// EnsureSeed 在首次启动时装载初始参考数据。
//
// seedGz 为内嵌的 gzip 数据；seedURL 非空时优先从网络拉取（覆盖内嵌数据）。
// 幂等：库内已有 funds 数据则直接返回，重复启动不会被重新灌库。
func EnsureSeed(ctx context.Context, db *sqlx.DB, seedGz []byte, seedURL string, logf func(string, ...any)) error {
	var fundCount int
	if err := db.GetContext(ctx, &fundCount, `SELECT COUNT(*) FROM funds`); err != nil {
		return fmt.Errorf("check seed state: %w", err)
	}
	if fundCount > 0 {
		return nil // 已有数据，无需 seed
	}

	var script string
	var err error
	if strings.TrimSpace(seedURL) != "" {
		logf("seed: 检测到空库，从 %s 下载初始数据", seedURL)
		script, err = fetchSeedSQL(ctx, seedURL)
		if err != nil {
			return fmt.Errorf("fetch seed: %w", err)
		}
	} else {
		if len(seedGz) == 0 {
			logf("seed: 无内嵌数据且未配置 SANHU_SEED_URL，空库启动")
			return nil
		}
		logf("seed: 检测到空库，解压内嵌初始数据（%.1fMB）", float64(len(seedGz))/(1<<20))
		script, err = gunzipString(seedGz)
		if err != nil {
			return fmt.Errorf("decompress embedded seed: %w", err)
		}
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin seed tx: %w", err)
	}
	if _, err := tx.ExecContext(ctx, script); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("apply seed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit seed: %w", err)
	}

	if err := db.GetContext(ctx, &fundCount, `SELECT COUNT(*) FROM funds`); err != nil {
		return fmt.Errorf("verify seed: %w", err)
	}

	// seed 是用早期代码生成的，category 全是 mixed（fund_type 是对的），按 fund_type 重算一次
	if fixed, err := fundcat.NormalizeAll(db); err != nil {
		logf("seed: 分类归一化失败（不影响启动，下次抓取会修正）: %v", err)
	} else if fixed > 0 {
		logf("seed: 修正 %d 只基金的分类", fixed)
	}

	logf("seed: 初始数据装载完成（funds=%d）", fundCount)
	return nil
}

// fetchSeedSQL 下载 seed 资产并返回 SQL 文本；URL 以 .gz 结尾时自动解压。
func fetchSeedSQL(ctx context.Context, url string) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var reader io.Reader = resp.Body
	if strings.HasSuffix(strings.ToLower(url), ".gz") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return "", fmt.Errorf("gzip reader: %w", err)
		}
		defer gz.Close()
		reader = gz
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("read seed body: %w", err)
	}
	return string(data), nil
}

func gunzipString(raw []byte) (string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()
	data, err := io.ReadAll(gz)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	return string(data), nil
}
