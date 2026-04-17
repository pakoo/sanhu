// API 客户端
const API = {
    async get(url) {
        const resp = await fetch(url);
        return resp.json();
    },

    async post(url, data) {
        const resp = await fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(data),
        });
        return resp.json();
    },

    // 持仓
    getPortfolio() { return this.get('/api/portfolio'); },
    getHoldingDetail(code) { return this.get(`/api/portfolio/${code}`); },
    recordTransaction(data) { return this.post('/api/portfolio/transaction', data); },

    // 基金数据
    searchFund(q) { return this.get(`/api/funds/search?q=${encodeURIComponent(q)}`); },
    getNav(code, days = 365) { return this.get(`/api/funds/${code}/nav?days=${days}`); },
    getRealtime(code) { return this.get(`/api/funds/${code}/realtime`); },
    refreshData() { return this.post('/api/funds/refresh'); },
    getRefreshStatus() { return this.get('/api/funds/refresh/status'); },

    // 风险分析
    getRiskAnalysis() { return this.get('/api/risk/analysis'); },
    getCorrelation() { return this.get('/api/risk/correlation'); },

    // 调仓建议
    getRebalanceSuggestions() { return this.get('/api/rebalance/suggestions'); },
    getTargetAllocation() { return this.get('/api/rebalance/target'); },
    setTargetAllocation(data) { return this.post('/api/rebalance/target', data); },
    getTransitionPlan(months, monthly) {
        return this.get(`/api/rebalance/transition?months=${months}&monthly_invest=${monthly}`);
    },

    // 回测
    backtestDCA(data) { return this.post('/api/backtest/dca', data); },
    backtestTakeProfit(data) { return this.post('/api/backtest/takeprofit', data); },
    backtestPortfolio(data) { return this.post('/api/backtest/portfolio', data); },

    // 持仓透视
    getFundStocks(code) { return this.get(`/api/holdings/${code}/stocks`); },
    getFundIndustry(code) { return this.get(`/api/holdings/${code}/industry`); },
    getHoldingsOverlap() { return this.get('/api/holdings/overlap'); },
    getIndustryTotal() { return this.get('/api/holdings/industry-total'); },
    getHoldingsChanges(code) { return this.get(`/api/holdings/${code}/changes`); },
    refreshHoldings() { return this.post('/api/holdings/refresh'); },
    getHoldingsStatus() { return this.get('/api/holdings/refresh/status'); },

    // v2.0
    getMarketValuation() { return this.get('/api/market/valuation'); },
    refreshMarketValuation() { return this.post('/api/market/valuation/refresh'); },
    getFundScores() { return this.get('/api/funds/scores'); },
    refreshFundScores() { return this.post('/api/funds/scores/refresh'); },
    getAIContext() { return this.get('/api/ai/context'); },

    // 关注列表
    getWatchlist() { return this.get('/api/watchlist'); },
    addToWatchlist(code) { return this.post('/api/watchlist/add', { code }); },
    removeFromWatchlist(code) {
        return fetch(`/api/watchlist/${code}`, { method: 'DELETE' }).then(r => r.json());
    },

    // 设置
    getSettings() { return this.get('/api/settings').then(r => r.data || {}); },
    patchSettings(data) {
        return fetch('/api/settings', {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(data),
        }).then(r => r.json());
    },

    // 截图导入 / 手动更新持仓
    parseImport(formData) {
        return fetch('/api/import/parse', { method: 'POST', body: formData }).then(r => r.json());
    },
    commitImport(data) { return this.post('/api/import/commit', data); },

    // 单条持仓操作
    updateHolding(code, data) {
        return fetch(`/api/holdings/${code}`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(data),
        }).then(r => r.json());
    },
    deleteHolding(code) {
        return fetch(`/api/holdings/${code}`, { method: 'DELETE' }).then(r => r.json());
    },
};
