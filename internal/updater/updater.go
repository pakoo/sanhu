// Package updater 实现 exe 自更新：
//  1. CheckLatestRelease — 查询 GitHub releases API，返回最新版本号和下载 URL
//  2. NeedsUpdate — 比较当前版本与最新版本
//  3. DownloadAndApply — 下载新 exe，Windows 用批处理脚本完成重命名+重启
//
// Windows 自更新原理（running exe 不能被覆盖但可以被重命名）：
//
//	下载 → sanhu.exe.new
//	写 _update.bat：等 2 秒 → move sanhu.exe.new sanhu.exe → start sanhu.exe
//	启动 _update.bat（detached）→ 当前进程 os.Exit(0)
package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	repoOwner    = "pakoo"
	repoName     = "sanhu"
	apiLatestURL = "https://api.github.com/repos/" + repoOwner + "/" + repoName + "/releases/latest"
)

// ReleaseInfo 最新 Release 信息
type ReleaseInfo struct {
	TagName    string  `json:"tag_name"`
	HTMLURL    string  `json:"html_url"`
	DownloadURL string // 填充后的平台对应下载 URL
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

// CheckLatestRelease 查询 GitHub API，返回最新 Release 信息
func CheckLatestRelease() (*ReleaseInfo, error) {
	req, _ := http.NewRequest(http.MethodGet, apiLatestURL, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "sanhu-updater")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("check release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API %d", resp.StatusCode)
	}

	var rel struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	info := &ReleaseInfo{
		TagName: rel.TagName,
		HTMLURL: rel.HTMLURL,
	}

	// 匹配当前平台的下载资产
	assetSuffix := platformAssetSuffix()
	for _, a := range rel.Assets {
		if strings.Contains(a.Name, assetSuffix) {
			info.DownloadURL = a.BrowserDownloadURL
			break
		}
	}

	return info, nil
}

// NeedsUpdate 比较版本号（简单字符串比较，依赖 semver 格式 vX.Y.Z）
func NeedsUpdate(current, latest string) bool {
	if current == "dev" || current == "" {
		return false // 开发版本不触发自动更新
	}
	// 去掉 v 前缀后直接比较
	c := strings.TrimPrefix(current, "v")
	l := strings.TrimPrefix(latest, "v")
	return l != "" && l != c
}

// DownloadAndApply 下载新 exe 并执行替换。
// Windows：批处理脚本法；其他平台：直接替换。
// 替换完成后进程退出，由脚本或调用方负责重启。
func DownloadAndApply(downloadURL string, logs func(string)) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	exePath, _ = filepath.Abs(exePath)
	exeDir := filepath.Dir(exePath)

	logs("下载新版本中...")
	newExePath := exePath + ".new"
	if err := downloadFile(downloadURL, newExePath); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	// 设置可执行权限
	_ = os.Chmod(newExePath, 0755)
	logs("下载完成，准备替换...")

	if runtime.GOOS == "windows" {
		return applyWindows(exePath, newExePath, exeDir, logs)
	}
	return applyUnix(exePath, newExePath, logs)
}

// ─── 内部辅助 ──────────────────────────────────────────────────────────────────

func applyWindows(exePath, newExePath, exeDir string, logs func(string)) error {
	// Windows：写 batch 脚本完成重命名，再 detached 启动
	batPath := filepath.Join(exeDir, "_update.bat")
	bat := fmt.Sprintf(`@echo off
timeout /t 2 /nobreak >nul
move /y "%s" "%s"
if exist "%s.old" del /f /q "%s.old"
start "" "%s"
del /f /q "%%~f0"
`,
		newExePath, exePath,
		exePath, exePath,
		exePath,
	)
	if err := os.WriteFile(batPath, []byte(bat), 0644); err != nil {
		return fmt.Errorf("write update script: %w", err)
	}

	// 将当前 exe 先重命名为 .old（腾位置让脚本 move 新 exe 进来）
	oldPath := exePath + ".old"
	if err := os.Rename(exePath, oldPath); err != nil {
		return fmt.Errorf("rename current exe: %w", err)
	}

	logs("启动更新脚本，即将重启...")
	cmd := exec.Command("cmd", "/c", "start", "/b", batPath)
	cmd.Dir = exeDir
	if err := cmd.Start(); err != nil {
		// 回滚
		_ = os.Rename(oldPath, exePath)
		return fmt.Errorf("start update script: %w", err)
	}

	os.Exit(0)
	return nil // unreachable
}

func applyUnix(exePath, newExePath string, logs func(string)) error {
	// Unix：直接原子替换（rename syscall 在同 FS 上是原子的）
	if err := os.Rename(newExePath, exePath); err != nil {
		return fmt.Errorf("replace exe: %w", err)
	}
	logs("替换完成，请重启程序。")
	return nil
}

func downloadFile(url, dest string) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

// platformAssetSuffix 返回当前平台对应的 Release asset 名称片段
func platformAssetSuffix() string {
	switch runtime.GOOS {
	case "windows":
		return "windows-amd64"
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "darwin-arm64"
		}
		return "darwin-amd64"
	default:
		return "linux-amd64"
	}
}
