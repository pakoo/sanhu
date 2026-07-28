package handler

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/updater"
)

// ─── 更新任务状态 ──────────────────────────────────────────────────────────────

type updateState struct {
	mu         sync.Mutex
	Running    bool
	Done       bool
	Success    bool
	Logs       []string
	NewVersion string
}

var updateTask updateState

// ─── Handlers ──────────────────────────────────────────────────────────────────

func (h *Handler) GetSystemVersion(c *gin.Context) {
	ver := h.Version
	if ver == "" {
		ver = "dev"
	}
	tag := h.GitTag
	var tagVal interface{} = tag
	if tag == "" {
		tagVal = nil
	}
	c.JSON(http.StatusOK, gin.H{
		"version":       ver,
		"data_dir":      h.DataDir,
		"git_tag":       tagVal,
		"changelog_url": "https://github.com/pakoo/sanhu/releases",
	})
}

func (h *Handler) CheckUpdate(c *gin.Context) {
	current := h.Version
	if current == "" {
		current = "dev"
	}

	rel, err := updater.CheckLatestRelease()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"has_update": false,
			"current":    current,
			"error":      err.Error(),
		})
		return
	}

	hasUpdate := updater.NeedsUpdate(current, rel.TagName)
	resp := gin.H{
		"has_update":    hasUpdate,
		"current":       current,
		"latest":        rel.TagName,
		"changelog_url": rel.HTMLURL,
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) TriggerUpdate(c *gin.Context) {
	updateTask.mu.Lock()
	if updateTask.Running {
		updateTask.mu.Unlock()
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "更新已在进行中"})
		return
	}
	updateTask.Running = true
	updateTask.Done = false
	updateTask.Success = false
	updateTask.Logs = []string{}
	updateTask.NewVersion = ""
	updateTask.mu.Unlock()

	go func() {
		logf := func(msg string) {
			updateTask.mu.Lock()
			updateTask.Logs = append(updateTask.Logs, msg)
			updateTask.mu.Unlock()
		}

		rel, err := updater.CheckLatestRelease()
		if err != nil {
			logf("检查版本失败：" + err.Error())
			updateTask.mu.Lock()
			updateTask.Running = false
			updateTask.Done = true
			updateTask.mu.Unlock()
			return
		}

		if rel.DownloadURL == "" {
			logf("未找到当前平台的下载文件")
			updateTask.mu.Lock()
			updateTask.Running = false
			updateTask.Done = true
			updateTask.mu.Unlock()
			return
		}

		logf("最新版本：" + rel.TagName)
		updateTask.mu.Lock()
		updateTask.NewVersion = rel.TagName
		updateTask.mu.Unlock()

		if err := updater.DownloadAndApply(rel.DownloadURL, logf); err != nil {
			logf("更新失败：" + err.Error())
			updateTask.mu.Lock()
			updateTask.Running = false
			updateTask.Done = true
			updateTask.mu.Unlock()
			return
		}

		// 成功时 DownloadAndApply 会 os.Exit(0)，下面只在 Unix 非重启时到达
		updateTask.mu.Lock()
		updateTask.Running = false
		updateTask.Done = true
		updateTask.Success = true
		updateTask.mu.Unlock()
	}()

	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "更新已启动，请关注进度"})
}

func (h *Handler) GetUpdateStatus(c *gin.Context) {
	updateTask.mu.Lock()
	defer updateTask.mu.Unlock()
	var newVer interface{} = updateTask.NewVersion
	if updateTask.NewVersion == "" {
		newVer = nil
	}
	c.JSON(http.StatusOK, gin.H{
		"running":     updateTask.Running,
		"done":        updateTask.Done,
		"success":     updateTask.Success,
		"logs":        updateTask.Logs,
		"new_version": newVer,
	})
}

func (h *Handler) Restart(c *gin.Context) {
	// 无法远程重启 exe，返回 ok 由前端提示用户手动重启
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "请手动重启程序"})
}
