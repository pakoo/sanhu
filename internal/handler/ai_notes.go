package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/aiprompt"
	"github.com/pakoo/sanhu/internal/timing"
)

// AI 结论台账（产品稿 Q8=C）。
//
// 工具只负责存和取，**不解析 AI 说了什么、也不据此做任何自动动作** ——
// Q8 明确砍掉了「AI 结论一键落成决策」的联动，因为那要处理决策取消/改期时
// 结论怎么跟随，边界情况一堆，而闭环的价值 90% 在「能回溯当时怎么判断的」。

type aiNote struct {
	ID         int64  `db:"id"          json:"id"`
	Scene      string `db:"scene"       json:"scene"`
	SceneTitle string `db:"scene_title" json:"scene_title"`
	Codes      string `db:"codes"       json:"codes"`
	Summary    string `db:"summary"     json:"summary"`
	Content    string `db:"content"     json:"content"`
	CreatedAt  string `db:"created_at"  json:"created_at"`
}

// CreateAINote 回填一条 AI 结论
func (h *Handler) CreateAINote(c *gin.Context) {
	var req struct {
		Scene   string   `json:"scene"`
		Codes   []string `json:"codes"`
		Summary string   `json:"summary"`
		Content string   `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		fail(c, http.StatusBadRequest, "没有内容可存档")
		return
	}

	title := req.Scene
	if s, found := aiprompt.SceneByID(req.Scene); found {
		title = s.Title
	}

	res, err := h.DB.Exec(`
		INSERT INTO ai_notes (scene, scene_title, codes, summary, content, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		req.Scene, title, strings.Join(cleanCodes(req.Codes), ","),
		strings.TrimSpace(req.Summary), req.Content,
		time.Now().Format("2006-01-02 15:04:05"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	ok(c, gin.H{"id": id})
}

// ListAINotes 台账列表。带 code 参数时只返回与该基金相关的（基金详情弹窗用）。
func (h *Handler) ListAINotes(c *gin.Context) {
	code := strings.TrimSpace(c.Query("code"))
	limit := parseIntDefault(c.Query("limit"), 50)

	var notes []aiNote
	var err error
	if code != "" {
		err = h.DB.Select(&notes, `
			SELECT id, scene, scene_title, codes, summary, content, created_at
			FROM ai_notes
			WHERE ','||codes||',' LIKE '%,'||?||',%'
			ORDER BY created_at DESC LIMIT ?`, code, limit)
	} else {
		err = h.DB.Select(&notes, `
			SELECT id, scene, scene_title, codes, summary, content, created_at
			FROM ai_notes ORDER BY created_at DESC LIMIT ?`, limit)
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if notes == nil {
		notes = []aiNote{}
	}

	// 「当时 vs 现在」的对照：这是台账真正值钱的地方 ——
	// 让旧结论变成可验证的东西，而不是聊完就忘。只在按基金查时算，列表页不算（会很慢）。
	out := make([]gin.H, 0, len(notes))
	for _, n := range notes {
		row := gin.H{
			"id": n.ID, "scene": n.Scene, "scene_title": n.SceneTitle,
			"codes": splitCodes(n.Codes), "summary": n.Summary,
			"content": n.Content, "created_at": n.CreatedAt,
		}
		if code != "" {
			row["now"] = h.thenVsNow(code)
		}
		out = append(out, row)
	}
	ok(c, gin.H{"notes": out})
}

// DeleteAINote 删掉一条存档
func (h *Handler) DeleteAINote(c *gin.Context) {
	id, err := parseInt64Param(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := h.DB.Exec(`DELETE FROM ai_notes WHERE id=?`, id); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{})
}

// thenVsNow 这只基金现在的择时状态，供前端拼「当时说等回踩，现在偏离 +1.2%」
func (h *Handler) thenVsNow(code string) gin.H {
	t, err := timing.GetTimingSignal(h.DB, code)
	if err != nil || t == nil {
		return gin.H{}
	}
	out := gin.H{"timing_level": t.Level, "composite_score": t.CompositeScore}
	if t.PriceSignal.DeviationPct != nil {
		out["deviation_pct"] = *t.PriceSignal.DeviationPct
	}
	if t.MarketSignal.PEPct != nil {
		out["market_pe_pct"] = *t.MarketSignal.PEPct
	}
	return out
}

func cleanCodes(codes []string) []string {
	out := make([]string, 0, len(codes))
	seen := map[string]bool{}
	for _, c := range codes {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

func splitCodes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	return strings.Split(raw, ",")
}
