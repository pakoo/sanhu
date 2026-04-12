/* 决策日志 Tab — decisions.js */

(function () {
    'use strict';

    // ── init ─────────────────────────────────────────────────────────────────
    let _decisionsKindFilter = 'all';
    let _decisionModalBindingsReady = false;
    const _peerFundCache = {};

    window.decisionsInit = async function () {
        ensureDecisionModalBindings();
        await Promise.all([
            loadDecisionsAlerts(),
            loadDecisionsPending(),
            loadDecisionsHistory(),
        ]);
    };

    window.decisionsSetKind = function (kind) {
        _decisionsKindFilter = kind;
        // 更新按钮 active 样式
        document.querySelectorAll('#decisionKindFilter .kind-btn').forEach(b => {
            b.classList.toggle('active', b.getAttribute('data-kind') === kind);
        });
        // 只重新加载 pending 和 history（alerts 不受 kind 影响）
        loadDecisionsPending();
        loadDecisionsHistory();
    };

    function kindQS() {
        return `&kind=${_decisionsKindFilter}`;
    }

    // ── 告警区 ───────────────────────────────────────────────────────────────
    async function loadDecisionsAlerts() {
        const bar = document.getElementById('decisionsAlertBar');
        try {
            const res = await fetch('/api/decisions?status=triggered_tp,triggered_sl');
            const json = await res.json();
            const items = json.data || [];
            if (items.length === 0) {
                bar.innerHTML = '<div style="color:var(--success);font-size:13px;">✓ 今日无止盈/止损触发信号</div>';
                return;
            }
            bar.innerHTML = items.map(d => {
                const isTP = d.status === 'triggered_tp';
                const pnlStr = d.current_pnl_pct != null
                    ? `<span style="font-size:13px;color:${d.current_pnl_pct >= 0 ? 'var(--success)' : 'var(--danger)'};">
                         ${(d.current_pnl_pct * 100).toFixed(1)}%
                       </span>`
                    : '';
                return `
                    <div style="display:flex;align-items:center;gap:10px;padding:8px 12px;
                                background:${isTP ? '#dcfce7' : '#fee2e2'};border-radius:8px;margin-bottom:6px;flex-wrap:wrap;">
                        <span style="font-size:13px;font-weight:600;">${isTP ? '🎯 止盈触发' : '⚠️ 止损触发'}</span>
                        <span style="font-size:13px;font-weight:600;">${d.name || d.code}${d.name ? ` <span style="font-size:11px;color:var(--text-secondary);font-weight:400;">(${d.code})</span>` : ''}</span>
                        ${pnlStr}
                        <button onclick="decisionsShowDetail(${d.id})"
                                style="margin-left:auto;font-size:12px;padding:3px 10px;border:1px solid currentColor;
                                       border-radius:4px;background:transparent;cursor:pointer;">
                            查看复盘
                        </button>
                        <button onclick="decisionsMarkHandled(${d.id})"
                                style="font-size:12px;padding:3px 10px;border:1px solid var(--border);
                                       border-radius:4px;background:transparent;cursor:pointer;color:var(--text-secondary);">
                            标记已处理
                        </button>
                    </div>`;
            }).join('');
        } catch (e) {
            bar.innerHTML = `<div class="sub" style="color:var(--danger);font-size:13px;">告警加载失败: ${e.message}</div>`;
        }
    }

    // ── 待执行决策 ───────────────────────────────────────────────────────────
    async function loadDecisionsPending() {
        const el = document.getElementById('decisionsPendingList');
        try {
            const res = await fetch(`/api/decisions?status=pending${kindQS()}`);
            const json = await res.json();
            const items = json.data || [];
            Object.keys(_pendingCache).forEach(key => { delete _pendingCache[key]; });
            if (items.length === 0) {
                el.innerHTML = `
                    <div class="empty-state">
                        <div class="icon">📋</div>
                        <p>还没有待执行的决策</p>
                        <p class="sub">你可以：</p>
                        <ul class="empty-action-list">
                            <li>点右上角"<strong>+ 手动创建</strong>"添加一笔计划买入</li>
                            <li>或者勾选"🧪 虚拟建仓"先假装买，不花真钱</li>
                            <li>在「选基」Tab 生成推荐后点"创建决策"</li>
                        </ul>
                    </div>`;
                return;
            }
            // 缓存完整数据供 detail 面板读取
            items.forEach(d => { _pendingCache[d.id] = d; });
            el.innerHTML = items.map(d => renderPendingCard(d)).join('');
            // 异步加载每个基金的择时信号
            items.forEach(d => loadTimingBadge(d.code, `timing-${d.id}`));
        } catch (e) {
            el.innerHTML = `<div class="sub" style="color:var(--danger);font-size:13px;">加载失败: ${e.message}</div>`;
        }
    }

    function renderPendingCard(d) {
        const tp = d.target_tp_pct != null ? `止盈 +${(d.target_tp_pct * 100).toFixed(0)}%` : '';
        const sl = d.target_sl_pct != null ? `止损 ${(d.target_sl_pct * 100).toFixed(0)}%` : '';
        const amt = d.target_amount != null ? `计划 ¥${d.target_amount.toLocaleString()}` : '';
        const tags = [amt, tp, sl].filter(Boolean).join(' · ');
        const createdAt = (d.created_at || '').slice(0, 16).replace('T', ' ');
        const baseFundName = d.name || d.code;
        const isVirtual = !!d.is_virtual;
        const fundName = (isVirtual ? '🧪 ' : '') + baseFundName;
        const cardBorderLeft = isVirtual ? 'border-left:3px solid #3b82f6;' : '';
        const hasName = !!d.name;
        const fragments = Array.isArray(d.reason_fragments) ? d.reason_fragments : [];
        const topFragments = fragments.slice(0, 3);
        const hasMoreFragments = fragments.length > 3;

        // 核心理由区 (inline)
        let reasonBlock;
        if (fragments.length > 0) {
            reasonBlock = `
                <div style="background:#f8fafc;border-left:3px solid var(--primary);
                            padding:8px 12px;border-radius:4px;margin-bottom:10px;">
                    <div style="font-size:11px;color:var(--text-secondary);font-weight:600;margin-bottom:4px;">候选理由</div>
                    ${topFragments.map(f => `
                        <div style="font-size:12px;color:var(--text);line-height:1.7;">· ${f}</div>
                    `).join('')}
                    ${hasMoreFragments ? `<div style="font-size:11px;color:var(--text-secondary);margin-top:2px;">... 另 ${fragments.length - 3} 条，点"详细依据"查看</div>` : ''}
                </div>`;
        } else {
            reasonBlock = `
                <div style="background:#fef9c3;border-left:3px solid #ca8a04;
                            padding:8px 12px;border-radius:4px;margin-bottom:10px;font-size:12px;color:#713f12;">
                    ${d.source_session_id ? '此决策来自选基流程，但未能匹配到候选理由' : '手动创建，无 selector 判断依据'}
                </div>`;
        }

        return `
            <div style="border:1px solid var(--border);${cardBorderLeft}border-radius:12px;padding:14px 16px;margin-bottom:10px;">
                <div style="display:flex;align-items:flex-start;justify-content:space-between;gap:10px;margin-bottom:8px;">
                    <div style="flex:1;min-width:0;">
                        <div style="font-weight:700;font-size:15px;line-height:1.3;">
                            ${fundName}
                            <span style="margin-left:6px;font-size:12px;padding:2px 8px;border-radius:4px;
                                         background:#dbeafe;color:#1d4ed8;font-weight:500;">${decisionTypeCn(d.decision_type)}</span>
                        </div>
                        ${hasName ? `<div style="font-size:11px;color:var(--text-secondary);margin-top:2px;">${d.code}</div>` : ''}
                    </div>
                    <span id="timing-${d.id}" style="font-size:12px;padding:2px 10px;border-radius:12px;
                                                    background:var(--border);color:var(--text-secondary);white-space:nowrap;">
                        择时检测中...
                    </span>
                </div>

                <div style="font-size:13px;color:var(--text-secondary);margin-bottom:8px;">${tags || '无参数设置'}</div>

                ${reasonBlock}

                <div style="font-size:12px;color:var(--text-secondary);margin-bottom:10px;">${createdAt} 创建</div>

                <div id="pending-detail-${d.id}" style="display:none;margin-bottom:10px;
                        background:#f8fafc;border:1px solid var(--border);border-radius:6px;padding:12px;"></div>

                <div style="display:flex;gap:8px;flex-wrap:wrap;">
                    ${isVirtual
                        ? `<button class="btn" disabled
                                style="font-size:12px;padding:5px 14px;background:#eff6ff;border:1px solid #bfdbfe;color:#1d4ed8;cursor:not-allowed;">
                                🧪 虚拟仓位无需链接真实交易
                           </button>`
                        : `<button class="btn btn-primary" onclick="decisionsOpenLinkTx(${d.id})"
                                style="font-size:12px;padding:5px 14px;">✓ 已在支付宝下单</button>`}
                    <button class="btn" onclick="decisionsTogglePendingDetail(${d.id})"
                            style="font-size:12px;padding:5px 14px;background:#f8fafc;border:1px solid var(--border);">
                        <span id="pending-detail-btn-${d.id}">▾ 详细依据</span>
                    </button>
                    <button class="btn" onclick="decisionsExportReview(${d.id})"
                            style="font-size:12px;padding:5px 14px;background:#f8fafc;border:1px solid var(--border);">
                        📋 导出依据
                    </button>
                    <button class="btn" onclick="decisionsCancel(${d.id})"
                            style="font-size:12px;padding:5px 14px;color:var(--text-secondary);">取消决策</button>
                </div>
            </div>`;
    }

    // ── pending detail 折叠 ────────────────────────────────────────────────
    const _pendingCache = {};
    window.decisionsTogglePendingDetail = function (id) {
        const panel = document.getElementById(`pending-detail-${id}`);
        const btn = document.getElementById(`pending-detail-btn-${id}`);
        if (!panel) return;
        if (panel.style.display !== 'none') {
            panel.style.display = 'none';
            if (btn) btn.textContent = '▾ 详细依据';
            return;
        }
        panel.style.display = 'block';
        if (btn) btn.textContent = '▴ 收起';
        const d = _pendingCache[id];
        if (!d) {
            panel.innerHTML = '<div class="sub text-neutral" style="font-size:12px;">数据不可用</div>';
            return;
        }
        panel.innerHTML = renderRationaleDetail(d);
    };

    function renderRationaleDetail(d) {
        const fragments = Array.isArray(d.reason_fragments) ? d.reason_fragments : [];
        const rationale = d.rationale || {};
        const timingText = extractTimingText(rationale.timing_signal);
        const score = rationale.score_snapshot;
        const scoreBlock = (score && typeof score === 'object') ? Object.entries(score)
            .filter(([k, v]) => v != null && typeof v !== 'object')
            .map(([k, v]) => `<span style="display:inline-block;margin-right:12px;font-size:12px;">
                <span style="color:var(--text-secondary);">${k}:</span> <strong>${typeof v === 'number' ? v.toFixed(1) : v}</strong>
            </span>`).join('') : '';

        return `
            <div style="font-size:12px;line-height:1.8;">
                <div style="font-weight:600;color:var(--text-secondary);margin-bottom:4px;">候选理由（${fragments.length}条）</div>
                ${fragments.length > 0
                    ? fragments.map(f => `<div>· ${f}</div>`).join('')
                    : '<div style="color:var(--text-secondary);">无</div>'}

                <div style="font-weight:600;color:var(--text-secondary);margin-top:10px;margin-bottom:4px;">创建时市场快照</div>
                <div>${timingText}</div>

                ${scoreBlock ? `
                    <div style="font-weight:600;color:var(--text-secondary);margin-top:10px;margin-bottom:4px;">创建时综合分快照</div>
                    <div>${scoreBlock}</div>
                ` : ''}
            </div>`;
    }

    function extractTimingText(timingRaw) {
        if (!timingRaw) return '无';
        if (typeof timingRaw === 'string') return timingRaw;
        if (typeof timingRaw === 'object') {
            const hs = timingRaw.hs300 || timingRaw.HS300;
            const cs = timingRaw.csi500 || timingRaw.CSI500;
            const parts = [];
            if (hs && hs.signal) {
                parts.push(`HS300 <strong>${hs.signal}</strong> (PE 百分位 ${hs.percentile != null ? hs.percentile + '%' : '?'})`);
            }
            if (cs && cs.signal) {
                parts.push(`CSI500 <strong>${cs.signal}</strong> (PE 百分位 ${cs.percentile != null ? cs.percentile + '%' : '?'})`);
            }
            return parts.length > 0 ? parts.join(' · ') : JSON.stringify(timingRaw);
        }
        return String(timingRaw);
    }

    async function loadTimingBadge(code, elId) {
        try {
            const res = await fetch(`/api/timing/${code}`);
            const json = await res.json();
            const el = document.getElementById(elId);
            if (!el) return;
            const map = {
                favorable:  { bg: '#dcfce7', color: '#15803d', label: '✅ 入场条件良好' },
                neutral:    { bg: '#f1f5f9', color: '#475569', label: '⚠️ 中性' },
                unfavorable:{ bg: '#fee2e2', color: '#b91c1c', label: '❌ 不利入场' },
            };
            const style = map[json.level] || map.neutral;
            el.style.background = style.bg;
            el.style.color = style.color;
            el.textContent = style.label;
            el.title = (json.fragments || []).join(' · ');
        } catch (_) { /* 静默失败 */ }
    }

    // ── 历史决策 ────────────────────────────────────────────────────────────
    async function loadDecisionsHistory() {
        const el = document.getElementById('decisionsHistoryList');
        try {
            const res = await fetch(`/api/decisions?limit=30${kindQS()}`);
            const json = await res.json();
            const items = (json.data || []).filter(d => d.status !== 'pending');
            if (items.length === 0) {
                el.innerHTML = `
                    <div class="empty-state">
                        <div class="icon">📂</div>
                        <p>还没有历史决策</p>
                        <p class="sub">创建一笔决策并执行（或虚拟建仓）后，会在这里显示追踪记录。</p>
                    </div>`;
                return;
            }
            el.innerHTML = `
                <table style="width:100%;border-collapse:collapse;font-size:13px;">
                    <thead>
                        <tr style="color:var(--text-secondary);border-bottom:1px solid var(--border);">
                            <th style="text-align:left;padding:6px 8px;font-weight:500;">日期</th>
                            <th style="text-align:left;padding:6px 8px;font-weight:500;">基金</th>
                            <th style="text-align:left;padding:6px 8px;font-weight:500;">类型</th>
                            <th style="text-align:left;padding:6px 8px;font-weight:500;">状态</th>
                            <th style="text-align:right;padding:6px 8px;font-weight:500;">收益</th>
                            <th style="text-align:center;padding:6px 8px;font-weight:500;">操作</th>
                        </tr>
                    </thead>
                    <tbody>
                        ${items.map(d => `
                            <tr style="border-bottom:1px solid var(--border);${d.is_virtual ? 'box-shadow:inset 3px 0 0 #3b82f6;background:#f8fbff;' : ''}">
                                <td style="padding:8px;">${(d.created_at || '').slice(0, 10)}</td>
                                <td style="padding:8px;">
                                    <div style="font-weight:600;">${d.is_virtual ? '🧪 ' : ''}${d.name || d.code}</div>
                                    ${d.name ? `<div style="font-size:11px;color:var(--text-secondary);">${d.code}</div>` : ''}
                                </td>
                                <td style="padding:8px;">${decisionTypeCn(d.decision_type)}</td>
                                <td style="padding:8px;">${statusBadge(d.status)}</td>
                                <td style="padding:8px;text-align:right;${pnlStyle(d.current_pnl_pct)}">
                                    ${d.current_pnl_pct != null ? (d.current_pnl_pct * 100).toFixed(1) + '%' : '--'}
                                </td>
                                <td style="padding:8px;text-align:center;">
                                    <button onclick="decisionsShowDetail(${d.id})"
                                            style="font-size:11px;padding:2px 8px;border:1px solid var(--border);
                                                   border-radius:4px;background:transparent;cursor:pointer;">
                                        复盘
                                    </button>
                                </td>
                            </tr>
                            <tr id="hist-detail-${d.id}" style="display:none;">
                                <td colspan="6" style="padding:0;background:#f8fafc;">
                                    <div style="padding:16px;" id="hist-detail-content-${d.id}"></div>
                                </td>
                            </tr>
                        `).join('')}
                    </tbody>
                </table>`;
        } catch (e) {
            el.innerHTML = `<div class="sub" style="color:var(--danger);font-size:13px;">加载失败: ${e.message}</div>`;
        }
    }

    // ── 复盘详情（行内展开）────────────────────────────────────────────────
    window.decisionsShowDetail = async function (id) {
        const row = document.getElementById(`hist-detail-${id}`);
        const content = document.getElementById(`hist-detail-content-${id}`);
        if (!row || !content) return;
        if (row.style.display !== 'none') { row.style.display = 'none'; return; }
        row.style.display = '';
        content.innerHTML = '<div class="sub text-neutral" style="font-size:13px;">加载复盘数据中...</div>';
        try {
            const res = await fetch(`/api/decisions/${id}`);
            const json = await res.json();
            if (json.status !== 'ok') throw new Error(json.msg);
            const d = json.data;
            const perf = d.performance || {};
            const chartId = `perf-chart-${id}`;
            let rationale = {};
            try { rationale = JSON.parse(d.rationale_json || '{}'); } catch (_) {}
            const fragments = rationale.reason_fragments || [];
            const timingText = extractTimingText(rationale.timing_signal);
            const score = rationale.score_snapshot || {};

            const metaItems = [];
            if (timingText && timingText !== '无') metaItems.push(`市场快照: ${timingText}`);
            if (score && typeof score === 'object' && score.total_score != null) {
                metaItems.push(`评分快照: ${Number(score.total_score).toFixed(0)}`);
            }

            content.innerHTML = `
                <div style="display:flex;gap:16px;flex-wrap:wrap;">
                    <div style="flex:1;min-width:220px;">
                        <div style="font-size:12px;color:var(--text-secondary);font-weight:600;margin-bottom:6px;">当时的判断依据</div>
                        ${fragments.length
                            ? fragments.map(f => `<div style="font-size:12px;color:var(--text);margin-bottom:2px;">· ${f}</div>`).join('')
                            : '<div style="font-size:12px;color:var(--text-secondary);">无记录</div>'}
                        ${metaItems.map(m => `<div style="font-size:12px;color:var(--text-secondary);margin-top:2px;">· ${m}</div>`).join('')}
                        <div style="margin-top:10px;">
                            <button onclick="decisionsExportReview(${id})"
                                    style="font-size:12px;padding:4px 12px;border:1px solid var(--border);
                                           border-radius:4px;background:transparent;cursor:pointer;">
                                📋 导出复盘提示词
                            </button>
                        </div>
                    </div>
                    <div style="flex:2;min-width:280px;">
                        ${perf.nav_curve && perf.nav_curve.length > 1
                            ? `<div id="${chartId}" style="height:160px;"></div>`
                            : '<div style="font-size:12px;color:var(--text-secondary);line-height:2;">暂无净值曲线（尚未执行或数据不足）</div>'}
                        ${perf.current_pnl_pct != null
                            ? `<div style="font-size:13px;margin-top:6px;">
                                   当前收益 <strong style="${pnlStyle(perf.current_pnl_pct)}">${(perf.current_pnl_pct * 100).toFixed(2)}%</strong>
                                   ${perf.hs300_pnl_pct != null
                                        ? ` · 同期HS300 <strong>${(perf.hs300_pnl_pct * 100).toFixed(2)}%</strong>`
                                        : ''}
                               </div>`
                            : ''}
                    </div>
                </div>`;

            if (perf.nav_curve && perf.nav_curve.length > 1) {
                setTimeout(() => {
                    const chartEl = document.getElementById(chartId);
                    if (!chartEl || typeof echarts === 'undefined') return;
                    const chart = echarts.init(chartEl);
                    const dates = perf.nav_curve.map(p => p.date);
                    const fundLine = perf.nav_curve.map(p => (p.pnl_pct * 100).toFixed(2));
                    const hs300Line = (perf.hs300_curve || []).map(p => (p.pnl_pct * 100).toFixed(2));
                    const series = [{
                        name: d.code, type: 'line', data: fundLine,
                        smooth: true, showSymbol: false,
                        lineStyle: { color: 'var(--primary)', width: 2 },
                    }];
                    if (hs300Line.length === dates.length && hs300Line.length > 0) {
                        series.push({
                            name: 'HS300', type: 'line', data: hs300Line,
                            smooth: true, showSymbol: false,
                            lineStyle: { color: '#94a3b8', width: 1.5, type: 'dashed' },
                        });
                    }
                    chart.setOption({
                        tooltip: {
                            trigger: 'axis',
                            formatter: p => p.map(s => `${s.seriesName}: ${s.value}%`).join('<br>'),
                        },
                        legend: { data: series.map(s => s.name), right: 0, top: 0, textStyle: { fontSize: 11 } },
                        grid: { left: 40, right: 10, top: 30, bottom: 20 },
                        xAxis: { type: 'category', data: dates, axisLabel: { fontSize: 10 } },
                        yAxis: { type: 'value', axisLabel: { formatter: v => v + '%', fontSize: 10 } },
                        series,
                    });
                }, 50);
            }
        } catch (e) {
            content.innerHTML = `<div style="color:var(--danger);font-size:13px;">加载失败: ${e.message}</div>`;
        }
    };

    // ── 导出复盘提示词 ───────────────────────────────────────────────────────
    window.decisionsExportReview = async function (id) {
        try {
            const res = await fetch(`/api/decisions/${id}/export`);
            if (!res.ok) throw new Error('导出失败');
            const text = await res.text();
            await navigator.clipboard.writeText(text);
            decisionsToast('复盘提示词已复制到剪贴板，粘贴到 Claude 开始对话 🎉', 4000);
        } catch (e) {
            decisionsToast(`导出失败: ${e.message}`, 3000);
        }
    };

    // ── 告警标记已处理 ────────────────────────────────────────────────────
    window.decisionsMarkHandled = async function (id) {
        try {
            await fetch(`/api/decisions/${id}`, { method: 'DELETE' });
            await loadDecisionsAlerts();
        } catch (e) {
            decisionsToast('操作失败: ' + e.message, 3000);
        }
    };

    // ── 取消待执行决策 ───────────────────────────────────────────────────────
    window.decisionsCancel = async function (id) {
        if (!confirm('确认取消这条决策？')) return;
        try {
            await fetch(`/api/decisions/${id}`, { method: 'DELETE' });
            await loadDecisionsPending();
        } catch (e) {
            decisionsToast('取消失败: ' + e.message, 3000);
        }
    };

    // ── 链接交易（标记已执行）────────────────────────────────────────────
    window.decisionsOpenLinkTx = function (decisionId) {
        const txId = prompt(
            '请输入交易ID（先在"总览"Tab → 记录交易，提交后在浏览器控制台或 /api/portfolio 查交易ID）：'
        );
        if (!txId || isNaN(parseInt(txId))) {
            decisionsToast('请先录入交易记录再链接');
            return;
        }
        _decisionsLinkTx(decisionId, parseInt(txId));
    };

    async function _decisionsLinkTx(decisionId, transactionId) {
        try {
            const res = await fetch(`/api/decisions/${decisionId}/link-tx`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ transaction_id: transactionId }),
            });
            const json = await res.json();
            if (!res.ok || json.status !== 'ok') throw new Error(json.detail || json.msg || '链接失败');
            decisionsToast('决策已标记为已执行 ✓');
            await decisionsInit();
        } catch (e) {
            decisionsToast('链接失败: ' + e.message, 3000);
        }
    }

    // ── 手动创建决策（Modal）────────────────────────────────────────────────
    window.decisionsCreateManual = function () {
        openDecisionModal('', '', null);
    };

    // 当前弹窗对应的仓位建议数据（供 applySuggest / toggleDetail 读取）
    let _latestPositionSizing = null;

    window.openDecisionModal = function (code, name, sessionId) {
        ensureDecisionModalBindings();
        document.getElementById('decisionModalCode').value = code || '';
        document.getElementById('decisionModalName').textContent = name ? `(${name})` : '';
        document.getElementById('decisionModalSessionId').value = sessionId || '';
        document.getElementById('decisionModalAmt').value = '';
        document.getElementById('decisionModalTP').value = '';
        document.getElementById('decisionModalSL').value = '';
        document.getElementById('decisionCreateModal').style.display = 'flex';

        // 重置 is_virtual checkbox
        document.getElementById('decisionModalIsVirtual').checked = false;
        syncVirtualDecisionUI();

        // 重置仓位建议区
        _latestPositionSizing = null;
        document.getElementById('positionSuggestRow').style.display = 'none';
        document.getElementById('positionSuggestWarnings').style.display = 'none';
        document.getElementById('positionSuggestWarnings').innerHTML = '';
        document.getElementById('positionSuggestDetail').style.display = 'none';
        document.getElementById('positionSuggestDetail').innerHTML = '';
        document.getElementById('positionSuggestToggleText').textContent = '▾ 为什么';

        // 重置 peer 对比区
        resetPeerSection(code || '');

        if (code) loadPositionSizing(code);
    };

    function ensureDecisionModalBindings() {
        if (_decisionModalBindingsReady) return;
        const virtualCheckbox = document.getElementById('decisionModalIsVirtual');
        const codeInput = document.getElementById('decisionModalCode');
        if (!virtualCheckbox || !codeInput) return;
        virtualCheckbox.addEventListener('change', syncVirtualDecisionUI);
        codeInput.addEventListener('input', handleDecisionCodeChange);
        _decisionModalBindingsReady = true;
    }

    function syncVirtualDecisionUI() {
        const cb = document.getElementById('decisionModalIsVirtual');
        const hint = document.getElementById('decisionVirtualHint');
        if (!cb || !hint) return;
        hint.style.display = cb.checked ? 'block' : 'none';
        if (cb.checked) {
            document.getElementById('decisionModalTP').value = '';
            document.getElementById('decisionModalSL').value = '';
        }
    }

    function handleDecisionCodeChange() {
        const code = getDecisionModalCode();
        const peerRow = document.getElementById('peerCompareRow');
        const peerContent = document.getElementById('peerContent');
        if (!peerRow) return;
        peerRow.style.display = code ? 'block' : 'none';
        if (!code) {
            resetPeerSection('');
            return;
        }
        if (peerContent && peerContent.dataset.code !== code) {
            resetPeerSection(code);
        }
    }

    function getDecisionModalCode() {
        const input = document.getElementById('decisionModalCode');
        if (!input) return '';
        const raw = input.value.trim();
        // 自动修正多余的前导 0（大陆公募基金代码均为 6 位）
        if (/^\d{7,}$/.test(raw)) {
            const fixed = raw.replace(/^0+/, '').padStart(6, '0');
            input.value = fixed;
            return fixed;
        }
        return raw;
    }

    function resetPeerSection(code) {
        const peerRow = document.getElementById('peerCompareRow');
        const peerSection = document.getElementById('peerSection');
        const peerToggleIcon = document.getElementById('peerToggleIcon');
        const peerLoading = document.getElementById('peerLoading');
        const peerContent = document.getElementById('peerContent');
        if (peerRow) peerRow.style.display = code ? 'block' : 'none';
        if (peerSection) peerSection.style.display = 'none';
        if (peerToggleIcon) peerToggleIcon.textContent = '▾';
        if (peerLoading) {
            peerLoading.textContent = '加载中...';
            peerLoading.style.display = 'block';
        }
        if (peerContent) {
            peerContent.innerHTML = '';
            peerContent.style.display = 'none';
            peerContent.dataset.code = code || '';
        }
    }

    window.togglePeerSection = async function () {
        const code = getDecisionModalCode();
        if (!code) {
            decisionsToast('请先填写基金代码');
            return;
        }
        const section = document.getElementById('peerSection');
        const icon = document.getElementById('peerToggleIcon');
        if (!section || !icon) return;
        const isOpen = section.style.display !== 'none';
        section.style.display = isOpen ? 'none' : 'block';
        icon.textContent = isOpen ? '▾' : '▴';
        if (!isOpen) await loadPeerFunds(code);
    };

    async function loadPeerFunds(code) {
        const loadingEl = document.getElementById('peerLoading');
        const contentEl = document.getElementById('peerContent');
        if (!loadingEl || !contentEl) return;

        if (_peerFundCache[code]) {
            renderPeerFunds(_peerFundCache[code], code);
            return;
        }

        loadingEl.style.display = 'block';
        loadingEl.textContent = '加载中...';
        contentEl.style.display = 'none';
        try {
            const res = await fetch(`/api/funds/${code}/peers?limit=8`);
            const json = await res.json();
            if (!res.ok || json.status !== 'ok') throw new Error(json.detail || json.msg || '加载失败');
            _peerFundCache[code] = json.data;
            renderPeerFunds(json.data, code);
        } catch (e) {
            loadingEl.style.display = 'block';
            loadingEl.textContent = `加载失败: ${e.message}`;
            contentEl.style.display = 'none';
        }
    }

    function renderPeerFunds(data, code) {
        const loadingEl = document.getElementById('peerLoading');
        const contentEl = document.getElementById('peerContent');
        if (!loadingEl || !contentEl) return;

        const rows = Array.isArray(data) ? data : (data?.items || data?.peers || data?.funds || []);
        const note = Array.isArray(data) ? '' : (data?.note || '');
        const fmtPct = value => (value == null || value === '') ? '--' : `${Number(value).toFixed(2)}%`;

        contentEl.innerHTML = `
            ${note ? `<div style="margin-bottom:10px;padding:8px 10px;background:#fef3c7;border-left:3px solid #d97706;border-radius:4px;font-size:12px;color:#92400e;">${note}</div>` : ''}
            ${rows.length === 0
                ? `<div style="font-size:12px;color:var(--text-secondary);">未找到 ${code} 的同主题基金。</div>`
                : `<div style="overflow:auto;">
                        <table style="width:100%;border-collapse:collapse;font-size:12px;">
                            <thead>
                                <tr style="color:var(--text-secondary);border-bottom:1px solid var(--border);">
                                    <th style="text-align:left;padding:6px 8px;">代码</th>
                                    <th style="text-align:left;padding:6px 8px;">名称</th>
                                    <th style="text-align:right;padding:6px 8px;">pct_1y</th>
                                    <th style="text-align:right;padding:6px 8px;">近1月 ret_1m</th>
                                    <th style="text-align:right;padding:6px 8px;">近3月 ret_3m</th>
                                </tr>
                            </thead>
                            <tbody>
                                ${rows.map(item => `
                                    <tr style="border-bottom:1px solid var(--border);">
                                        <td style="padding:7px 8px;font-family:monospace;">${item.code || '--'}</td>
                                        <td style="padding:7px 8px;">${item.name || '--'}</td>
                                        <td style="padding:7px 8px;text-align:right;">${fmtPct(item.pct_1y)}</td>
                                        <td style="padding:7px 8px;text-align:right;">${fmtPct(item.ret_1m)}</td>
                                        <td style="padding:7px 8px;text-align:right;">${fmtPct(item.ret_3m)}</td>
                                    </tr>
                                `).join('')}
                            </tbody>
                        </table>
                   </div>`}
        `;
        contentEl.dataset.code = code;
        loadingEl.style.display = 'none';
        contentEl.style.display = 'block';
    }

    window.openAiThemePrompt = async function () {
        const code = getDecisionModalCode();
        if (!code) {
            decisionsToast('请先填写基金代码');
            return;
        }
        const modal = document.getElementById('aiPromptModal');
        const metaEl = document.getElementById('aiPromptMeta');
        const textEl = document.getElementById('aiPromptText');
        if (!modal || !metaEl || !textEl) return;

        modal.style.display = 'flex';
        metaEl.textContent = '加载中...';
        textEl.value = '';

        try {
            const res = await fetch(`/api/funds/${code}/ai-prompts/theme-analysis`);
            const json = await res.json();
            if (!res.ok || json.status !== 'ok') throw new Error(json.detail || json.msg || '加载失败');
            const data = json.data || {};
            const prompt = data.prompt || data.rendered_prompt || '';
            const metaBits = [];
            if (data.version) metaBits.push(`version ${data.version}`);
            if (data.last_verified_date) metaBits.push(`last_verified_date ${data.last_verified_date}`);
            if (data.char_count != null) metaBits.push(`char_count ${data.char_count}`);
            if (data.stale) metaBits.push('⚠️ stale');
            metaEl.textContent = metaBits.join(' · ') || '无元信息';
            textEl.value = prompt;
        } catch (e) {
            metaEl.textContent = `加载失败: ${e.message}`;
            textEl.value = '';
        }
    };

    window.closeAiPromptModal = function () {
        const modal = document.getElementById('aiPromptModal');
        if (modal) modal.style.display = 'none';
    };

    window.copyAiPrompt = async function () {
        const text = document.getElementById('aiPromptText')?.value || '';
        if (!text) {
            decisionsToast('没有可复制的 prompt');
            return;
        }
        try {
            await navigator.clipboard.writeText(text);
            decisionsToast('Prompt 已复制到剪贴板');
        } catch (e) {
            decisionsToast(`复制失败: ${e.message}`, 3000);
        }
    };

    async function loadPositionSizing(code) {
        try {
            const res = await fetch(`/api/position-sizing/${code}`);
            const json = await res.json();
            if (json.status !== 'ok') throw new Error(json.msg || '建议加载失败');
            const data = json.data;
            _latestPositionSizing = data;
            renderPositionSizingUI(data);
        } catch (e) {
            // 静默失败，不阻塞用户手填
            console.warn('[position-sizing]', e);
        }
    }

    function renderPositionSizingUI(data) {
        const row = document.getElementById('positionSuggestRow');
        const btn = document.getElementById('positionSuggestBtn');
        const warnEl = document.getElementById('positionSuggestWarnings');
        const detailEl = document.getElementById('positionSuggestDetail');

        row.style.display = 'block';

        // 按钮状态
        if (data.suggested_amount === 0) {
            btn.innerHTML = '⚠️ 类别已超配';
            btn.style.background = '#fee2e2';
            btn.style.color = '#b91c1c';
            btn.style.borderColor = '#fca5a5';
            btn.disabled = true;
            btn.style.cursor = 'not-allowed';
        } else {
            btn.innerHTML = `🪙 建议 ¥${data.suggested_amount.toLocaleString()}`;
            btn.style.background = '#dbeafe';
            btn.style.color = '#1d4ed8';
            btn.style.borderColor = '#93c5fd';
            btn.disabled = false;
            btn.style.cursor = 'pointer';
            btn.title = `范围 ¥${data.range_min.toLocaleString()} ~ ¥${data.range_max.toLocaleString()}`;
        }

        // warnings 顶部黄色 banner
        if (Array.isArray(data.warnings) && data.warnings.length > 0) {
            const isCritical = data.suggested_amount === 0;
            warnEl.style.display = 'block';
            warnEl.innerHTML = data.warnings.map(w => `
                <div style="background:${isCritical ? '#fee2e2' : '#fef9c3'};
                            border-left:3px solid ${isCritical ? '#dc2626' : '#ca8a04'};
                            padding:6px 10px;border-radius:4px;font-size:12px;
                            color:${isCritical ? '#7f1d1d' : '#713f12'};margin-bottom:4px;">
                    ⚠️ ${w}
                </div>
            `).join('');
        } else {
            warnEl.style.display = 'none';
        }

        // detail 默认收起
        detailEl.innerHTML = renderPositionSizingDetail(data);
    }

    function renderPositionSizingDetail(data) {
        const steps = Array.isArray(data.steps) ? data.steps : [];
        const stepsHtml = steps.map((s, idx) => {
            const isLast = idx === steps.length - 1;
            const valStr = `¥${Math.round(s.value).toLocaleString()}`;
            return `
                <div style="display:flex;justify-content:space-between;gap:12px;
                            padding:4px 0;${isLast ? 'border-top:1px dashed var(--border);margin-top:4px;padding-top:8px;' : ''}">
                    <div style="flex:1;min-width:0;">
                        <div style="font-weight:${isLast ? '700' : '600'};color:var(--text);">${s.label}</div>
                        ${s.detail ? `<div style="font-size:11px;color:var(--text-secondary);margin-top:1px;">${s.detail}</div>` : ''}
                    </div>
                    <div style="font-weight:${isLast ? '700' : '500'};color:${isLast ? 'var(--primary)' : 'var(--text)'};white-space:nowrap;">
                        ${valStr}
                    </div>
                </div>`;
        }).join('');

        const inputs = data.inputs || {};
        const inputLines = [];
        if (inputs.total_value != null) inputLines.push(`总资产 ¥${Math.round(inputs.total_value).toLocaleString()}`);
        if (inputs.current_cat_pct != null && inputs.target_cat_pct != null) {
            inputLines.push(`${data.category} ${(inputs.current_cat_pct * 100).toFixed(1)}% / 目标 ${(inputs.target_cat_pct * 100).toFixed(1)}%`);
        }
        if (inputs.timing_level) inputLines.push(`timing=${inputs.timing_level}`);
        if (inputs.overlap_rate != null) inputLines.push(`overlap=${(inputs.overlap_rate * 100).toFixed(0)}%`);

        return `
            ${stepsHtml}
            ${inputLines.length > 0 ? `
                <div style="margin-top:8px;padding-top:6px;border-top:1px solid var(--border);
                            font-size:11px;color:var(--text-secondary);">
                    ${inputLines.join(' · ')}
                </div>
            ` : ''}
        `;
    }

    window.decisionsApplySuggest = function () {
        if (!_latestPositionSizing || _latestPositionSizing.suggested_amount <= 0) return;
        document.getElementById('decisionModalAmt').value = _latestPositionSizing.suggested_amount;
    };

    window.decisionsToggleSuggestDetail = function () {
        const el = document.getElementById('positionSuggestDetail');
        const txt = document.getElementById('positionSuggestToggleText');
        if (el.style.display === 'none') {
            el.style.display = 'block';
            txt.textContent = '▴ 收起';
        } else {
            el.style.display = 'none';
            txt.textContent = '▾ 为什么';
        }
    };

    window.decisionsModalClose = function () {
        document.getElementById('decisionCreateModal').style.display = 'none';
        resetPeerSection('');
    };

    window.decisionsModalSubmit = async function () {
        const rawCode = document.getElementById('decisionModalCode').value.trim();
        // 大陆公募基金代码均为 6 位；如果用户多打了前导 0 自动修正
        const code = /^\d{7,}$/.test(rawCode) ? rawCode.replace(/^0+/, '').padStart(6, '0') : rawCode;
        if (rawCode !== code) {
            document.getElementById('decisionModalCode').value = code;
        }
        const sessionId = document.getElementById('decisionModalSessionId').value.trim();
        const tpVal = parseFloat(document.getElementById('decisionModalTP').value);
        const slVal = parseFloat(document.getElementById('decisionModalSL').value);
        const amtVal = parseFloat(document.getElementById('decisionModalAmt').value);
        if (!code) { decisionsToast('请填写基金代码'); return; }
        const isVirtual = document.getElementById('decisionModalIsVirtual').checked;
        const payload = {
            code,
            decision_type: 'buy',
            is_virtual: isVirtual,
            ...(amtVal > 0 && { target_amount: amtVal }),
            ...(tpVal > 0 && { target_tp_pct: tpVal / 100 }),
            ...(slVal > 0 && { target_sl_pct: -slVal / 100 }),
            ...(sessionId && { source_session_id: sessionId }),
        };
        try {
            const res = await fetch('/api/decisions', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload),
            });
            const json = await res.json();
            if (!res.ok || json.status !== 'ok') {
                throw new Error(json.detail || json.msg || '创建失败');
            }
            decisionsModalClose();
            const flag = json.data.is_virtual ? '🧪 虚拟' : '';
            decisionsToast(`${flag}决策已创建 (ID: ${json.data.id}) ✓`);
            await decisionsInit();
        } catch (e) {
            decisionsToast(`创建失败: ${e.message}`, 3000);
        }
    };

    // ── helpers ──────────────────────────────────────────────────────────────
    function decisionTypeCn(t) {
        return ({ buy: '买入', sell: '卖出', rebalance: '调仓', hold: '持仓观察' })[t] || t;
    }

    function statusBadge(s) {
        const map = {
            pending:      ['#dbeafe', '#1d4ed8', '待执行'],
            executed:     ['#dcfce7', '#15803d', '已执行'],
            cancelled:    ['#f1f5f9', '#94a3b8', '已取消'],
            triggered_tp: ['#dcfce7', '#15803d', '🎯止盈触发'],
            triggered_sl: ['#fee2e2', '#b91c1c', '⚠️止损触发'],
        };
        const [bg, color, label] = map[s] || ['#f1f5f9', '#94a3b8', s];
        return `<span style="font-size:11px;padding:2px 7px;border-radius:4px;background:${bg};color:${color};">${label}</span>`;
    }

    function pnlStyle(pct) {
        if (pct == null) return '';
        if (pct > 0) return 'color:var(--success);font-weight:600;';
        if (pct < 0) return 'color:var(--danger);font-weight:600;';
        return '';
    }

    function decisionsToast(msg, duration = 2500) {
        const el = document.getElementById('decisionsToast');
        if (!el) return;
        el.textContent = msg;
        el.style.display = 'block';
        setTimeout(() => { el.style.display = 'none'; }, duration);
    }

    // ── tab switch hook ──────────────────────────────────────────────────────
    const _origSwitchTabD = window.switchTab;
    window.switchTab = function (tabName) {
        _origSwitchTabD && _origSwitchTabD(tabName);
        if (tabName === 'decisions') decisionsInit();
    };

})();
