package handler

import "github.com/gin-gonic/gin"

func RegisterRoutes(r *gin.Engine, h *Handler) {
	api := r.Group("/api")

	api.GET("/portfolio", h.GetPortfolio)
	api.GET("/portfolio/:code", h.GetPortfolioDetail)
	api.POST("/portfolio/transaction", h.CreateTransaction)

	api.GET("/funds/search", h.SearchFunds)
	api.POST("/funds/refresh", h.RefreshFunds)
	api.GET("/funds/refresh/status", h.GetRefreshStatus)
	api.GET("/funds/scores", h.GetFundScores)
	api.POST("/funds/scores/refresh", h.RefreshFundScores)
	api.GET("/funds/:code/lookup", h.LookupFund)
	api.GET("/funds/:code/nav", h.GetFundNav)
	api.GET("/funds/:code/realtime", h.GetFundRealtime)
	api.GET("/funds/:code/peers", h.GetFundPeers)
	api.GET("/funds/:code/ai-prompts/theme-analysis", h.GetFundAIThemePrompt)

	api.POST("/holdings/refresh", h.RefreshHoldings)
	api.GET("/holdings/refresh/status", h.GetHoldingsRefreshStatus)
	api.GET("/holdings/overlap", h.GetHoldingsOverlap)
	// 持仓穿透的 AI 提示词（新增的第四个入口）
	api.GET("/holdings/ai-prompt", h.GetLookThroughPrompt)
	api.GET("/holdings/industry-total", h.GetHoldingsIndustryTotal)
	api.GET("/holdings/:code/stocks", h.GetFundStocks)
	api.GET("/holdings/:code/industry", h.GetFundIndustry)
	api.GET("/holdings/:code/changes", h.GetHoldingsChanges)
	api.PUT("/holdings/:code", h.UpdateHolding)
	api.DELETE("/holdings/:code", h.DeleteHolding)

	api.GET("/risk/analysis", h.GetRiskAnalysis)
	api.GET("/risk/correlation", h.GetCorrelation)

	api.GET("/rebalance/suggestions", h.GetRebalanceSuggestions)
	api.GET("/rebalance/target", h.GetTargetAllocation)
	api.POST("/rebalance/target", h.SetTargetAllocation)
	api.GET("/rebalance/transition", h.GetTransitionPlan)

	api.POST("/backtest/dca", h.BacktestDCA)
	api.POST("/backtest/takeprofit", h.BacktestTakeProfit)
	api.POST("/backtest/portfolio", h.BacktestPortfolio)

	// 市场板块（全球指标 + AI 关注区）
	api.GET("/market/global", h.GetGlobalMarket)
	api.GET("/market/ai-watchlist", h.GetAIWatchlist)
	api.POST("/market/ai-watchlist", h.SaveAIWatchlist)
	api.GET("/market/quote-lookup", h.LookupQuote)

	// 板块资金流（产品稿 docs/product-specs/sector-fund-flow.html）
	// 限流时不报错，返回降级数据 + stale 标记，前端据此显示「数据截至 X」
	api.GET("/fundflow/rank", h.GetSectorFlowRank)
	api.GET("/fundflow/minute", h.GetSectorFlowMinute)
	api.GET("/fundflow/daily", h.GetSectorFlowDaily)
	api.GET("/fundflow/replay", h.GetSectorFlowReplay)

	api.GET("/market/valuation", h.GetMarketValuation)
	api.POST("/market/valuation/refresh", h.RefreshMarketValuation)
	api.POST("/peer-ranks/refresh", h.RefreshPeerRanks)

	// AI 分析：统一入口 + 场景清单 + 结论台账。
	// /ai/context 是旧的「AI持仓解读」端点，内部转发到 portfolio_checkup 场景，
	// 保留是为了不打断已发布的前端和用户书签。
	api.GET("/ai/scenes", h.GetAIScenes)
	api.GET("/ai/prompts/:scene", h.GetAIPrompt)
	api.GET("/ai/context", h.GetAIContext)
	api.GET("/ai/notes", h.ListAINotes)
	api.POST("/ai/notes", h.CreateAINote)
	api.DELETE("/ai/notes/:id", h.DeleteAINote)

	api.POST("/import/parse", h.ImportParse)
	api.POST("/import/commit", h.ImportCommit)
	// 录入后补档案/净值的后台进度；失败明细在这里，前端据此生成可复制的报错信息
	api.GET("/import/status", h.GetImportStatus)

	api.GET("/watchlist", h.GetWatchlist)
	api.POST("/watchlist/add", h.AddWatchlist)
	api.DELETE("/watchlist/:code", h.RemoveWatchlist)

	api.GET("/selector/questions", h.GetSelectorQuestions)
	api.POST("/selector/diagnose", h.DiagnoseGaps)
	api.POST("/selector/recommend", h.Recommend)
	api.GET("/selector/session/:session_id", h.GetSelectorSession)
	api.GET("/selector/export/:session_id", h.ExportSelectorSession)
	api.POST("/selector/adopt", h.AdoptRecommendation)
	api.POST("/selector/refresh-profile", h.RefreshProfile)

	api.GET("/timing/:code", h.GetTiming)
	api.GET("/position-sizing/:code", h.GetPositionSizing)

	api.GET("/settings", h.GetSettings)
	api.PATCH("/settings", h.PatchSettings)

	api.POST("/decisions/check-triggers", h.CheckTriggers)
	api.POST("/decisions", h.CreateDecision)
	api.GET("/decisions", h.ListDecisions)
	api.GET("/decisions/:id", h.GetDecision)
	api.PATCH("/decisions/:id/link-tx", h.LinkTransaction)
	api.GET("/decisions/:id/export", h.ExportDecision)
	api.DELETE("/decisions/:id", h.CancelDecision)

	api.GET("/simulations/strategies", h.GetSimulationStrategies)
	api.POST("/simulations", h.CreateSimulation)
	api.GET("/simulations", h.ListSimulations)
	api.GET("/simulations/:id", h.GetSimulationDetail)
	api.POST("/simulations/:id/run", h.RunSimulation)
	api.DELETE("/simulations/:id", h.DeleteSimulation)
	api.POST("/simulations/:id/advance", h.AdvanceSimulation)

	api.GET("/system/version", h.GetSystemVersion)
	api.GET("/system/check-update", h.CheckUpdate)
	api.POST("/system/update", h.TriggerUpdate)
	api.GET("/system/update/status", h.GetUpdateStatus)
	api.POST("/system/restart", h.Restart)
}
