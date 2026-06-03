// Package bootstrap 负责首次启动时的初始数据装载。
//
// 设计目标（对应"交付方便"）：二进制本身不内嵌 ~11MB 的参考数据，
// 而是在 fresh 安装、数据库为空时，从 release 资产下载 seed.sql.gz 灌入。
// 这样 release 体积小、初始数据可独立更新。
package bootstrap

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// downloadTimeout 给初始数据下载一个宽松上限（seed 约 1MB，弱网也够）。
const downloadTimeout = 120 * time.Second

// EnsureSeed 在首次启动时装载初始参考数据。
//
// 幂等：库内已有 funds 数据则直接返回，老用户与重复启动都不会被重新灌库。
// 容错：下载或灌库失败返回 error，但调用方应作为非致命处理——服务仍可空库启动，
// 下次启动（funds 仍为空）会自动重试。
func EnsureSeed(ctx context.Context, db *sqlx.DB, seedURL string, logf func(string, ...any)) error {
	var fundCount int
	if err := db.GetContext(ctx, &fundCount, `SELECT COUNT(*) FROM funds`); err != nil {
		return fmt.Errorf("check seed state: %w", err)
	}
	if fundCount > 0 {
		return nil // 已有数据，无需 seed
	}
	if strings.TrimSpace(seedURL) == "" {
		logf("seed: 未配置 SANHU_SEED_URL，跳过初始数据下载（空库启动）")
		return nil
	}

	logf("seed: 检测到空库，开始从 %s 下载初始数据", seedURL)
	script, err := fetchSeedSQL(ctx, seedURL)
	if err != nil {
		return fmt.Errorf("fetch seed: %w", err)
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
