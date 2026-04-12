/* 模拟盘 Tab — simulations.js */

(function () {
    'use strict';

    let _simStrategies = [];
    let _simCurrentId = null;
    let _simChart = null;

    window.simulationsInit = async function () {
        applySimulationDefaults();
        await Promise.all([
            loadSimulationStrategies(),
            window.simulationsLoadList(),
        ]);
        if (_simCurrentId) {
            await window.simOpenDetail(_simCurrentId);
        }
    };

    function applySimulationDefaults() {
        const startEl = document.getElementById('simStart');
        const endEl = document.getElementById('simEnd');
        if (!startEl || !endEl || startEl.value || endEl.value) return;

        const today = new Date();
        const past = new Date(today);
        past.setMonth(past.getMonth() - 12);

        startEl.value = toDateInputValue(past);
        endEl.value = toDateInputValue(today);
    }

    function toDateInputValue(date) {
        const year = date.getFullYear();
        const month = String(date.getMonth() + 1).padStart(2, '0');
        const day = String(date.getDate()).padStart(2, '0');
        return `${year}-${month}-${day}`;
    }

    async function requestJSON(url, options) {
        const res = await fetch(url, options);
        const json = await res.json();
        if (!res.ok || json.status !== 'ok') {
            throw new Error(json.detail || json.msg || '请求失败');
        }
        return json.data;
    }

    function parseJsonField(raw, fallback) {
        if (Array.isArray(raw) || (raw && typeof raw === 'object')) return raw;
        if (!raw) return fallback;
        try {
            return JSON.parse(raw);
        } catch (_) {
            return fallback;
        }
    }

    function simulationToast(message) {
        const toast = document.getElementById('decisionsToast');
        if (!toast) {
            window.alert(message);
            return;
        }
        toast.textContent = message;
        toast.style.display = 'block';
        window.setTimeout(() => {
            toast.style.display = 'none';
        }, 2500);
    }

    async function loadSimulationStrategies() {
        const select = document.getElementById('simStrategy');
        const desc = document.getElementById('simStrategyDesc');
        if (!select || !desc) return;

        select.innerHTML = '<option value="">加载中...</option>';
        desc.textContent = '读取策略说明中...';

        try {
            _simStrategies = await requestJSON('/api/simulations/strategies');
            if (_simStrategies.length === 0) {
                select.innerHTML = '<option value="">暂无可用策略</option>';
                desc.textContent = '后端当前没有返回策略。';
                return;
            }
            // 策略名称对用户友好化
            const strategyLabels = {
                'dca_monthly': '每月定投（dca_monthly）',
            };
            select.innerHTML = _simStrategies.map(item => `
                <option value="${item.name}">${strategyLabels[item.name] || item.name}</option>
            `).join('');
            window.simulationsOnStrategyChange();
        } catch (e) {
            select.innerHTML = '<option value="">加载失败</option>';
            desc.textContent = `策略加载失败: ${e.message}`;
        }
    }

    window.simulationsOnStrategyChange = function () {
        const select = document.getElementById('simStrategy');
        const desc = document.getElementById('simStrategyDesc');
        if (!select || !desc) return;
        const current = _simStrategies.find(item => item.name === select.value);
        desc.innerHTML = (current?.description || '该策略暂无描述。') +
            '<div style="margin-top:8px;font-size:11px;color:#9ca3af;">📌 目前只开放了"每月定投"一个策略。更多策略（估值加权定投、目标仓位补仓、质量排名再平衡）将在后续版本开放。</div>';
    };

    window.simulationsLoadList = async function () {
        const listEl = document.getElementById('simList');
        if (!listEl) return;
        listEl.innerHTML = '<div class="sub text-neutral" style="font-size:13px;">加载中...</div>';

        try {
            const items = await requestJSON('/api/simulations');
            if (!items.length) {
                listEl.innerHTML = `
                    <div class="empty-state">
                        <div class="icon">🧪</div>
                        <p>还没有模拟</p>
                        <p class="sub">试试这个起步示例：</p>
                        <ol class="empty-guide-steps">
                            <li>策略选 <strong>dca_monthly</strong>（每月定投）</li>
                            <li>基金填 <strong>009049</strong>（或你感兴趣的代码）</li>
                            <li>时间 <strong>2024-01-01</strong> 到 <strong>2024-12-31</strong></li>
                            <li>点"创建并运行"，几秒看到全年12笔交易结果</li>
                        </ol>
                    </div>`;
                return;
            }

            listEl.innerHTML = items.map(item => {
                const fundPool = parseJsonField(item.fund_pool_json, []);
                const params = parseJsonField(item.params_json, {});
                const amountPerFund = params.amount_per_fund;
                return `
                    <div style="padding:14px 16px;border:1px solid var(--border);border-radius:12px;margin-bottom:10px;background:#fff;">
                        <div style="display:flex;align-items:flex-start;justify-content:space-between;gap:10px;">
                            <div style="min-width:0;">
                                <div style="font-size:15px;font-weight:700;line-height:1.4;">${item.name}</div>
                                <div style="margin-top:4px;font-size:12px;color:var(--text-secondary);line-height:1.6;">
                                    ${item.strategy_name} · ${item.mode} · status=${item.status}
                                    ${item.current_date ? ` · current_date=${item.current_date}` : ''}
                                </div>
                                <div style="margin-top:6px;font-size:12px;color:var(--text-secondary);line-height:1.6;">
                                    基金池 ${fundPool.length} 只
                                    ${amountPerFund != null ? ` · amount_per_fund=${formatMoneySafe(amountPerFund)}` : ''}
                                    · 初始资金 ${formatMoneySafe(item.initial_capital)}
                                </div>
                            </div>
                            <button class="btn" onclick="simOpenDetail(${item.id})" style="padding:6px 14px;background:#eff6ff;border:1px solid #bfdbfe;color:#1d4ed8;">查看</button>
                        </div>
                    </div>`;
            }).join('');
        } catch (e) {
            listEl.innerHTML = `<div class="sub" style="font-size:13px;color:var(--danger);">加载失败: ${e.message}</div>`;
        }
    };

    window.simCreate = async function () {
        const btn = document.getElementById('simCreateBtn');
        const name = document.getElementById('simName')?.value.trim();
        const strategyName = document.getElementById('simStrategy')?.value;
        const fundPoolText = document.getElementById('simFundPool')?.value || '';
        const initialCapital = parseFloat(document.getElementById('simCapital')?.value || '');
        const amountPerFund = parseFloat(document.getElementById('simAmountPerFund')?.value || '');
        const startDate = document.getElementById('simStart')?.value;
        const endDate = document.getElementById('simEnd')?.value;
        const mode = document.getElementById('simMode')?.value || 'backtest';
        const fundPool = Array.from(new Set(fundPoolText.split(/[\s,，]+/).map(item => item.trim()).filter(Boolean)));

        if (!name) return simulationToast('请填写模拟名称');
        if (!strategyName) return simulationToast('请选择策略');
        if (!fundPool.length) return simulationToast('请至少填写一个基金代码');
        if (!(initialCapital > 0)) return simulationToast('请填写有效的初始资金');
        if (!(amountPerFund > 0)) return simulationToast('请填写有效的每只基金投入金额');
        if (!startDate) return simulationToast('请填写开始日期');
        if (mode === 'backtest' && !endDate) return simulationToast('backtest 模式必须填写结束日期');

        const payload = {
            name,
            strategy_name: strategyName,
            params: { amount_per_fund: amountPerFund },
            fund_pool: fundPool,
            initial_capital: initialCapital,
            start_date: startDate,
            end_date: endDate || null,
            mode,
        };

        if (btn) {
            btn.disabled = true;
            btn.textContent = '创建并运行中...';
        }

        try {
            const created = await requestJSON('/api/simulations', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload),
            });
            await requestJSON(`/api/simulations/${created.id}/run`, { method: 'POST' });
            await window.simulationsLoadList();
            await window.simOpenDetail(created.id);
            simulationToast(`模拟已创建并启动 (ID: ${created.id})`);
        } catch (e) {
            simulationToast(`创建失败: ${e.message}`);
        } finally {
            if (btn) {
                btn.disabled = false;
                btn.textContent = '创建并运行';
            }
        }
    };

    window.simOpenDetail = async function (id) {
        const detailCard = document.getElementById('simDetailCard');
        const titleEl = document.getElementById('simDetailTitle');
        const subEl = document.getElementById('simDetailSub');
        const warningsEl = document.getElementById('simWarnings');
        const statsEl = document.getElementById('simStats');
        const tradesBody = document.getElementById('simTradesBody');
        const advanceBtn = document.getElementById('simAdvanceBtn');
        if (!detailCard || !titleEl || !subEl || !warningsEl || !statsEl || !tradesBody || !advanceBtn) return;

        _simCurrentId = id;
        detailCard.style.display = 'block';
        titleEl.textContent = '模拟详情';
        subEl.textContent = '加载中...';
        warningsEl.innerHTML = '';
        statsEl.innerHTML = '';
        tradesBody.innerHTML = '<tr><td colspan="6" style="padding:16px;text-align:center;color:var(--text-secondary);">加载中...</td></tr>';
        renderSimChart([]);

        try {
            const detail = await requestJSON(`/api/simulations/${id}`);
            const fundPool = parseJsonField(detail.fund_pool_json, []);
            const stats = detail.stats || {};
            const warnings = Array.isArray(detail.warnings) ? detail.warnings : [];
            const trades = Array.isArray(detail.trades) ? detail.trades : [];
            const snapshots = Array.isArray(detail.snapshots) ? detail.snapshots : [];

            titleEl.textContent = detail.name || `模拟 #${id}`;
            subEl.textContent = [
                detail.strategy_name,
                detail.mode,
                `status=${detail.status}`,
                detail.current_date ? `current_date=${detail.current_date}` : '',
                fundPool.length ? `fund_pool=${fundPool.join(', ')}` : '',
            ].filter(Boolean).join(' · ');

            warningsEl.innerHTML = warnings.length
                ? warnings.map(item => `
                    <div style="margin-bottom:8px;padding:10px 12px;background:#fef3c7;border-left:3px solid #d97706;border-radius:6px;font-size:12px;color:#92400e;">
                        ${item}
                    </div>`).join('')
                : '';

            statsEl.innerHTML = `
                ${renderMetric('最终市值', formatMoneySafe(stats.final_value))}
                ${renderMetric('总收益率', formatPercentSafe(stats.total_return_pct))}
                ${renderMetric('最大回撤', formatPercentSafe(stats.max_drawdown))}
                ${renderMetric('交易次数', stats.trade_count ?? 0)}
            `;

            tradesBody.innerHTML = trades.length
                ? trades.map(item => `
                    <tr style="border-bottom:1px solid var(--border);">
                        <td style="padding:8px 10px;">${item.trade_date || '--'}</td>
                        <td style="padding:8px 10px;">${item.code || '--'}</td>
                        <td style="padding:8px 10px;">${item.action || '--'}</td>
                        <td style="padding:8px 10px;text-align:right;">${formatMoneySafe(item.amount)}</td>
                        <td style="padding:8px 10px;text-align:right;">${numberSafe(item.shares, 4)}</td>
                        <td style="padding:8px 10px;text-align:right;">${numberSafe(item.price, 4)}</td>
                    </tr>
                `).join('')
                : '<tr><td colspan="6" style="padding:16px;text-align:center;color:var(--text-secondary);">暂无交易</td></tr>';

            advanceBtn.style.display = detail.mode === 'forward' ? 'inline-flex' : 'none';
            renderSimChart(snapshots);
        } catch (e) {
            subEl.textContent = `加载失败: ${e.message}`;
            tradesBody.innerHTML = '<tr><td colspan="6" style="padding:16px;text-align:center;color:var(--danger);">详情加载失败</td></tr>';
        }
    };

    function renderMetric(label, value) {
        return `
            <div class="metric-item">
                <div class="metric-value">${value}</div>
                <div class="metric-label">${label}</div>
            </div>
        `;
    }

    function renderSimChart(snapshots) {
        const chartEl = document.getElementById('simPnlChart');
        if (!chartEl || typeof echarts === 'undefined') return;

        if (_simChart) {
            _simChart.dispose();
            _simChart = null;
        }

        _simChart = echarts.init(chartEl);

        if (!snapshots.length) {
            _simChart.setOption({
                title: {
                    text: '暂无 PnL 曲线',
                    left: 'center',
                    top: 'middle',
                    textStyle: { fontSize: 14, fontWeight: 500, color: '#94a3b8' },
                },
                xAxis: { show: false, type: 'category', data: [] },
                yAxis: { show: false, type: 'value' },
                series: [],
            });
            return;
        }

        _simChart.setOption({
            tooltip: {
                trigger: 'axis',
                formatter(params) {
                    const point = params[0];
                    return `${point.axisValue}<br/>总市值: ${formatMoneySafe(point.value)}`;
                },
            },
            grid: { left: 48, right: 18, top: 28, bottom: 36 },
            xAxis: {
                type: 'category',
                data: snapshots.map(item => item.date),
                axisLabel: { fontSize: 11, color: '#64748b' },
                axisLine: { lineStyle: { color: '#cbd5e1' } },
            },
            yAxis: {
                type: 'value',
                axisLabel: {
                    color: '#64748b',
                    formatter(value) {
                        return Number(value).toLocaleString('zh-CN');
                    },
                },
                splitLine: { lineStyle: { color: '#e2e8f0' } },
            },
            series: [{
                name: '总市值',
                type: 'line',
                data: snapshots.map(item => item.total_value),
                smooth: true,
                showSymbol: false,
                lineStyle: { color: '#2563eb', width: 2.5 },
                areaStyle: {
                    color: {
                        type: 'linear',
                        x: 0,
                        y: 0,
                        x2: 0,
                        y2: 1,
                        colorStops: [
                            { offset: 0, color: 'rgba(59,130,246,0.32)' },
                            { offset: 1, color: 'rgba(59,130,246,0.04)' },
                        ],
                    },
                },
            }],
        });
    }

    window.simAdvanceCurrent = async function () {
        if (!_simCurrentId) return simulationToast('请先打开一个模拟详情');
        try {
            await requestJSON(`/api/simulations/${_simCurrentId}/advance`, { method: 'POST' });
            await window.simulationsLoadList();
            await window.simOpenDetail(_simCurrentId);
            simulationToast('已推进 forward 模拟');
        } catch (e) {
            simulationToast(`推进失败: ${e.message}`);
        }
    };

    window.simRerunCurrent = async function () {
        if (!_simCurrentId) return simulationToast('请先打开一个模拟详情');
        try {
            await requestJSON(`/api/simulations/${_simCurrentId}/run`, { method: 'POST' });
            await window.simulationsLoadList();
            await window.simOpenDetail(_simCurrentId);
            simulationToast('模拟已重跑');
        } catch (e) {
            simulationToast(`重跑失败: ${e.message}`);
        }
    };

    window.simDeleteCurrent = async function () {
        if (!_simCurrentId) return simulationToast('请先打开一个模拟详情');
        if (!window.confirm('确认删除这个模拟？')) return;

        try {
            await requestJSON(`/api/simulations/${_simCurrentId}`, { method: 'DELETE' });
            _simCurrentId = null;
            document.getElementById('simDetailCard').style.display = 'none';
            await window.simulationsLoadList();
            simulationToast('模拟已删除');
        } catch (e) {
            simulationToast(`删除失败: ${e.message}`);
        }
    };

    function formatMoneySafe(value) {
        if (value == null || Number.isNaN(Number(value))) return '--';
        if (typeof window.formatMoney === 'function') return `¥${window.formatMoney(Number(value))}`;
        return `¥${Number(value).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
    }

    function formatPercentSafe(value) {
        if (value == null || Number.isNaN(Number(value))) return '--';
        return `${Number(value).toFixed(2)}%`;
    }

    function numberSafe(value, digits) {
        if (value == null || Number.isNaN(Number(value))) return '--';
        return Number(value).toFixed(digits);
    }

    const _origSwitchTabSim = window.switchTab;
    window.switchTab = function (tabName) {
        _origSwitchTabSim && _origSwitchTabSim(tabName);
        if (tabName === 'simulations') {
            window.simulationsInit();
        }
    };
})();
