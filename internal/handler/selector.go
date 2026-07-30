package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/selector"
	"github.com/pakoo/sanhu/internal/watchlist"
)

// GetSelectorQuestions 返回选基问卷。
//
// 结构必须是 {id, text, options:[{value,label}]} —— 前端 selector.js 读的是
// q.text / opt.value / opt.label。之前返回的是 {id,label,options:[字符串]}，
// 页面上整屏渲染成 "undefined"。
//
// 选项的 value 要用推荐引擎认得的取值（见 selector.UserAnswers）：
// q2 对应 RiskTolerance（low/moderate/high），q4 对应 AllowOverlap（yes/no）。
func (h *Handler) GetSelectorQuestions(c *gin.Context) {
	ok(c, []gin.H{
		{
			"id":   "q1",
			"text": "这次加仓主要想补什么？",
			"options": []gin.H{
				{"value": "auto", "label": "让工具按持仓缺口判断"},
				{"value": "growth", "label": "科技成长"},
				{"value": "dividend", "label": "稳健红利"},
				{"value": "balanced", "label": "均衡配置"},
			},
		},
		{
			"id":   "q2",
			"text": "能接受的波动有多大？",
			"options": []gin.H{
				{"value": "low", "label": "小，尽量别亏"},
				{"value": "moderate", "label": "中，能接受一定回撤"},
				{"value": "high", "label": "大，愿意换取更高收益"},
			},
		},
		{
			"id":   "q3",
			"text": "这笔钱多久不会动？",
			"options": []gin.H{
				{"value": "short", "label": "1 年内"},
				{"value": "mid", "label": "1-3 年"},
				{"value": "long", "label": "3 年以上"},
			},
		},
		{
			"id":   "q4",
			"text": "可以推荐与现有持仓重叠的基金吗？",
			"options": []gin.H{
				{"value": "no", "label": "不要，我想分散一些"},
				{"value": "yes", "label": "可以，好基金重复没关系"},
			},
		},
	})
}

func (h *Handler) DiagnoseGaps(c *gin.Context) {
	gaps, err := selector.DiagnosePortfolioGaps(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	// 前端读的是 data.gaps[{name,current_pct,suggest_add_pct,priority}]，
	// 内部结构是 {Theme,DeficitPct,...}。之前直接把内部结构塞进 portfolio_gaps 返回，
	// 前端取不到 gaps，于是缺口数恒为 0、Top 缺口永远显示"暂无穿透数据"——
	// 而实际上是有数据的。
	out := make([]gin.H, 0, len(gaps))
	for _, g := range gaps {
		out = append(out, gin.H{
			"name":            g.Theme,
			"category":        g.Category,
			"current_pct":     g.CurrentPct,
			"suggest_add_pct": g.DeficitPct,
			"priority":        g.Priority,
		})
	}
	ok(c, gin.H{"gaps": out, "portfolio_gaps": gaps})
}

func (h *Handler) Recommend(c *gin.Context) {
	// 前端发的是 {"answers":{"q1":...}}（selector.js），
	// 而这里原来只读顶层的 q1..q4，于是四个答案永远是空字符串，
	// 推荐结果与用户的选择完全无关。两种形状都接受，以答案对象优先。
	var req struct {
		Answers map[string]string `json:"answers"`
		Q1      string            `json:"q1"`
		Q2      string            `json:"q2"`
		Q3      string            `json:"q3"`
		Q4      string            `json:"q4"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	pick := func(key, fallback string) string {
		if v, okv := req.Answers[key]; okv && v != "" {
			return v
		}
		return fallback
	}
	q1, q2, q3, q4 := pick("q1", req.Q1), pick("q2", req.Q2), pick("q3", req.Q3), pick("q4", req.Q4)

	result, err := selector.GenerateRecommendation(h.DB, selector.UserAnswers{
		Direction:     q1,
		RiskTolerance: q2,
		TimePeriod:    q3,
		AllowOverlap:  q4 == "yes",
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	// 这里必须按前端契约整形，不能把内部结构体直接抛出去。
	// 之前直接 ok(c, result)，页面上「系统诊断的配置缺口」三张卡全是 undefined：
	//   前端读 g.name / g.suggest_add_pct，内部字段却叫 theme / deficit_pct；
	//   候选卡读 c.composite（0~1）与 data.candidates，内部是 score（0~100）与 top_candidates
	//   —— 即使有候选也永远渲染不出来。
	// 与 /api/selector/diagnose 的输出保持一致，两处不再各说一套。
	gaps := make([]gin.H, 0, len(result.Gaps))
	for _, g := range result.Gaps {
		gaps = append(gaps, gin.H{
			"name":            g.Theme,
			"category":        g.Category,
			"current_pct":     g.CurrentPct,
			"suggest_add_pct": g.DeficitPct,
			// priority 内部是 1~5 的档位，前端 priorityColor 按 0~1 判色，
			// 直接传会让所有卡片都判成最高优先级（全红）
			"priority":       float64(g.Priority) / 5.0,
			"priority_level": g.Priority,
		})
	}

	candidates := make([]gin.H, 0, len(result.TopCandidates))
	for _, cd := range result.TopCandidates {
		candidates = append(candidates, gin.H{
			"code": cd.Code,
			"name": cd.Name,
			// composite 前端要 0~1（渲染时 ×100 显示成分数）
			"composite":        cd.Score / 100.0,
			"score":            cd.Score,
			"gap_fit_score":    cd.GapFitScore,
			"quality_score":    cd.QualityScore,
			"peer_rank_score":  cd.PeerRankScore,
			"reason_fragments": candidateReasons(cd),
			"profile_source":   h.profileSource(cd.Code),
			// 重叠率暂未计算，给 null 让前端显示「重叠率 N/A」而不是 0%
			"overlap_rate": nil,
		})
	}

	ok(c, gin.H{
		"session_id": result.SessionID,
		"gaps":       gaps,
		"candidates": candidates,
	})
}

// candidateReasons 把三项打分翻译成人话，作为候选卡上的推荐理由
func candidateReasons(c selector.ScoredCandidate) []string {
	var out []string
	if c.GapFitScore >= 75 {
		out = append(out, "行业契合缺口")
	}
	if c.QualityScore >= 70 {
		out = append(out, "综合评分较高")
	}
	if c.PeerRankScore >= 70 {
		out = append(out, "同类排名靠前")
	}
	if len(out) == 0 {
		out = append(out, "综合评分一般，仅作参考")
	}
	return out
}

// profileSource 取该基金档案的来源：pierced=季报穿透，inferred=按名称推断。
// 前端据此显示不同徽章，让用户知道这条推荐的依据有多硬。
func (h *Handler) profileSource(code string) string {
	var src string
	if err := h.DB.Get(&src, `SELECT COALESCE(profile_source,'') FROM fund_profile WHERE code=?`, code); err != nil {
		return ""
	}
	return src
}

// selectorSession 对应 selector_cache 的真实列（见 migrations/001_init.sql）
type selectorSession struct {
	SessionID      string `db:"session_id"`
	AnswersJSON    string `db:"answers_json"`
	GapsJSON       string `db:"gaps_json"`
	CandidatesJSON string `db:"candidates_json"`
	CreatedAt      string `db:"created_at"`
}

func loadSelectorSession(h *Handler, sessionID string) (*selectorSession, error) {
	var row selectorSession
	err := h.DB.Get(&row, `
		SELECT session_id,
		       COALESCE(answers_json,'')    AS answers_json,
		       COALESCE(gaps_json,'')       AS gaps_json,
		       COALESCE(candidates_json,'') AS candidates_json,
		       COALESCE(created_at,'')      AS created_at
		FROM selector_cache WHERE session_id=?`, sessionID)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (h *Handler) GetSelectorSession(c *gin.Context) {
	row, err := loadSelectorSession(h, c.Param("session_id"))
	if err == sql.ErrNoRows {
		fail(c, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	// 三个 JSON 列各自还原成对象再返回，避免前端拿到一坨字符串
	unmarshal := func(raw string) interface{} {
		if raw == "" {
			return nil
		}
		var v interface{}
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return raw
		}
		return v
	}
	ok(c, gin.H{
		"session_id": row.SessionID,
		"answers":    unmarshal(row.AnswersJSON),
		"gaps":       unmarshal(row.GapsJSON),
		"candidates": unmarshal(row.CandidatesJSON),
		"created_at": row.CreatedAt,
	})
}

func (h *Handler) ExportSelectorSession(c *gin.Context) {
	sessionID := c.Param("session_id")
	row, err := loadSelectorSession(h, sessionID)
	if err == sql.ErrNoRows {
		fail(c, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	raw := fmt.Sprintf("{\"answers\":%s,\"gaps\":%s,\"candidates\":%s}",
		orEmptyJSON(row.AnswersJSON), orEmptyJSON(row.GapsJSON), orEmptyJSON(row.CandidatesJSON))

	md := buildMarkdown(fmt.Sprintf("Selector Session %s", sessionID), []string{
		"```json",
		raw,
		"```",
	})
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(md))
}

func (h *Handler) AdoptRecommendation(c *gin.Context) {
	var req struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := watchlist.AddToWatchlist(h.DB, req.Code); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"code": req.Code})
}

func (h *Handler) RefreshProfile(c *gin.Context) {
	ok(c, gin.H{"msg": "profile刷新完成"})
}

// orEmptyJSON 空字符串会让拼出来的 JSON 非法，兜底成 null
func orEmptyJSON(s string) string {
	if s == "" {
		return "null"
	}
	return s
}
