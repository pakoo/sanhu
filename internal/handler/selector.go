package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/aiprompt"
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
	// 先确认这次问卷存在，否则统一场景那边只会返回一句 ready=false，
	// 而这个端点的调用方（选基页的「导出提示词」按钮）期望的是 404
	if _, err := loadSelectorSession(h, sessionID); err == sql.ErrNoRows {
		fail(c, http.StatusNotFound, "not found")
		return
	} else if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	// 输出完整提示词，不再是「# Selector Session xxx + 一坨 JSON」。
	// 原来那份没有任何指令，用户粘进 Claude 只会被反问「你想让我分析什么」。
	// 装配逻辑统一在 ai_prompts.go 的 selector_gap 场景里，这里只做转发。
	b := aiprompt.NewBuilder(aiprompt.Options{Redact: c.Query("redact") == "1"})
	scene, _ := aiprompt.SceneByID("selector_gap")
	res, err := h.sceneSelector(b, scene, time.Now().Format("2006-01-02 15:04"), sessionID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if !res.Ready {
		fail(c, http.StatusBadRequest, res.ReadyNote)
		return
	}
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(res.Prompt))
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

// answerLabels 把问卷答案的内部取值翻译成题干+人话，
// 直接把 direction=auto 这种发给 AI 它看不懂
func answerLabels(a selector.UserAnswers) map[string]string {
	dir := map[string]string{
		"auto": "让工具按持仓缺口判断", "growth": "科技成长",
		"dividend": "稳健红利", "balanced": "均衡配置",
	}
	risk := map[string]string{"low": "小，尽量别亏", "moderate": "中，能接受一定回撤", "high": "大，愿意换取更高收益"}
	period := map[string]string{"short": "1 年内", "mid": "1-3 年", "long": "3 年以上"}

	pick := func(m map[string]string, k, dflt string) string {
		if v, ok := m[k]; ok {
			return v
		}
		if k == "" {
			return dflt
		}
		return k
	}

	overlap := "不希望与现有持仓重叠"
	if a.AllowOverlap {
		overlap = "可以与现有持仓重叠"
	}
	return map[string]string{
		"1 想补的方向":  pick(dir, a.Direction, "未选择"),
		"2 能接受的波动": pick(risk, a.RiskTolerance, "未选择"),
		"3 持有期":    pick(period, a.TimePeriod, "未选择"),
		"4 重叠容忍度":  overlap,
	}
}

// sourceLabel 档案来源的人话说明，让 AI 知道这条推荐的依据有多硬
func sourceLabel(src string) string {
	switch src {
	case "pierced":
		return "季报穿透"
	case "inferred":
		return "按名称推断"
	default:
		return "未知"
	}
}
