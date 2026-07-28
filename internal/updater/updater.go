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
	"strconv"
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

// NeedsUpdate 判断是否有更新：latest 必须严格新于 current。
//
// 原来是 `latest != current` 字符串不等——任何差异都算"有更新"，
// 一次回滚发布就会把所有用户推去装旧版本。改成按 semver 数字比较。
func NeedsUpdate(current, latest string) bool {
	if current == "dev" || current == "" || latest == "" {
		return false // 开发版本不触发自动更新
	}
	return compareSemver(latest, current) > 0
}

// compareSemver 比较 vX.Y.Z：a>b 返回 1，a<b 返回 -1，相等返回 0。
// 段数不等时短的补 0（v1.2 == v1.2.0）；非数字段落回退为字符串比较。
func compareSemver(a, b string) int {
	as := strings.Split(strings.TrimPrefix(strings.TrimSpace(a), "v"), ".")
	bs := strings.Split(strings.TrimPrefix(strings.TrimSpace(b), "v"), ".")

	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		av, aErr := segmentValue(as, i)
		bv, bErr := segmentValue(bs, i)
		if aErr != nil || bErr != nil {
			// 含预发布标签等非纯数字段，退回字符串比较，避免误判成"有更新"
			return strings.Compare(strings.TrimPrefix(a, "v"), strings.TrimPrefix(b, "v"))
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return 0
}

func segmentValue(segments []string, i int) (int, error) {
	if i >= len(segments) {
		return 0, nil
	}
	return strconv.Atoi(segments[i])
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
	// 先下到临时文件（保留原始扩展名，解压时按后缀判断格式）
	archivePath := exePath + ".download" + assetExt(downloadURL)
	if err := downloadFile(downloadURL, archivePath); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer os.Remove(archivePath)

	logs("解压中...")
	newExePath := exePath + ".new"
	if err := extractBinary(archivePath, newExePath); err != nil {
		os.Remove(newExePath)
		return fmt.Errorf("extract: %w", err)
	}
	if err := verifyExecutable(newExePath); err != nil {
		os.Remove(newExePath)
		return fmt.Errorf("下载的文件不是有效的可执行程序，已放弃更新: %w", err)
	}
	_ = os.Chmod(newExePath, 0755)
	logs("下载完成，准备替换...")

	if runtime.GOOS == "windows" {
		return applyWindows(exePath, newExePath, exeDir, logs)
	}
	return applyUnix(exePath, newExePath, logs)
}

// assetExt 从下载 URL 里取出扩展名（.tar.gz 作为整体保留）。
func assetExt(url string) string {
	name := url
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, "?"); i >= 0 {
		name = name[:i]
	}
	lower := strings.ToLower(name)
	for _, ext := range []string{".tar.gz", ".tgz", ".zip", ".gz"} {
		if strings.HasSuffix(lower, ext) {
			return ext
		}
	}
	return ""
}

// verifyExecutable 在覆盖当前程序之前做最后一道体检：
// 文件非空、且带可执行文件的魔数。宁可放弃更新，也不能把用户装好的程序换成一团垃圾。
func verifyExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() < 1<<20 { // 正常构建产物 >10MB，1MB 以下必然不对
		return fmt.Errorf("文件过小（%d 字节）", info.Size())
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		return fmt.Errorf("读取文件头: %w", err)
	}
	switch {
	case magic[0] == 0x7f && string(magic[1:4]) == "ELF": // Linux
		return nil
	case magic[0] == 'M' && magic[1] == 'Z': // Windows PE
		return nil
	// macOS Mach-O：32/64 位、大小端、universal binary
	case string(magic) == "\xcf\xfa\xed\xfe" || string(magic) == "\xce\xfa\xed\xfe",
		string(magic) == "\xfe\xed\xfa\xcf" || string(magic) == "\xfe\xed\xfa\xce",
		string(magic) == "\xca\xfe\xba\xbe":
		return nil
	}
	return fmt.Errorf("无法识别的文件格式（%x）", magic)
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
	// 先把旧版本挪到 .old 留作退路，替换失败时还能换回来
	oldPath := exePath + ".old"
	_ = os.Remove(oldPath)
	if err := os.Rename(exePath, oldPath); err != nil {
		return fmt.Errorf("backup current exe: %w", err)
	}

	// Unix：同 FS 上 rename 是原子的
	if err := os.Rename(newExePath, exePath); err != nil {
		if rbErr := os.Rename(oldPath, exePath); rbErr != nil {
			// 走到这里旧程序还在 .old，提示用户手工恢复，别让人以为文件没了
			return fmt.Errorf("替换失败且回滚失败，请手动把 %s 改名回 %s（原因: %v / 回滚: %v）",
				oldPath, exePath, err, rbErr)
		}
		return fmt.Errorf("replace exe（已回滚到原版本）: %w", err)
	}

	_ = os.Remove(oldPath)
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

// platformAssetSuffix 返回当前平台对应的 Release asset 名称片段。
// 直接用 GOOS-GOARCH，与 CI matrix 的命名一致——原来 linux/windows 分支写死 amd64，
// arm64 机器会去下 amd64 的包。
func platformAssetSuffix() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}
