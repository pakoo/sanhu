// === 关注列表模块 ===

let _wlFilter = 'all';     // 'all' | 'holding' | 'watchonly'
let _wlAllData = [];       // 全量数据缓存，用于前端过滤
let _wlSparkCharts = {};   // ECharts 实例缓存，避免重复 init

const CAT_NAMES = { bond: '债券型', equity: '股票型', mixed: '混合型', qdii: 'QDII/海外' };

// ── 数据加载 ────────────────────────────────────────────────────
async function loadWatchlist() {
    try {
        const data = await API.getWatchlist();
        watchlistData = data;
        _wlAllData = data;
        _renderWatchlist();
    } catch (e) {
        console.error('加载关注列表失败:', e);
    }
}

// ── 过滤器 ──────────────────────────────────────────────────────
function filterWatchlist(type) {
    _wlFilter = type;
    document.querySelectorAll('[id^="wl-chip-"]').forEach(el => el.classList.remove('active'));
    document.getElementById('wl-chip-' + type)?.classList.add('active');
    _renderWatchlist();
}

// ── 渲染主表 ────────────────────────────────────────────────────
function _renderWatchlist() {
    // 销毁旧 sparkline 实例
    Object.values(_wlSparkCharts).forEach(chart => { try { chart.dispose(); } catch(e) {} });
    _wlSparkCharts = {};

    let data = _wlAllData;
    if (_wlFilter === 'holding')  data = data.filter(d => d.is_holding);
    if (_wlFilter === 'watchonly') data = data.filter(d => !d.is_holding);

    // 更新 count badge
    const countEl = document.getElementById('watchlistCount');
    if (countEl) countEl.textContent = _wlAllData.length;

    // 更新市场估值迷你条（复用 valuationData）
    _renderWlValuation();

    const tbody = document.getElementById('watchlistBody');
    if (!tbody) return;

    if (data.length === 0) {
        if (_wlFilter !== 'all') {
            tbody.innerHTML = `<tr><td colspan="10" style="text-align:center;padding:32px;color:var(--text-secondary);">暂无符合筛选条件的基金</td></tr>`;
        } else {
            tbody.innerHTML = `<tr><td colspan="10">
                <div class="empty-state">
                    <div class="icon">👀</div>
                    <p>关注列表是空的</p>
                    <p class="sub">把想长期跟踪的基金加进来：</p>
                    <ul class="empty-action-list">
                        <li>点上方「+ 添加基金」搜索基金代码或名称</li>
                        <li>或在「选基」Tab 筛选到感兴趣的基金 → 点"加入关注"</li>
                        <li>加入后工具自动抓取持仓数据（约24小时内）</li>
                    </ul>
                </div>
            </td></tr>`;
        }
        return;
    }

    tbody.innerHTML = data.map((d, i) => {
        const catName = CAT_NAMES[d.category] || d.category || '--';
        const statusTag = d.is_holding
            ? '<span style="color:#22c55e;font-size:12px;">🟢 持仓中</span>'
            : '<span style="color:var(--text-secondary);font-size:12px;">⚪ 自选</span>';

        const nav = d.latest_nav != null ? d.latest_nav.toFixed(4) : '--';
        const todayStr = _formatPct(d.daily_return);
        const ret7dStr = _formatPct(d.ret_7d);
        const ret1mStr = _formatPct(d.ret_1m);

        // 评分 badge
        const score = d.total_score;
        const scoreBadge = score != null
            ? `<span class="score-badge ${score >= 70 ? 'score-high' : score >= 40 ? 'score-mid' : 'score-low'}">${score}</span>`
            : '<span style="color:var(--text-secondary);font-size:12px;">--</span>';

        // sparkline 容器（ECharts 初始化后填充）
        const sparkId = `wl-spark-${d.code}`;

        return `<tr style="border-bottom:1px solid var(--border);">
            <td style="padding:8px 10px;">
                <div style="font-weight:500;font-size:14px;">${d.name}</div>
                <div style="font-size:12px;color:var(--text-secondary);">${d.code}</div>
            </td>
            <td style="padding:8px 10px;font-size:13px;color:var(--text-secondary);">${catName}</td>
            <td style="padding:8px 10px;">${statusTag}</td>
            <td style="padding:8px 10px;font-size:14px;text-align:right;">${nav}</td>
            <td style="padding:8px 10px;font-size:13px;text-align:right;" class="${_pctClass(d.daily_return)}">${todayStr}</td>
            <td style="padding:8px 10px;font-size:13px;text-align:right;" class="${_pctClass(d.ret_7d)}">${ret7dStr}</td>
            <td style="padding:8px 10px;font-size:13px;text-align:right;" class="${_pctClass(d.ret_1m)}">${ret1mStr}</td>
            <td style="padding:8px 10px;text-align:center;"><div id="${sparkId}" style="width:80px;height:40px;display:inline-block;"></div></td>
            <td style="padding:8px 10px;text-align:center;">${scoreBadge}</td>
            <td style="padding:8px 10px;text-align:center;">
                <button onclick="removeFundFromWatchlist('${d.code}','${d.name.replace(/'/g,"\\'")}','${d.is_holding}')"
                    title="移除关注" style="background:none;border:none;cursor:pointer;color:#ef4444;font-size:16px;">🗑</button>
            </td>
        </tr>`;
    }).join('');

    // 初始化 sparklines（异步，避免阻塞渲染）
    requestAnimationFrame(() => {
        data.forEach(d => {
            if (d.sparkline && d.sparkline.length >= 2) {
                _renderSparkline(`wl-spark-${d.code}`, d.sparkline);
            }
        });
        // 确保 tooltip ⓘ 挂载到动态渲染的表头（MutationObserver 无法捕获 CSS 显示变化）
        if (typeof initTermTooltips === 'function') {
            const panel = document.getElementById('watchlistBody');
            if (panel) initTermTooltips(panel.closest('table') || document);
        }
    });
}

function _formatPct(v) {
    if (v == null) return '--';
    const sign = v >= 0 ? '+' : '';
    return `${sign}${v.toFixed(2)}%`;
}

function _pctClass(v) {
    if (v == null) return '';
    return v >= 0 ? 'text-profit' : 'text-loss';
}

// ── Sparkline 迷你折线 ────────────────────────────────────────
function _renderSparkline(elId, navValues) {
    const el = document.getElementById(elId);
    if (!el || navValues.length < 2) return;

    let chart;
    try {
        chart = echarts.init(el);
    } catch (e) { return; }

    const isPositive = navValues[navValues.length - 1] >= navValues[0];
    const color = isPositive ? '#22c55e' : '#ef4444';

    chart.setOption({
        animation: false,
        grid: { top: 2, bottom: 2, left: 2, right: 2 },
        xAxis: { type: 'category', show: false, data: navValues.map((_, i) => i) },
        yAxis: { type: 'value', show: false, scale: true },
        series: [{
            type: 'line',
            data: navValues,
            symbol: 'none',
            lineStyle: { width: 1.5, color },
            areaStyle: { color, opacity: 0.12 },
        }],
    });

    _wlSparkCharts[elId] = chart;
}

// ── 市场估值迷你条 ────────────────────────────────────────────
function _renderWlValuation() {
    const el = document.getElementById('watchlistValuation');
    if (!el) return;

    if (!valuationData || !valuationData.hs300?.current_pe) {
        el.style.display = 'none';
        return;
    }

    el.style.display = 'block';
    const hs = valuationData.hs300;
    const cs = valuationData.csi500;

    const barHtml = (label, pct, signal) => {
        if (!pct) return '';
        const color = pct > 70 ? '#ef4444' : pct < 30 ? '#22c55e' : '#f59e0b';
        const sigColor = pct > 70 ? '#ef4444' : pct < 30 ? '#22c55e' : '#d97706';
        return `<div style="display:flex;align-items:center;gap:8px;font-size:12px;margin-bottom:4px;">
            <span style="color:var(--text-secondary);width:50px;">${label}</span>
            <div style="flex:1;height:6px;background:var(--border);border-radius:3px;max-width:120px;">
                <div style="width:${pct}%;height:100%;background:${color};border-radius:3px;"></div>
            </div>
            <span style="color:var(--text-secondary);">${pct}%分位</span>
            <span style="color:${sigColor};font-weight:500;">${signal}</span>
        </div>`;
    };

    el.innerHTML = `<div style="background:var(--bg-secondary);border-radius:8px;padding:10px 14px;">
        <div style="font-size:12px;color:var(--text-secondary);margin-bottom:6px;">市场估值参考</div>
        ${barHtml('沪深300', hs.percentile, hs.signal)}
        ${cs?.current_pe ? barHtml('中证500', cs.percentile, cs.signal) : ''}
    </div>`;
}

// ── 添加基金弹窗 ─────────────────────────────────────────────
let _addFundDebounce = null;
let _wlCodes = new Set();   // 已在关注列表中的 code

function openAddFundModal() {
    _wlCodes = new Set(_wlAllData.map(d => d.code));
    document.getElementById('addFundSearch').value = '';
    document.getElementById('addFundResults').innerHTML = '';
    document.getElementById('addFundModal').style.display = 'flex';
    setTimeout(() => document.getElementById('addFundSearch').focus(), 100);
}

function closeAddFundModal(event) {
    if (!event || event.target === document.getElementById('addFundModal')) {
        document.getElementById('addFundModal').style.display = 'none';
    }
}

function searchAddFund(q) {
    clearTimeout(_addFundDebounce);
    if (!q || q.length < 2) {
        document.getElementById('addFundResults').innerHTML = '';
        return;
    }
    _addFundDebounce = setTimeout(async () => {
        const resultsEl = document.getElementById('addFundResults');
        resultsEl.innerHTML = '<div style="padding:12px;color:var(--text-secondary);font-size:13px;">搜索中...</div>';
        try {
            const results = await API.searchFund(q);
            if (!results || results.length === 0) {
                resultsEl.innerHTML = '<div style="padding:12px;color:var(--text-secondary);font-size:13px;">未找到相关基金</div>';
                return;
            }
            resultsEl.innerHTML = results.slice(0, 10).map(r => {
                const inWl = _wlCodes.has(r.code);
                return `<div style="display:flex;align-items:center;justify-content:space-between;padding:10px 0;border-bottom:1px solid var(--border);">
                    <div>
                        <div style="font-size:14px;font-weight:500;">${r.name}</div>
                        <div style="font-size:12px;color:var(--text-secondary);">${r.code} · ${r.type || ''}</div>
                    </div>
                    ${inWl
                        ? '<span style="font-size:12px;color:var(--text-secondary);">已关注</span>'
                        : `<button class="btn btn-primary" onclick="addFundToWatchlist('${r.code}','${(r.name||'').replace(/'/g,"\\'")}',this)"
                            style="font-size:12px;padding:4px 10px;">+ 关注</button>`
                    }
                </div>`;
            }).join('');
        } catch (e) {
            resultsEl.innerHTML = `<div style="padding:12px;color:#ef4444;font-size:13px;">搜索失败：${e.message}</div>`;
        }
    }, 400);
}

async function addFundToWatchlist(code, name, btn) {
    if (btn) { btn.disabled = true; btn.textContent = '已关注'; }
    _wlCodes.add(code);

    // 立即关闭弹窗，列表插入占位行
    document.getElementById('addFundModal').style.display = 'none';
    _insertWlPlaceholder(code, name);

    // 后台异步拉取数据，完成后刷新列表
    try {
        await API.addToWatchlist(code);
        watchlistData = null;
        await loadWatchlist();
    } catch (e) {
        document.getElementById(`wl-placeholder-${code}`)?.remove();
        _wlCodes.delete(code);
        const countEl = document.getElementById('watchlistCount');
        if (countEl) countEl.textContent = Math.max(0, parseInt(countEl.textContent) - 1);
        alert('添加失败：' + e.message);
    }
}

function _insertWlPlaceholder(code, name) {
    const tbody = document.getElementById('watchlistBody');
    if (!tbody) return;

    // 清除"空列表"提示行
    if (tbody.querySelector('td[colspan]')) tbody.innerHTML = '';

    const tr = document.createElement('tr');
    tr.id = `wl-placeholder-${code}`;
    tr.style.borderBottom = '1px solid var(--border)';
    tr.innerHTML = `
        <td style="padding:8px 10px;">
            <div style="font-weight:500;font-size:14px;">${name}</div>
            <div style="font-size:12px;color:var(--text-secondary);">${code}</div>
        </td>
        <td style="padding:8px 10px;color:var(--text-secondary);">--</td>
        <td style="padding:8px 10px;"><span style="color:var(--text-secondary);font-size:12px;">⚪ 自选</span></td>
        <td colspan="6" style="padding:8px 10px;font-size:13px;color:var(--text-secondary);">
            <span style="display:inline-flex;align-items:center;gap:6px;">
                <span class="wl-spinner"></span>数据加载中…
            </span>
        </td>
        <td style="padding:8px 10px;"></td>`;
    tbody.insertBefore(tr, tbody.firstChild);

    const countEl = document.getElementById('watchlistCount');
    if (countEl) countEl.textContent = parseInt(countEl.textContent || 0) + 1;
}

async function removeFundFromWatchlist(code, name, isHolding) {
    const holdingWarn = isHolding === 'true'
        ? '\n注意：该基金仍在您的持仓中，移除关注不影响持仓记录。' : '';
    if (!confirm(`确认从关注列表移除「${name}」？${holdingWarn}`)) return;
    try {
        await API.removeFromWatchlist(code);
        watchlistData = null;
        await loadWatchlist();
    } catch (e) {
        alert('移除失败：' + e.message);
    }
}
