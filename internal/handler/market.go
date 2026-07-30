package handler

import (
	"database/sql"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/fetcher"
)

func (h *Handler) GetMarketValuation(c *gin.Context) {
	// 与 Python 基准 + 前端契约对齐：返回 hs300 / csi500 两个指数的 PE 百分位信号。
	ok(c, gin.H{
		"hs300":      pePercentile(h.DB, "hs300"),
		"csi500":     pePercentile(h.DB, "csi500"),
		"updated_at": time.Now().Format("2006-01-02 15:04"),
	})
}

// pePercentile 复刻 Python get_pe_percentile：近 10 年(2520 行)PE 百分位 + 信号。
func pePercentile(db *sqlx.DB, indexCode string) gin.H {
	type peRow struct {
		Date string          `db:"date"`
		PE   sql.NullFloat64 `db:"pe"`
		PB   sql.NullFloat64 `db:"pb"`
	}
	var rows []peRow
	_ = db.Select(&rows, `
		SELECT date, pe, pb FROM index_pe_history
		WHERE index_code=? AND pe IS NOT NULL
		ORDER BY date DESC LIMIT ?`, indexCode, 10*252)

	if len(rows) == 0 {
		return gin.H{"index_code": indexCode, "current_pe": nil, "current_pb": nil,
			"percentile": nil, "signal": "无数据", "date": "", "history_count": 0}
	}

	curPE := rows[0].PE.Float64
	peVals := make([]float64, 0, len(rows))
	for _, r := range rows {
		if r.PE.Valid {
			peVals = append(peVals, r.PE.Float64)
		}
	}
	hc := len(peVals)

	var pct float64
	if hc < 2 {
		pct = 50.0
	} else {
		below := 0
		for _, v := range peVals {
			if v <= curPE {
				below++
			}
		}
		pct = math.Round(float64(below)/float64(hc)*100*10) / 10 // 保留 1 位
	}

	signal := "合理"
	if pct < 30 {
		signal = "低估"
	} else if pct >= 70 {
		signal = "高估"
	}

	var pb interface{}
	if rows[0].PB.Valid {
		pb = math.Round(rows[0].PB.Float64*100) / 100
	}

	return gin.H{
		"index_code":    indexCode,
		"current_pe":    math.Round(curPE*100) / 100,
		"current_pb":    pb,
		"percentile":    pct,
		"signal":        signal,
		"date":          rows[0].Date,
		"history_count": hc,
	}
}

func (h *Handler) RefreshMarketValuation(c *gin.Context) {
	go func() { _ = fetcher.FetchIndexPEAll(h.DB) }()
	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "PE数据刷新已启动"})
}

func (h *Handler) RefreshPeerRanks(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "peer ranks刷新完成"})
}

// GetAIContext 与 currentRiskLevel 已移至 ai_prompts.go —— 那里是 AI 提示词的统一归口，
// 留在这里会和新的场景化实现重复声明。
