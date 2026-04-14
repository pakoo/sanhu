// 基金投资决策助手 - 主逻辑

// === Toast 提示 ===
function showToast(msg, duration = 3000, type = 'info') {
    let el = document.getElementById('_globalToast');
    if (!el) {
        el = document.createElement('div');
        el.id = '_globalToast';
        el.style.cssText = 'position:fixed;bottom:32px;left:50%;transform:translateX(-50%);' +
            'color:#fff;padding:10px 20px;border-radius:8px;font-size:13px;' +
            'z-index:9999;box-shadow:0 4px 12px rgba(0,0,0,0.3);transition:opacity 0.3s;pointer-events:none;';
        document.body.appendChild(el);
    }
    el.style.background = type === 'error' ? '#dc2626' : '#1e293b';
    el.textContent = msg;
    el.style.opacity = '1';
    clearTimeout(el._timer);
    el._timer = setTimeout(() => { el.style.opacity = '0'; }, duration);
}

// 申万二级行业简介字典（用于图表 tooltip 和持仓列表 chip hover 提示）
const INDUSTRY_DESC = {
  // 电子
  '元件':       '电容、电阻、连接器、传感器等无源/有源元器件，代表：顺络电子、三环集团',
  '半导体':     '芯片设计、晶圆制造、封测，含CPU/GPU/存储/功率器件，代表：中芯国际、韦尔股份',
  '消费电子':   '手机、平板、PC及耳机等周边配件，代表：立讯精密、歌尔股份',
  '光学光电子': '光学镜头、面板、LED照明，代表：舜宇光学、三安光电',
  '电子化学品': '半导体用光刻胶、湿电子化学品，代表：雅克科技、飞凯材料',
  // 通信
  '通信设备':   '基站、路由器、光模块、交换机等通信硬件，代表：中兴通讯、烽火通信',
  '通信服务':   '电信运营商及增值服务，代表：中国移动、中国联通',
  // 电力设备/新能源
  '电池':       '锂电池、储能电池、消费电池，代表：宁德时代、亿纬锂能',
  '光伏设备':   '太阳能电池片、组件、逆变器及产线设备，代表：迈为股份、捷佳伟创',
  '风电设备':   '风机整机、叶片、塔筒，代表：金风科技、明阳智能',
  '电网设备':   '变压器、断路器、电缆等输配电设备，代表：许继电气、平高电气',
  // 汽车
  '汽车零部件': '发动机、底盘、汽车电子等整车配套零件，代表：均胜电子、福耀玻璃',
  '乘用车':     '轿车、SUV、新能源整车，代表：比亚迪、上汽集团',
  '商用车':     '货车、客车、专用作业车，代表：中国重汽、宇通客车',
  // 家电
  '白色家电':   '冰箱、洗衣机、空调等大型家用电器，代表：美的集团、海尔智家',
  '小家电':     '电饭煲、吸尘器、空气净化器等，代表：九阳股份、苏泊尔',
  '厨卫电器':   '燃气灶、油烟机、热水器，代表：老板电器、华帝股份',
  // 计算机/传媒
  '软件开发':   '企业管理/工业/金融IT软件，代表：用友网络、恒生电子',
  'IT服务':     '系统集成、云计算、信息安全，代表：浪潮信息、科大讯飞',
  '广告营销':   '互联网广告、内容营销、品牌策划，代表：分众传媒、蓝色光标',
  '游戏':       '手游、端游研发发行，代表：腾讯、网易',
  '影视院线':   '电影制作及院线运营，代表：万达电影、中国电影',
  '出版':       '图书出版、数字内容，代表：中文传媒、凤凰传媒',
  // 医药
  '医疗器械':   'CT/MRI、耗材、IVD体外诊断，代表：迈瑞医疗、联影医疗',
  '化学制药':   '化学小分子药物及原料药，代表：恒瑞医药、石药集团',
  '生物制品':   '疫苗、血制品、单抗生物药，代表：智飞生物、华兰生物',
  '中药':       '中成药、中药饮片，代表：云南白药、片仔癀',
  '医疗服务':   '连锁医院、眼科牙科等医疗机构，代表：爱尔眼科',
  // 金融
  '银行':       '国有大行、股份行、城商行，代表：招商银行、工商银行',
  '证券II':     '证券经纪、投行、资管，代表：中信证券、东方财富',
  '保险':       '人寿、财险、再保险，代表：中国平安、中国人寿',
  // 材料/化工
  '化工':       '石化、精细化工、新材料，代表：万华化学、华鲁恒升',
  '钢铁':       '普钢、特钢、不锈钢，代表：宝钢股份、华菱钢铁',
  '有色金属':   '铜、铝、锂、稀土等金属采选冶炼，代表：紫金矿业、赣锋锂业',
  '建筑材料':   '水泥、玻璃、玻纤，代表：海螺水泥、中国巨石',
  // 消费
  '食品':       '乳制品、调味品、休闲零食，代表：海天味业、伊利股份',
  '饮料乳品':   '饮料、乳品，代表：伊利股份、农夫山泉',
  '白酒':       '高端及大众白酒，代表：贵州茅台、泸州老窖',
  '农牧渔':     '种植、畜禽养殖、水产品，代表：牧原股份、大北农',
  '零售':       '超市、便利店、专业零售，代表：永辉超市、名创优品',
  '互联网电商': '电商平台、O2O，代表：阿里巴巴、京东',
  // 工业
  '工程机械':   '挖掘机、起重机、叉车，代表：三一重工、中联重科',
  '通用设备':   '泵、阀门、液压件等通用机械，代表：恒立液压',
  '军工':       '航空、舰船、导弹、雷达，代表：中航西飞、中船工业',
  '环保':       '污水处理、垃圾发电、大气治理，代表：光大环境、格林美',
  '电力':       '发电（火/水/核电）及电网运营，代表：长江电力、华能国际',
  '建筑工程':   '建筑施工总承包，代表：中国建筑、中国交建',
  '交通运输':   '公路、铁路、港口、航运，代表：中国中铁、中远海控',
  '房地产':     '住宅开发、商业地产，代表：万科A、保利发展',
};

let portfolioData = null;
let riskData = null;
let rebalanceData = null;
let holdingsData = null;
let scoresData = null;      // v2.0: fund scores map {code: score}
let valuationData = null;   // v2.0: market PE signals
let watchlistData = null;   // 关注列表
let activeFilters = new Set(['bond', 'equity', 'mixed', 'qdii']);

// === Tab 切换 ===
function switchTab(tab) {
    document.querySelectorAll('.tab-content').forEach(el => el.classList.remove('active'));
    document.querySelectorAll('.tab-btn').forEach(el => el.classList.remove('active'));
    document.getElementById(`tab-${tab}`).classList.add('active');
    event.target.classList.add('active');

    // 懒加载
    if (tab === 'risk' && !riskData) loadRisk();
    if (tab === 'rebalance' && !rebalanceData) loadRebalance();
    if (tab === 'holdings' && !holdingsData) loadHoldings();
    if (tab === 'rebalance' && !valuationData) loadValuation();
    if (tab === 'simulations' && typeof simInit === 'function') simInit();
    // 模块使用说明区
    if (typeof initModuleGuide === 'function') initModuleGuide(tab);
}

// === 总览内子 Tab（持仓明细 / 关注列表）===
function switchDashboardTab(name) {
    const isWatchlist = name === 'watchlist';
    document.getElementById('holdings-panel').style.display = isWatchlist ? 'none' : '';
    document.getElementById('watchlist-panel').style.display = isWatchlist ? '' : 'none';
    document.getElementById('holdings-actions').style.display = isWatchlist ? 'none' : '';
    document.getElementById('watchlist-actions').style.display = isWatchlist ? '' : 'none';

    const activeStyle = 'color:var(--primary);border-bottom:2px solid var(--primary);margin-bottom:-2px;';
    const inactiveStyle = 'color:var(--text-secondary);border-bottom:2px solid transparent;margin-bottom:-2px;';
    document.getElementById('subtab-btn-holdings').style.cssText += isWatchlist ? inactiveStyle : activeStyle;
    document.getElementById('subtab-btn-watchlist').style.cssText += isWatchlist ? activeStyle : inactiveStyle;

    if (isWatchlist && !watchlistData) loadWatchlist();
}

// === 数据刷新 ===
function _renderRefreshTimestamp(isoStr) {
    const el = document.getElementById('refreshTimestamp');
    if (!el) return;
    if (!isoStr) { el.innerHTML = ''; return; }
    const dt = new Date(isoStr);
    const diffHrs = (Date.now() - dt) / 3600000;
    const timeStr = dt.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
    const dotColor = diffHrs < 1 ? '#86efac' : diffHrs < 8 ? '#fcd34d' : 'rgba(255,255,255,0.35)';
    el.innerHTML = `<span style="color:${dotColor}">●</span> ${timeStr} 已更新`;
}

// 启动底部状态栏轮询（供首次点击和页面刷新后恢复共用）
function _startRefreshPoll() {
    const btn      = document.getElementById('refreshBtn');
    const bar      = document.getElementById('refreshStatusBar');
    const rsbBar   = document.getElementById('rsbBar');
    const rsbLabel = document.getElementById('rsbLabel');
    const rsbCount = document.getElementById('rsbCount');
    const rsbChips = document.getElementById('rsbChips');

    btn.disabled = true;
    btn.textContent = '更新中…';
    rsbLabel.textContent = '更新净值中';
    rsbLabel.style.color = '#374151';
    rsbCount.textContent = '';
    rsbBar.style.width = '0%';
    rsbChips.innerHTML = '';
    bar.style.display = 'flex';

    const poll = setInterval(async () => {
        let s;
        try { s = await API.getRefreshStatus(); } catch (e) { return; }

        const pct = s.total > 0 ? Math.round((s.done_count / s.total) * 90) + 5 : 5;
        rsbBar.style.width = pct + '%';
        rsbCount.textContent = s.total > 0 ? `${s.done_count} / ${s.total}` : '';

        if (s.recent && s.recent.length > 0) {
            rsbChips.innerHTML = s.recent.slice(-12).map(r => {
                const bg = r.ok ? '#dcfce7' : '#fee2e2';
                const fg = r.ok ? '#16a34a' : '#dc2626';
                return `<span style="background:${bg};color:${fg};font-size:11px;padding:2px 6px;border-radius:4px;">${r.code} ${r.ok ? '✓' : '✗'}</span>`;
            }).join('');
        }

        if (!s.running) {
            clearInterval(poll);
            rsbBar.style.width = '100%';
            const hasErr = s.errors && s.errors.length > 0;
            if (hasErr) {
                rsbLabel.textContent = `⚠ ${s.errors.length} 只更新失败`;
                rsbLabel.style.color = '#dc2626';
            } else {
                rsbLabel.textContent = `✓ 已更新 ${s.done_count} 只净值`;
                rsbLabel.style.color = '#16a34a';
                setTimeout(() => { bar.style.display = 'none'; }, 4000);
            }
            rsbCount.textContent = '';
            _renderRefreshTimestamp(s.last_refresh_at);
            btn.disabled = false;
            btn.textContent = '更新净值';
            riskData = null;
            rebalanceData = null;
            await loadDashboard();
        }
    }, 1500);
}

async function loadRefreshTimestamp() {
    try {
        const s = await API.getRefreshStatus();
        _renderRefreshTimestamp(s.last_refresh_at);
    } catch (e) {}
}

async function refreshData() {
    try {
        await API.refreshData();
    } catch (e) {
        showToast('更新失败：' + e.message, 4000, 'error');
        return;
    }
    _startRefreshPoll();
}

// === Tab 1: 总览 ===
async function loadDashboard() {
    try {
        [portfolioData, scoresData] = await Promise.all([
            API.getPortfolio(),
            API.getFundScores().catch(() => []),
        ]);
        renderSummaryCards(portfolioData);
        renderAllocationChart(portfolioData);
        renderReturnChart(portfolioData);
        renderHoldingsTable(portfolioData);
        populateBacktestFunds(portfolioData);
        document.getElementById('aiContextBar').style.display = 'block';
    } catch (e) {
        console.error('加载持仓失败:', e);
    }
}

function renderSummaryCards(data) {
    const profitClass = data.total_profit >= 0 ? 'text-profit' : 'text-loss';
    const dailyClass = data.daily_profit >= 0 ? 'text-profit' : 'text-loss';
    const sign = data.total_profit >= 0 ? '+' : '';
    const dailySign = data.daily_profit >= 0 ? '+' : '';

    document.getElementById('summaryCards').innerHTML = `
        <div class="summary-card">
            <div class="label">总资产</div>
            <div class="value">${formatMoney(data.total_value)}</div>
        </div>
        <div class="summary-card">
            <div class="label">持有收益</div>
            <div class="value ${profitClass}">${sign}${formatMoney(data.total_profit)}</div>
            <div class="sub ${profitClass}">${sign}${data.total_profit_rate.toFixed(2)}%</div>
        </div>
        <div class="summary-card">
            <div class="label">昨日收益</div>
            <div class="value ${dailyClass}">${dailySign}${formatMoney(data.daily_profit)}</div>
        </div>
        <div class="summary-card">
            <div class="label">持有基金数</div>
            <div class="value">${data.holdings.length}</div>
            <div class="sub text-neutral">债${countCategory(data, 'bond')} / 股${countCategory(data, 'equity')} / 混${countCategory(data, 'mixed')} / QDII ${countCategory(data, 'qdii')}</div>
        </div>
    `;
}

function renderAllocationChart(data) {
    const chart = echarts.init(document.getElementById('allocationChart'));
    const categoryNames = { bond: '债券型', equity: '股票型', mixed: '混合型', qdii: 'QDII/海外' };
    const colors = { bond: '#60a5fa', equity: '#f87171', mixed: '#fbbf24', qdii: '#34d399' };

    const pieData = Object.entries(data.allocation).map(([cat, pct]) => ({
        name: categoryNames[cat] || cat,
        value: pct,
        itemStyle: { color: colors[cat] || '#94a3b8' }
    }));

    chart.setOption({
        tooltip: { trigger: 'item', formatter: '{b}: {c}% ({d}%)' },
        series: [{
            type: 'pie',
            radius: ['40%', '70%'],
            label: { formatter: '{b}\n{c}%', fontSize: 13 },
            data: pieData,
        }]
    });
    window.addEventListener('resize', () => chart.resize());
}

function renderReturnChart(data) {
    const chart = echarts.init(document.getElementById('returnChart'));
    const holdings = [...data.holdings].sort((a, b) => a.profit_rate - b.profit_rate);

    chart.setOption({
        tooltip: { trigger: 'axis', formatter: params => {
            const p = params[0];
            return `${p.name}<br/>收益率: ${p.value >= 0 ? '+' : ''}${p.value}%`;
        }},
        grid: { left: 120, right: 40, top: 20, bottom: 20 },
        xAxis: { type: 'value', axisLabel: { formatter: '{value}%' } },
        yAxis: {
            type: 'category',
            data: holdings.map(h => truncateName(h.name, 10)),
            axisLabel: { fontSize: 12 },
        },
        series: [{
            type: 'bar',
            data: holdings.map(h => ({
                value: h.profit_rate,
                itemStyle: { color: h.profit_rate >= 0 ? '#ef4444' : '#10b981' }
            })),
            barWidth: 20,
            label: {
                show: true,
                position: 'right',
                formatter: p => `${p.value >= 0 ? '+' : ''}${p.value}%`,
                fontSize: 12,
            }
        }]
    });
    window.addEventListener('resize', () => chart.resize());
}

function toggleFilter(cat) {
    if (activeFilters.has(cat)) {
        activeFilters.delete(cat);
    } else {
        activeFilters.add(cat);
    }
    // 更新 chip 样式
    ['bond', 'equity', 'mixed', 'qdii'].forEach(c => {
        const chip = document.getElementById('chip-' + c);
        if (!chip) return;
        chip.classList.toggle('inactive', !activeFilters.has(c));
        chip.textContent = (activeFilters.has(c) ? '✓ ' : '') + { bond:'债券', equity:'股票', mixed:'混合', qdii:'QDII' }[c];
    });
    // 过滤行
    document.querySelectorAll('#holdingsBody tr').forEach(tr => {
        tr.style.display = activeFilters.has(tr.dataset.category) ? '' : 'none';
    });
}

function renderHoldingsTable(data) {
    const tbody = document.getElementById('holdingsBody');
    const categoryNames = { bond: '债券', equity: '股票', mixed: '混合', qdii: 'QDII' };
    const scoreMap = {};
    if (Array.isArray(scoresData)) {
        scoresData.forEach(s => { scoreMap[s.code] = s; });
    }

    tbody.innerHTML = data.holdings.map(h => {
        const profitClass = h.profit >= 0 ? 'text-profit' : 'text-loss';
        const dailyClass = h.daily_profit >= 0 ? 'text-profit' : 'text-loss';
        const sign = h.profit >= 0 ? '+' : '';
        const dailySign = h.daily_profit >= 0 ? '+' : '';

        const fmtPct = v => v == null ? '--'
            : `<span class="${v >= 0 ? 'text-profit' : 'text-loss'}">${v >= 0 ? '+' : ''}${v}%</span>`;

        const scoreInfo = scoreMap[h.code];
        const scoreCell = scoreInfo
            ? `<span class="score-badge ${scoreInfo.total_score >= 70 ? 'score-high' : scoreInfo.total_score >= 40 ? 'score-mid' : 'score-low'}">${scoreInfo.total_score}</span>`
            : '<span style="color:var(--text-secondary);font-size:12px;">--</span>';

        const industryChips = (h.top2_industries || [])
            .filter(ind => ind && ind.industry && ind.weight != null)
            .map(ind => {
                const desc = INDUSTRY_DESC[ind.industry] || '';
                return `<span class="industry-chip"${desc ? ` title="${desc}"` : ''}>${ind.industry} ${Math.round(ind.weight)}%</span>`;
            })
            .join('');

        return `<tr data-category="${h.category}" style="${activeFilters.has(h.category) ? '' : 'display:none'}">
            <td>
                <span class="fund-name" onclick="showFundDetail('${h.code}')">${h.name}</span>
                <div style="font-size:12px;color:var(--text-secondary);margin-top:2px;">${h.code}</div>
                ${industryChips ? `<div class="industry-chip-row">${industryChips}</div>` : ''}
            </td>
            <td><span class="type-badge ${h.category}">${categoryNames[h.category] || h.category}</span></td>
            <td>${formatMoney(h.current_value)}</td>
            <td class="${profitClass}">${sign}${formatMoney(h.profit)}</td>
            <td class="${profitClass}">${sign}${h.profit_rate}%</td>
            <td class="${dailyClass}">${dailySign}${formatMoney(h.daily_profit)}</td>
            <td>${fmtPct(h.ret_7d)}</td>
            <td>${fmtPct(h.ret_1m)}</td>
            <td>${fmtPct(h.ret_3m)}</td>
            <td>${renderSparkline(h.nav_curve)}</td>
            <td style="text-align:center;">${scoreCell}</td>
        </tr>`;
    }).join('');
}

function renderSparkline(curve) {
    if (!curve || curve.length < 2) return '<span style="color:var(--text-secondary);font-size:11px;">--</span>';

    const W = 100, H = 32, pad = 2;
    const vals = curve.map(p => p.pct);
    const min = Math.min(...vals), max = Math.max(...vals);
    const range = max - min || 0.001;

    const pts = vals.map((v, i) => {
        const x = pad + (i / (vals.length - 1)) * (W - pad * 2);
        const y = H - pad - ((v - min) / range) * (H - pad * 2);
        return `${x.toFixed(1)},${y.toFixed(1)}`;
    }).join(' ');

    const lastVal = vals[vals.length - 1];
    const color = lastVal >= 0 ? '#ef4444' : '#10b981';
    const zeroPct = (max / range) * (H - pad * 2);
    const zeroY = (pad + zeroPct).toFixed(1);

    return `<svg width="${W}" height="${H}" style="display:block;">
        <line x1="${pad}" y1="${zeroY}" x2="${W - pad}" y2="${zeroY}" stroke="#e2e8f0" stroke-width="1"/>
        <polyline points="${pts}" fill="none" stroke="${color}" stroke-width="1.5" stroke-linejoin="round"/>
        <circle cx="${(W - pad).toFixed(1)}" cy="${(H - pad - ((lastVal - min) / range) * (H - pad * 2)).toFixed(1)}" r="2" fill="${color}"/>
    </svg>`;
}

// === Tab 2: 风险分析 ===
async function loadRisk() {
    try {
        const [risk, corr] = await Promise.all([
            API.getRiskAnalysis(),
            API.getCorrelation()
        ]);
        riskData = risk;
        renderRiskScore(risk);
        renderRiskAlerts(risk.alerts);
        renderCorrelationChart(corr);
        renderVolatilityChart(risk.volatility);
    } catch (e) {
        console.error('加载风险分析失败:', e);
    }
}

function renderRiskScore(data) {
    const scoreEl = document.getElementById('riskScore');
    const levelEl = document.getElementById('riskLevel');
    const volEl = document.getElementById('portfolioVol');

    scoreEl.textContent = data.score;
    scoreEl.className = 'value ' + (data.score >= 70 ? 'text-loss' : data.score >= 40 ? '' : 'text-profit');

    const level = data.score >= 70 ? '风险较低' : data.score >= 40 ? '风险适中' : '风险较高';
    levelEl.textContent = level;
    volEl.textContent = data.volatility.portfolio_vol + '%';
}

function renderRiskAlerts(alerts) {
    const container = document.getElementById('riskAlerts');
    if (!alerts.length) {
        container.innerHTML = '<div class="empty-state"><p>暂无风险预警</p></div>';
        return;
    }

    const icons = { high: '🔴', medium: '🟡', low: '🟢' };
    container.innerHTML = alerts.map(a => `
        <div class="alert alert-${a.level}">
            <span class="icon">${icons[a.level]}</span>
            <div class="content">
                <div class="title">${a.message}</div>
                <div class="detail">${a.detail}</div>
            </div>
        </div>
    `).join('');
}

function renderCorrelationChart(data) {
    if (!data.matrix || !data.matrix.length) return;

    const chart = echarts.init(document.getElementById('correlationChart'));
    const names = data.names.map(n => truncateName(n, 6));
    const values = [];

    for (let i = 0; i < data.matrix.length; i++) {
        for (let j = 0; j < data.matrix[i].length; j++) {
            values.push([i, j, data.matrix[i][j]]);
        }
    }

    chart.setOption({
        tooltip: {
            formatter: p => `${data.names[p.value[0]]} vs ${data.names[p.value[1]]}<br/>相关系数: ${p.value[2]}`
        },
        grid: { left: 80, right: 40, top: 20, bottom: 80 },
        xAxis: { type: 'category', data: names, axisLabel: { rotate: 45, fontSize: 11 } },
        yAxis: { type: 'category', data: names, axisLabel: { fontSize: 11 } },
        visualMap: {
            min: -1, max: 1,
            inRange: { color: ['#3b82f6', '#ffffff', '#ef4444'] },
            orient: 'horizontal',
            left: 'center',
            bottom: 0,
            textStyle: { fontSize: 11 },
        },
        series: [{
            type: 'heatmap',
            data: values,
            label: { show: true, formatter: p => p.value[2].toFixed(2), fontSize: 11 },
        }]
    });
    window.addEventListener('resize', () => chart.resize());
}

function renderVolatilityChart(volData) {
    if (!volData || !volData.fund_vols) return;

    const chart = echarts.init(document.getElementById('volatilityChart'));
    const entries = Object.entries(volData.fund_vols);

    // 获取基金名称
    const names = entries.map(([code]) => {
        const h = portfolioData?.holdings.find(h => h.code === code);
        return h ? truncateName(h.name, 8) : code;
    });

    chart.setOption({
        tooltip: { trigger: 'axis' },
        grid: { left: 80, right: 20, top: 20, bottom: 40 },
        xAxis: { type: 'category', data: names, axisLabel: { rotate: 30, fontSize: 11 } },
        yAxis: { type: 'value', axisLabel: { formatter: '{value}%' } },
        series: [{
            type: 'bar',
            data: entries.map(([, vol]) => ({
                value: vol,
                itemStyle: { color: vol > 15 ? '#ef4444' : vol > 8 ? '#fbbf24' : '#60a5fa' }
            })),
            barWidth: 30,
            label: { show: true, position: 'top', formatter: '{c}%', fontSize: 11 }
        }]
    });
    window.addEventListener('resize', () => chart.resize());
}

// === Tab 3: 调仓建议 ===
async function loadRebalance() {
    try {
        const [suggestions, targets] = await Promise.all([
            API.getRebalanceSuggestions(),
            API.getTargetAllocation()
        ]);
        rebalanceData = suggestions;
        renderTargetSliders(targets);
        renderSuggestions(suggestions.suggestions);
        try { renderAllocationCompare(suggestions); } catch(e) { console.warn('Chart error:', e); }
    } catch (e) {
        console.error('加载调仓建议失败:', e);
    }
}

function renderAllocationCompare(data) {
    const chart = echarts.init(document.getElementById('allocationCompareChart'));
    const categoryNames = { bond: '债券型', equity: '股票型', mixed: '混合型', qdii: 'QDII' };
    const categories = Object.keys(data.target_allocation);
    const labels = categories.map(c => categoryNames[c] || c);

    chart.setOption({
        tooltip: { trigger: 'axis' },
        legend: { data: ['当前配置', '目标配置'], bottom: 0 },
        grid: { left: 60, right: 20, top: 20, bottom: 50 },
        xAxis: { type: 'category', data: labels },
        yAxis: { type: 'value', axisLabel: { formatter: '{value}%' } },
        series: [
            {
                name: '当前配置',
                type: 'bar',
                data: categories.map(c => data.current_allocation[c] || 0),
                itemStyle: { color: '#94a3b8' },
                barGap: '20%',
            },
            {
                name: '目标配置',
                type: 'bar',
                data: categories.map(c => data.target_allocation[c]),
                itemStyle: { color: '#3b82f6' },
            }
        ]
    });
    window.addEventListener('resize', () => chart.resize());
}

function renderTargetSliders(targets) {
    const categoryNames = { bond: '债券型', equity: '股票型', mixed: '混合型', qdii: 'QDII' };
    const container = document.getElementById('targetSliders');

    const html = Object.entries(targets).map(([cat, info]) => `
        <div class="slider-group">
            <label>${categoryNames[cat] || cat}</label>
            <input type="range" min="0" max="100" value="${info.target_pct}"
                   id="slider-${cat}" oninput="updateSliderValue('${cat}', this.value)">
            <span class="slider-value" id="sliderVal-${cat}">${info.target_pct}%</span>
        </div>
    `).join('');

    container.innerHTML = html;
}

function updateSliderValue(cat, val) {
    document.getElementById(`sliderVal-${cat}`).textContent = val + '%';
}

async function saveTargetAllocation() {
    const categories = ['bond', 'equity', 'mixed', 'qdii'];
    const allocations = {};
    let total = 0;

    categories.forEach(cat => {
        const slider = document.getElementById(`slider-${cat}`);
        if (slider) {
            allocations[cat] = parseFloat(slider.value);
            total += allocations[cat];
        }
    });

    if (Math.abs(total - 100) > 0.1) {
        alert(`配置比例之和为${total}%，需要等于100%`);
        return;
    }

    await API.setTargetAllocation({ allocations });
    rebalanceData = null;
    await loadRebalance();
}

function renderSuggestions(suggestions) {
    const container = document.getElementById('suggestions');
    const categoryNames = { bond: '债券型', equity: '股票型', mixed: '混合型', qdii: 'QDII' };

    if (!suggestions.length) {
        container.innerHTML = '<div class="empty-state"><p>暂无调仓建议</p></div>';
        return;
    }

    container.innerHTML = suggestions.map(s => {
        const badgeClass = s.action === 'buy' ? 'badge-buy' : s.action === 'sell' ? 'badge-sell' : 'badge-hold';
        const actionText = s.action === 'buy' ? '加仓' : s.action === 'sell' ? '减仓' : '持有';
        const sign = s.delta_pct >= 0 ? '+' : '';
        const amountAbs = Math.abs(s.amount);

        // 推荐基金（带评分标签解析）
        const parsedFunds = s.fund_suggestions.map(f => {
            const m = f.match(/^(.+?)\s*\[(\d+)分\]$/);
            if (m) {
                const score = parseInt(m[2]);
                const cls = score >= 70 ? 'score-high' : score >= 50 ? 'score-mid' : 'score-low';
                return `${m[1]} <span class="score-badge ${cls}">${score}</span>`;
            }
            return f;
        });
        const fundHtml = parsedFunds.length ? `<div style="font-size:12px;color:var(--primary);margin-top:4px;">
            推荐: ${parsedFunds.join(' / ')}
        </div>` : '';

        // 估值 note
        const noteHtml = s.note ? `<div style="font-size:12px;color:var(--text-secondary);margin-top:4px;font-style:italic;">
            ${s.note}
        </div>` : '';

        return `<div class="suggestion-item">
            <span class="suggestion-badge ${badgeClass}">${actionText}</span>
            <div style="flex:1;">
                <div style="font-weight:500;">${s.category_name}</div>
                <div style="font-size:13px;color:var(--text-secondary);">
                    当前 ${s.current_pct}% → 目标 ${s.target_pct}% (${sign}${s.delta_pct}%)
                </div>
                ${fundHtml}${noteHtml}
            </div>
            <div style="text-align:right;">
                <div style="font-weight:600;font-size:16px;" class="${s.action === 'sell' ? 'text-profit' : 'text-loss'}">${s.action === 'sell' ? '-' : '+'}${formatMoney(amountAbs)}</div>
                <div style="font-size:12px;color:var(--text-secondary);">元</div>
            </div>
        </div>`;
    }).join('');
}

async function loadTransitionPlan() {
    const months = parseInt(document.getElementById('transMonths').value) || 6;
    const monthly = parseFloat(document.getElementById('transMonthly').value) || 5000;

    const plan = await API.getTransitionPlan(months, monthly);
    if (!plan.length) return;

    const chart = echarts.init(document.getElementById('transitionChart'));
    const categoryNames = { bond: '债券型', equity: '股票型', mixed: '混合型', qdii: 'QDII' };
    const colors = { bond: '#60a5fa', equity: '#f87171', mixed: '#fbbf24', qdii: '#34d399' };
    const categories = Object.keys(plan[0].allocation_after);

    chart.setOption({
        tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
        legend: { data: categories.map(c => categoryNames[c] || c), bottom: 0 },
        grid: { left: 50, right: 20, top: 20, bottom: 50 },
        xAxis: { type: 'category', data: plan.map(p => `第${p.month}月`) },
        yAxis: { type: 'value', axisLabel: { formatter: '{value}%' }, max: 100 },
        series: categories.map(cat => ({
            name: categoryNames[cat] || cat,
            type: 'bar',
            stack: 'total',
            data: plan.map(p => p.allocation_after[cat] || 0),
            itemStyle: { color: colors[cat] || '#94a3b8' },
        }))
    });
    window.addEventListener('resize', () => chart.resize());
}

// === Tab 4: 回测 ===
function onBacktestTypeChange() {
    const type = document.getElementById('btType').value;
    document.getElementById('btTPGroup').style.display = type === 'takeprofit' ? 'block' : 'none';
}

function populateBacktestFunds(data) {
    const select = document.getElementById('btFund');
    if (!data || !data.holdings) return;

    select.innerHTML = data.holdings.map(h =>
        `<option value="${h.code}">${h.name} (${h.code})</option>`
    ).join('');

    // 设置默认日期
    const today = new Date();
    const twoYearsAgo = new Date(today);
    twoYearsAgo.setFullYear(today.getFullYear() - 2);

    document.getElementById('btEnd').value = today.toISOString().split('T')[0];
    document.getElementById('btStart').value = twoYearsAgo.toISOString().split('T')[0];
}

async function runBacktest() {
    const btn = document.getElementById('btRunBtn');
    btn.disabled = true;
    btn.textContent = '回测中...';

    try {
        const type = document.getElementById('btType').value;
        const params = {
            code: document.getElementById('btFund').value,
            amount: parseFloat(document.getElementById('btAmount').value),
            frequency: document.getElementById('btFreq').value,
            start_date: document.getElementById('btStart').value,
            end_date: document.getElementById('btEnd').value,
        };

        let result;
        if (type === 'dca') {
            result = await API.backtestDCA(params);
        } else {
            params.take_profit_pct = parseFloat(document.getElementById('btTP').value);
            result = await API.backtestTakeProfit(params);
        }

        if (result.error) {
            document.getElementById('btResults').innerHTML =
                `<div class="empty-state"><p>${result.error}</p></div>`;
            return;
        }

        renderBacktestResults(result, type);
        renderBacktestCurve(result);
    } catch (e) {
        console.error('回测失败:', e);
    }

    btn.disabled = false;
    btn.textContent = '开始回测';
}

function renderBacktestResults(data, type) {
    const container = document.getElementById('btResults');
    const returnClass = data.total_return >= 0 ? 'text-profit' : 'text-loss';

    let metricsHtml = `
        <div class="metrics-grid">
            <div class="metric-item">
                <div class="metric-value">${formatMoney(data.total_invested)}</div>
                <div class="metric-label">总投入</div>
            </div>
            <div class="metric-item">
                <div class="metric-value">${formatMoney(data.final_value || data.total_returned)}</div>
                <div class="metric-label">最终价值</div>
            </div>
            <div class="metric-item">
                <div class="metric-value ${returnClass}">${data.total_return >= 0 ? '+' : ''}${data.total_return}%</div>
                <div class="metric-label">总收益率</div>
            </div>
    `;

    if (type === 'dca') {
        metricsHtml += `
            <div class="metric-item">
                <div class="metric-value ${returnClass}">${data.annualized_return >= 0 ? '+' : ''}${data.annualized_return}%</div>
                <div class="metric-label">年化收益率</div>
            </div>
            <div class="metric-item">
                <div class="metric-value">${data.max_drawdown}%</div>
                <div class="metric-label">最大回撤</div>
            </div>
            <div class="metric-item">
                <div class="metric-value">${data.total_periods}</div>
                <div class="metric-label">定投期数</div>
            </div>
            <div class="metric-item">
                <div class="metric-value">${data.avg_cost}</div>
                <div class="metric-label">平均成本</div>
            </div>
            <div class="metric-item">
                <div class="metric-value">${data.current_nav}</div>
                <div class="metric-label">最新净值</div>
            </div>
        `;
    } else {
        const adv = data.comparison?.advantage || 0;
        const advClass = adv >= 0 ? 'text-profit' : 'text-loss';
        metricsHtml += `
            <div class="metric-item">
                <div class="metric-value">${data.num_cycles}</div>
                <div class="metric-label">止盈次数</div>
            </div>
            <div class="metric-item">
                <div class="metric-value">${data.comparison?.pure_dca_return || 0}%</div>
                <div class="metric-label">纯定投收益</div>
            </div>
            <div class="metric-item">
                <div class="metric-value ${advClass}">${adv >= 0 ? '+' : ''}${adv}%</div>
                <div class="metric-label">止盈优势</div>
            </div>
        `;
    }

    metricsHtml += '</div>';
    container.innerHTML = metricsHtml;
}

function renderBacktestCurve(data) {
    const card = document.getElementById('btChartCard');
    card.style.display = 'block';

    const chart = echarts.init(document.getElementById('btCurveChart'));
    const curve = data.curve || [];

    chart.setOption({
        tooltip: {
            trigger: 'axis',
            formatter: params => {
                const p = params[0];
                const d = curve[p.dataIndex];
                return `${d.date}<br/>投入: ${formatMoney(d.invested)}<br/>市值: ${formatMoney(d.value)}<br/>收益率: ${d.return_pct}%`;
            }
        },
        legend: { data: ['投入金额', '持有市值'], bottom: 0 },
        grid: { left: 60, right: 20, top: 20, bottom: 50 },
        xAxis: {
            type: 'category',
            data: curve.map(c => c.date),
            axisLabel: { formatter: v => v.slice(5), rotate: 30 }
        },
        yAxis: { type: 'value', axisLabel: { formatter: v => (v / 10000).toFixed(1) + '万' } },
        series: [
            {
                name: '投入金额',
                type: 'line',
                data: curve.map(c => c.invested),
                lineStyle: { color: '#94a3b8' },
                itemStyle: { color: '#94a3b8' },
                showSymbol: false,
                areaStyle: { color: 'rgba(148,163,184,0.1)' },
            },
            {
                name: '持有市值',
                type: 'line',
                data: curve.map(c => c.value),
                lineStyle: { color: '#3b82f6' },
                itemStyle: { color: '#3b82f6' },
                showSymbol: false,
                areaStyle: { color: 'rgba(59,130,246,0.1)' },
            }
        ]
    });
    window.addEventListener('resize', () => chart.resize());
}

// === 基金详情模态框：档案/同类排名/投资范围三大块 ===
function renderFundMetaBlock(d) {
    const hasAny = d.company || d.inception_date || d.aum != null || d.manager;
    if (!hasAny) return '';
    const aumStr = d.aum != null ? `${d.aum.toFixed(2)} 亿元` : '--';
    const concentration = d.industry_concentration && d.industry_concentration > 0
        ? ` · 前3大行业集中度 <strong>${d.industry_concentration.toFixed(1)}%</strong>` : '';
    return `
    <div class="info-block">
        <div class="info-title">基金档案${concentration}</div>
        <div class="info-grid">
            <div><label>基金公司</label><span>${d.company || '--'}</span></div>
            <div><label>成立日期</label><span>${d.inception_date || '--'}</span></div>
            <div><label>基金规模</label><span>${aumStr}</span></div>
            <div><label>基金经理</label><span>${d.manager || '--'}</span></div>
        </div>
    </div>`;
}

function renderPeerRankBlock(pr) {
    if (!pr || pr.rank_1y == null && pr.rank_1m == null) return '';
    const periods = [['近1月','1m'],['近3月','3m'],['近6月','6m'],['近1年','1y'],['近3年','3y']];
    const rows = periods.map(([label, key]) => {
        const rank = pr[`rank_${key}`];
        const pct = pr[`pct_${key}`];
        if (rank == null) return '';
        const color = pct <= 25 ? '#16a34a' : pct <= 50 ? '#f59e0b' : '#ef4444';
        const tier = pct <= 10 ? '顶尖' : pct <= 25 ? '优秀' : pct <= 50 ? '良好' : pct <= 75 ? '一般' : '落后';
        return `<div class="peer-row">
            <span>${label}</span>
            <span>第 ${rank} / ${pr.peer_total}</span>
            <div class="peer-bar"><div style="width:${Math.max(3, 100 - pct)}%;background:${color};"></div></div>
            <span style="color:${color};font-weight:600;text-align:right;">${tier} ${Math.round(pct)}%</span>
        </div>`;
    }).join('');
    if (!rows) return '';
    return `
    <div class="info-block peer-rank-card">
        <div class="info-title">同类排名 <span style="color:var(--text-secondary);font-size:12px;font-weight:400;">（同类 ${pr.peer_total} 只）</span></div>
        ${rows}
    </div>`;
}

function renderFundScopeBlock(scope) {
    if (!scope) return '';
    return `
    <div class="info-block">
        <div class="info-title">投资范围与策略</div>
        <div class="fm-scope">${scope.replace(/&/g,'&amp;').replace(/</g,'&lt;')}</div>
    </div>`;
}

// === 基金详情模态框 ===
async function showFundDetail(code) {
    document.getElementById('fundModal').style.display = 'flex';
    document.getElementById('modalContent').innerHTML = '<div class="loading">加载中</div>';

    try {
        const detail = await API.getHoldingDetail(code);
        if (detail.error) {
            document.getElementById('modalContent').innerHTML = `<p>${detail.error}</p>`;
            return;
        }

        document.getElementById('modalTitle').textContent = detail.name;

        const profitClass = detail.profit >= 0 ? 'text-profit' : 'text-loss';
        const sign = detail.profit >= 0 ? '+' : '';

        document.getElementById('modalContent').innerHTML = `
            <div class="metrics-grid">
                <div class="metric-item">
                    <div class="metric-value">${formatMoney(detail.current_value)}</div>
                    <div class="metric-label">持有金额</div>
                </div>
                <div class="metric-item">
                    <div class="metric-value ${profitClass}">${sign}${formatMoney(detail.profit)}</div>
                    <div class="metric-label">持有收益</div>
                </div>
                <div class="metric-item">
                    <div class="metric-value ${profitClass}">${sign}${detail.profit_rate}%</div>
                    <div class="metric-label">收益率</div>
                </div>
                <div class="metric-item">
                    <div class="metric-value">${detail.current_nav}</div>
                    <div class="metric-label">最新净值</div>
                </div>
            </div>
            <div style="font-size:13px;color:var(--text-secondary);margin-top:8px;">
                <p>类型: ${detail.fund_type} | 风险等级: ${detail.risk_level || '-'}</p>
                <p>基金经理: ${detail.manager || '-'} | 持有份额: ${detail.shares}</p>
            </div>
            ${renderFundMetaBlock(detail)}
            ${renderPeerRankBlock(detail.peer_rank)}
            ${renderFundScopeBlock(detail.scope)}
        `;

        // 持仓穿透（非债券基金）
        const nonBondCategories = ['equity', 'mixed', 'qdii'];
        if (nonBondCategories.includes(detail.category)) {
            loadFundHoldingsInModal(code);
        } else {
            document.getElementById('modalHoldingsSection').style.display = 'none';
        }

        // 渲染净值走势
        if (detail.nav_history && detail.nav_history.length) {
            const navData = [...detail.nav_history].reverse();
            const chart = echarts.init(document.getElementById('modalNavChart'));

            chart.setOption({
                tooltip: {
                    trigger: 'axis',
                    formatter: params => `${params[0].axisValue}<br/>净值: ${params[0].value}`
                },
                grid: { left: 50, right: 20, top: 10, bottom: 30 },
                xAxis: {
                    type: 'category',
                    data: navData.map(d => d.date),
                    axisLabel: { formatter: v => v.slice(5) }
                },
                yAxis: { type: 'value', scale: true },
                series: [{
                    type: 'line',
                    data: navData.map(d => d.nav),
                    showSymbol: false,
                    lineStyle: { color: '#3b82f6', width: 2 },
                    areaStyle: { color: 'rgba(59,130,246,0.08)' },
                }]
            });
        }
    } catch (e) {
        document.getElementById('modalContent').innerHTML = '<p>加载失败</p>';
    }
}

async function loadFundHoldingsInModal(code) {
    const section = document.getElementById('modalHoldingsSection');
    const tableEl = document.getElementById('modalHoldingsTable');
    const dateEl = document.getElementById('modalHoldingsDate');
    section.style.display = 'block';
    tableEl.innerHTML = '<div class="loading">加载中...</div>';

    try {
        const [stocks, industry] = await Promise.all([
            API.getFundStocks(code),
            API.getFundIndustry(code),
        ]);

        if (!stocks.stocks || !stocks.stocks.length) {
            tableEl.innerHTML = '<div class="empty-state"><p>暂无持仓数据，请先抓取</p></div>';
            return;
        }

        dateEl.textContent = stocks.report_date ? `截至 ${stocks.report_date}` : '';

        // 持仓股票表格
        tableEl.innerHTML = `
            <table class="holdings-table">
                <thead>
                    <tr>
                        <th>股票名称</th>
                        <th>代码</th>
                        <th style="text-align:right;">占净值比</th>
                    </tr>
                </thead>
                <tbody>
                    ${stocks.stocks.map((s, i) => `
                    <tr>
                        <td style="font-size:13px;">${s.stock_name}</td>
                        <td style="font-size:12px;color:var(--text-secondary);">${s.stock_code}</td>
                        <td style="text-align:right;font-size:13px;">${s.weight != null ? s.weight + '%' : '--'}</td>
                    </tr>`).join('')}
                </tbody>
            </table>`;

        // 行业分布饼图
        if (industry.industries && industry.industries.length) {
            const chart = echarts.init(document.getElementById('modalIndustryChart'));
            chart.setOption({
                tooltip: {
                    trigger: 'item',
                    formatter: p => {
                        const desc = INDUSTRY_DESC[p.name] || '';
                        return `<b>${p.name}: ${p.value}%</b>` +
                            (desc ? `<div style="font-size:11px;color:#94a3b8;max-width:180px;margin-top:3px;white-space:normal;line-height:1.4;">${desc}</div>` : '');
                    }
                },
                series: [{
                    type: 'pie',
                    radius: ['35%', '65%'],
                    center: ['50%', '50%'],
                    label: { formatter: '{b}\n{c}%', fontSize: 11 },
                    data: industry.industries.map(i => ({ name: i.industry, value: i.weight })),
                }]
            });
        }
    } catch (e) {
        tableEl.innerHTML = '<div class="empty-state"><p>加载失败</p></div>';
    }
}

function closeFundModal(event) {
    if (event.target.id === 'fundModal') {
        document.getElementById('fundModal').style.display = 'none';
    }
}

// === 工具函数 ===
function formatMoney(n) {
    if (n === undefined || n === null) return '--';
    return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

function truncateName(name, maxLen) {
    return name.length > maxLen ? name.slice(0, maxLen) + '...' : name;
}

function countCategory(data, cat) {
    return data.holdings.filter(h => h.category === cat).length;
}

// === Tab 5: 持仓透视 ===
async function loadHoldings() {
    try {
        const [overlap, industry] = await Promise.all([
            API.getHoldingsOverlap(),
            API.getIndustryTotal(),
        ]);
        holdingsData = { overlap, industry };
        renderDiversificationScore(overlap);
        renderOverlapStocks(overlap);
        renderIndustryBreakdown(industry);
    } catch (e) {
        console.error('加载持仓透视失败:', e);
        document.getElementById('overlapStocksList').innerHTML =
            '<div class="empty-state"><p>加载失败，请先点击「抓取持仓数据」</p></div>';
    }
}

function renderDiversificationScore(data) {
    const score = data.diversification_score ?? '--';
    const count = data.total_funds_analyzed ?? '--';
    const date = data.report_date ? `数据截至 ${data.report_date}` : '暂无数据';
    const overlapCount = data.overlap_stocks ? data.overlap_stocks.length : '--';

    document.getElementById('diversificationScore').textContent = score;
    document.getElementById('analyzedFundsCount').textContent = count;
    document.getElementById('holdingsReportDate').textContent = date;
    document.getElementById('overlapCount').textContent = overlapCount;
}

function renderOverlapStocks(data) {
    const container = document.getElementById('overlapStocksList');
    const stocks = data.overlap_stocks || [];

    if (!stocks.length) {
        container.innerHTML = '<div class="empty-state"><p>暂无重叠持仓数据<br><small>请先点击「抓取持仓数据」</small></p></div>';
        return;
    }

    container.innerHTML = stocks.map(s => {
        const fundBadges = s.fund_weights.map(fw =>
            `<span class="overlap-fund-badge" title="${fw.fund_name}（${fw.weight}%）">${fw.fund_name.slice(0, 4)}</span>`
        ).join('');
        const exposureClass = s.total_exposure_pct > 1 ? 'text-loss' : '';
        return `
        <div class="overlap-stock-item">
            <div class="overlap-stock-main">
                <span class="overlap-stock-name">${s.stock_name}</span>
                <span class="overlap-stock-code">${s.stock_code}</span>
                <span class="overlap-appear-badge">${s.appear_count} 只基金</span>
            </div>
            <div class="overlap-stock-sub">
                <span class="overlap-funds-list">${fundBadges}</span>
                <span class="overlap-exposure ${exposureClass}">实质暴露 ${s.total_exposure_pct.toFixed(2)}%</span>
            </div>
        </div>`;
    }).join('');
}

function renderIndustryBreakdown(data) {
    const industries = data.industries || [];
    if (!industries.length) return;

    if (data.report_date) {
        document.getElementById('industryReportDate').textContent = `数据截至 ${data.report_date}`;
    }

    const chart = echarts.init(document.getElementById('industryBreakdownChart'));
    const top10 = industries.slice(0, 10);

    chart.setOption({
        tooltip: {
            trigger: 'axis',
            axisPointer: { type: 'shadow' },
            formatter: params => {
                const p = params[0];
                const desc = INDUSTRY_DESC[p.name] || '';
                return `<div style="font-weight:600;margin-bottom:2px;">${p.name}</div>` +
                    `<div>加权占比: <b>${p.value.toFixed(2)}%</b></div>` +
                    (desc ? `<div style="font-size:11px;color:#94a3b8;max-width:220px;margin-top:5px;white-space:normal;line-height:1.5;">${desc}</div>` : '');
            }
        },
        grid: { left: 90, right: 50, top: 10, bottom: 20 },
        xAxis: {
            type: 'value',
            axisLabel: { formatter: '{value}%', fontSize: 11 },
        },
        yAxis: {
            type: 'category',
            data: top10.map(i => i.industry).reverse(),
            axisLabel: { fontSize: 11 },
        },
        series: [{
            type: 'bar',
            data: top10.map(i => i.total_weight).reverse(),
            barWidth: 16,
            itemStyle: { color: '#3b82f6', borderRadius: [0, 3, 3, 0] },
            label: { show: true, position: 'right', formatter: '{c}%', fontSize: 11 },
        }]
    });
    window.addEventListener('resize', () => chart.resize());
}

async function refreshHoldingsData() {
    const btn = document.getElementById('refreshHoldingsBtn');
    const logPanel = document.getElementById('holdingsLogPanel');
    btn.disabled = true;
    btn.textContent = '抓取中...';
    logPanel.innerHTML = '';
    logPanel.style.display = 'block';

    try {
        await API.refreshHoldings();
    } catch (e) {
        logPanel.innerHTML = `<div style="color:#dc2626">✗ 启动失败：${e}</div>`;
        btn.disabled = false;
        btn.textContent = '抓取持仓数据';
        return;
    }

    const poll = setInterval(async () => {
        let s;
        try {
            s = await API.getHoldingsStatus();
        } catch (e) {
            return; // 网络抖动时跳过本次轮询，不停止
        }

        logPanel.innerHTML = s.logs.map(l => {
            let color = '#374151';
            if (l.startsWith('✓')) color = '#16a34a';
            else if (l.startsWith('✗')) color = '#dc2626';
            else if (l.startsWith('—')) color = '#9ca3af';
            else if (l.startsWith('完成')) color = '#1d4ed8';
            return `<div style="color:${color};line-height:1.6">${l}</div>`;
        }).join('');
        logPanel.scrollTop = logPanel.scrollHeight;

        if (!s.running) {
            clearInterval(poll);
            btn.disabled = false;
            btn.textContent = '抓取持仓数据';
            if (s.result && s.result.refreshed && s.result.refreshed.length > 0) {
                holdingsData = null;
                await loadHoldings();
            }
        }
    }, 1500);
}

// === v2.0: 市场估值信号 ===
async function loadValuation() {
    try {
        valuationData = await API.getMarketValuation();
        renderValuationSignals(valuationData);
    } catch (e) {
        console.error('加载估值信号失败:', e);
    }
}

function renderValuationSignals(data) {
    const card = document.getElementById('valuationCard');
    const content = document.getElementById('valuationContent');
    if (!data) return;

    const hs = data.hs300 || {};
    const cs = data.csi500 || {};

    if (!hs.current_pe && !cs.current_pe) {
        card.style.display = 'none';
        return;
    }

    card.style.display = 'block';

    const signalColor = s => s === '低估' ? '#10b981' : s === '高估' ? '#ef4444' : '#f59e0b';
    const signalBg = s => s === '低估' ? '#f0fdf4' : s === '高估' ? '#fef2f2' : '#fffbeb';

    const renderBar = (info, label) => {
        if (!info.current_pe) return '';
        const pct = info.percentile || 0;
        const color = signalColor(info.signal);
        const bg = signalBg(info.signal);
        return `
        <div style="margin-bottom:14px;">
            <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:6px;">
                <span style="font-size:14px;font-weight:500;">${label}</span>
                <span style="display:flex;align-items:center;gap:8px;">
                    <span style="font-size:13px;color:var(--text-secondary);">PE ${info.current_pe} · PB ${info.current_pb || '--'}</span>
                    <span style="padding:2px 10px;border-radius:10px;font-size:12px;font-weight:600;background:${bg};color:${color};">${info.signal}</span>
                </span>
            </div>
            <div style="height:8px;background:#e2e8f0;border-radius:4px;position:relative;">
                <div style="height:100%;width:${pct}%;background:${color};border-radius:4px;transition:width .4s;"></div>
                <div style="position:absolute;top:-20px;left:calc(${pct}% - 16px);font-size:11px;color:${color};font-weight:600;">${pct}%</div>
            </div>
            <div style="display:flex;justify-content:space-between;font-size:11px;color:var(--text-secondary);margin-top:4px;">
                <span>历史最低</span><span>近10年百分位</span><span>历史最高</span>
            </div>
        </div>`;
    };

    content.innerHTML = renderBar(hs, '沪深300') + renderBar(cs, '中证500') +
        `<div style="font-size:11px;color:var(--text-secondary);margin-top:4px;">
            百分位 &lt;30% 低估 · 30-70% 合理 · &gt;70% 高估 · 更新于 ${data.updated_at || '--'}
        </div>`;
}

// === v2.0: AI 持仓解读 ===
async function openAIContext() {
    document.getElementById('aiContextModal').style.display = 'flex';
    document.getElementById('aiContextLoading').style.display = 'inline-block';
    document.getElementById('aiContextText').value = '';
    document.getElementById('copyContextBtn').textContent = '复制';

    try {
        const result = await API.getAIContext();
        document.getElementById('aiContextText').value = result.context || '';
    } catch (e) {
        document.getElementById('aiContextText').value = '生成失败，请检查网络或重试。';
    }
    document.getElementById('aiContextLoading').style.display = 'none';
}

function closeAIContextModal(event) {
    if (event.target.id === 'aiContextModal') {
        document.getElementById('aiContextModal').style.display = 'none';
    }
}

function copyAIContext() {
    const text = document.getElementById('aiContextText').value;
    if (!text) return;
    navigator.clipboard.writeText(text).then(() => {
        const btn = document.getElementById('copyContextBtn');
        btn.textContent = '已复制!';
        setTimeout(() => { btn.textContent = '复制'; }, 2000);
    });
}

// === 交易记录 ===
function openTxModal() {
    const select = document.getElementById('txFund');
    select.innerHTML = (portfolioData?.holdings || []).map(h =>
        `<option value="${h.code}">${h.name} (${h.code})</option>`
    ).join('');
    document.getElementById('txDate').value = new Date().toISOString().split('T')[0];
    document.getElementById('txAmount').value = '';
    document.getElementById('txNav').value = '';
    document.getElementById('txFee').value = '0';
    document.getElementById('txError').style.display = 'none';
    document.getElementById('txModal').style.display = 'flex';
}

function closeTxModal(event) {
    if (event.target.id === 'txModal') {
        document.getElementById('txModal').style.display = 'none';
    }
}

async function submitTransaction() {
    const errEl = document.getElementById('txError');
    const code = document.getElementById('txFund').value;
    const type = document.getElementById('txType').value;
    const date = document.getElementById('txDate').value;
    const amount = parseFloat(document.getElementById('txAmount').value);
    const navVal = document.getElementById('txNav').value;
    const fee = parseFloat(document.getElementById('txFee').value) || 0;

    if (!code || !date || isNaN(amount) || amount <= 0) {
        errEl.textContent = '请填写基金、日期和金额';
        errEl.style.display = 'block';
        return;
    }

    try {
        const result = await API.recordTransaction({
            code, type, date, amount,
            nav: navVal ? parseFloat(navVal) : null,
            fee,
        });
        if (result.error) {
            errEl.textContent = result.error;
            errEl.style.display = 'block';
            return;
        }
        document.getElementById('txModal').style.display = 'none';
        // 刷新持仓数据
        portfolioData = null;
        scoresData = null;
        await loadDashboard();
    } catch (e) {
        errEl.textContent = '提交失败，请检查数据后重试';
        errEl.style.display = 'block';
    }
}

// === 软件版本 & 自动更新 ===

async function initVersionBadge() {
    try {
        const d = await (await fetch('/api/system/version')).json();
        const ver = d.git_tag || `v${d.version}`;
        document.getElementById('vBadgeText').textContent = ver;
        document.getElementById('upCurrentVer').textContent = ver;
        if (d.changelog_url) document.getElementById('upChangelogLink').href = d.changelog_url;
    } catch (e) {}
    checkForUpdate();
}

async function checkForUpdate() {
    const btn = document.getElementById('upRefreshBtn');
    if (btn) { btn.style.color = '#3b82f6'; btn.textContent = '⟳'; }
    try {
        const d = await (await fetch('/api/system/check-update')).json();
        const dot = document.getElementById('vBadgeDot');
        const latestRow = document.getElementById('upLatestRow');
        const newBanner = document.getElementById('upNewBanner');
        const latestNotice = document.getElementById('upLatestNotice');
        const actionBtn = document.getElementById('upActionBtn');
        if (d.latest) latestRow.textContent = `最新版本: ${d.latest}`;
        if (d.changelog_url) document.getElementById('upChangelogLink').href = d.changelog_url;
        if (d.has_update) {
            dot.style.color = '#fb923c';
            newBanner.style.display = 'block';
            document.getElementById('upNewVer').textContent = d.latest;
            latestNotice.style.display = 'none';
            actionBtn.style.display = 'block';
            actionBtn.style.background = '#22c55e';
            actionBtn.textContent = '⬇ 立即更新';
            actionBtn.disabled = false;
            actionBtn.onclick = startUpdate;
        } else if (!d.error) {
            dot.style.color = 'rgba(255,255,255,0.4)';
            newBanner.style.display = 'none';
            latestNotice.style.display = 'block';
            actionBtn.style.display = 'none';
        }
    } catch (e) {}
    if (btn) { btn.style.color = '#94a3b8'; btn.textContent = '↻'; }
}

function toggleUpdatePanel(e) {
    e.stopPropagation();
    const panel = document.getElementById('updatePanel');
    const overlay = document.getElementById('upOverlay');
    if (panel.style.display !== 'none') {
        closeUpdatePanel();
    } else {
        const rect = document.getElementById('versionBadge').getBoundingClientRect();
        panel.style.top = (rect.bottom + 6) + 'px';
        panel.style.left = rect.left + 'px';
        panel.style.display = 'block';
        overlay.style.display = 'block';
    }
}

function closeUpdatePanel() {
    const p = document.getElementById('updatePanel');
    const o = document.getElementById('upOverlay');
    if (p) p.style.display = 'none';
    if (o) o.style.display = 'none';
}

async function startUpdate() {
    const actionBtn = document.getElementById('upActionBtn');
    const logBox = document.getElementById('upLogBox');
    actionBtn.disabled = true;
    actionBtn.textContent = '更新中...';
    logBox.innerHTML = '';
    logBox.style.display = 'block';
    try {
        await fetch('/api/system/update', { method: 'POST' });
    } catch (e) {
        showToast('启动更新失败', 3000, 'error');
        actionBtn.disabled = false;
        actionBtn.textContent = '⬇ 立即更新';
        return;
    }
    const poll = setInterval(async () => {
        try {
            const s = await (await fetch('/api/system/update/status')).json();
            logBox.innerHTML = s.logs.map(l => {
                const c = (l.startsWith('✓') || l.startsWith('✅')) ? '#16a34a'
                        : l.startsWith('✗') ? '#dc2626'
                        : l.startsWith('⚠') ? '#d97706' : '#374151';
                return `<div style="color:${c}">${l}</div>`;
            }).join('');
            logBox.scrollTop = logBox.scrollHeight;
            if (s.done) {
                clearInterval(poll);
                if (s.success) {
                    actionBtn.style.background = '#6366f1';
                    actionBtn.textContent = '🔄 重启服务';
                    actionBtn.disabled = false;
                    actionBtn.onclick = restartServer;
                } else {
                    actionBtn.style.background = '#ef4444';
                    actionBtn.textContent = '重试';
                    actionBtn.disabled = false;
                    actionBtn.onclick = startUpdate;
                }
            }
        } catch (e) {}
    }, 1500);
}

async function restartServer() {
    const actionBtn = document.getElementById('upActionBtn');
    const logBox = document.getElementById('upLogBox');
    actionBtn.disabled = true;
    actionBtn.textContent = '重启中...';
    logBox.innerHTML += '<div style="color:#6366f1;margin-top:4px;">🔄 服务重启中，页面将自动刷新...</div>';
    logBox.scrollTop = logBox.scrollHeight;
    try { await fetch('/api/system/restart', { method: 'POST' }); } catch (e) {}
    const t0 = Date.now();
    const wait = setInterval(async () => {
        try {
            if ((await fetch('/api/system/version')).ok) {
                clearInterval(wait);
                location.reload();
            }
        } catch (e) {
            if (Date.now() - t0 > 30000) {
                clearInterval(wait);
                showToast('重启超时，请手动刷新页面', 6000, 'error');
            }
        }
    }, 1000);
}

// === 初始化 ===
loadDashboard();
loadRefreshTimestamp();
initVersionBadge();

// 页面加载时检查是否有正在进行的更新任务，有则自动接上进度显示
(async function resumeRefreshIfRunning() {
    try {
        const s = await API.getRefreshStatus();
        if (s.running) _startRefreshPoll();
    } catch (e) {}
})();
