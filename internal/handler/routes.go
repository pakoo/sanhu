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
	api.GET("/funds/:code/nav", h.GetFundNav)
	api.GET("/funds/:code/realtime", h.GetFundRealtime)
	api.GET("/funds/:code/peers", h.GetFundPeers)
	api.GET("/funds/:code/ai-prompts/theme-analysis", h.GetFundAIThemePrompt)

	api.POST("/holdings/refresh", h.RefreshHoldings)
	api.GET("/holdings/refresh/status", h.GetHoldingsRefreshStatus)
	api.GET("/holdings/overlap", h.GetHoldingsOverlap)
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

	api.GET("/market/valuation", h.GetMarketValuation)
	api.POST("/market/valuation/refresh", h.RefreshMarketValuation)
	api.POST("/peer-ranks/refresh", h.RefreshPeerRanks)

	api.GET("/ai/context", h.GetAIContext)

	api.POST("/import/parse", h.ImportParse)
	api.POST("/import/commit", h.ImportCommit)

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
