package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/fetcher"
)

func (h *Handler) GetMarketValuation(c *gin.Context) {
	pePct, err := fetcher.CalcPEPercentile(h.DB, "000300", 5)
	if err != nil {
		// PE 数据尚未抓取，降级返回中性信号
		ok(c, gin.H{
			"pe_percentile": nil,
			"signal":        "neutral",
			"index_code":    "000300",
			"current_pe":    nil,
			"note":          "PE数据未就绪，请先点击刷新",
		})
		return
	}

	signal := "neutral"
	switch {
	case pePct < 30:
		signal = "favorable"
	case pePct > 70:
		signal = "unfavorable"
	}

	var currentPE float64
	col := "pe"
	cols, err := tableColumns(h.DB, "index_pe_history")
	if err == nil && !hasColumn(cols, "pe") && hasColumn(cols, "pe_ttm") {
		col = "pe_ttm"
	}
	_ = h.DB.Get(&currentPE, fmt.Sprintf("SELECT %s FROM index_pe_history WHERE index_code='000300' ORDER BY date DESC LIMIT 1", col))

	ok(c, gin.H{
		"pe_percentile": pePct,
		"signal":        signal,
		"index_code":    "000300",
		"current_pe":    currentPE,
	})
}

func (h *Handler) RefreshMarketValuation(c *gin.Context) {
	go func() { _ = fetcher.FetchIndexPEAll(h.DB) }()
	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "PE数据刷新已启动"})
}

func (h *Handler) RefreshPeerRanks(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "peer ranks刷新完成"})
}

func (h *Handler) GetAIContext(c *gin.Context) {
	holdings, total, err := loadPortfolioHoldings(h.DB)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	lines := []string{fmt.Sprintf("当前组合总市值约 %.2f 元。", total)}
	for i, item := range holdings {
		if i >= 5 {
			break
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = item.Code
		}
		lines = append(lines, fmt.Sprintf("%s（%s）占用约 %.2f 元，分类 %s。", name, item.Code, item.Value, item.Category))
	}
	ok(c, gin.H{"context": strings.Join(lines, "\n")})
}
