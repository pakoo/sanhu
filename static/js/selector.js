/* 选基 Tab — selector.js */

(function () {
    'use strict';

    // ── state ──────────────────────────────────────────────────────────────────
    let selectorQuestions = [];
    let selectorAnswers = {};      // {q1:'a', q2:'b', ...}
    let selectorDiagData = null;   // latest diagnose result
    let selectorSessionId = null;  // latest recommend session_id

    // ── init: called when tab becomes active ──────────────────────────────────
    window.selectorInit = async function () {
        if (selectorQuestions.length === 0) {
            await selectorLoadQuestions();
        }
        if (!selectorDiagData) {
            await selectorRunDiagnose();
        }
    };

    // ── load questions from API ──────────────────────────────────────────────
    async function selectorLoadQuestions() {
        try {
            const res = await fetch('/api/selector/questions');
            const json = await res.json();
            if (json.status === 'ok') {
                selectorQuestions = json.data;
                renderQuestions();
            }
        } catch (e) {
            document.getElementById('selectorQContainer').innerHTML =
                '<div class="sub text-neutral">加载问题失败，请刷新重试</div>';
        }
    }

    // ── render questionnaire ──────────────────────────────────────────────────
    function renderQuestions() {
        const container = document.getElementById('selectorQContainer');
        container.innerHTML = selectorQuestions.map(q => `
            <div class="selector-question" id="sq-${q.id}">
                <div style="font-weight:600;font-size:14px;margin-bottom:10px;color:var(--text);">
                    ${q.text}
                </div>
                <div style="display:flex;flex-wrap:wrap;gap:8px;">
                    ${q.options.map(opt => `
                        <button class="selector-opt-btn"
                                id="sqopt-${q.id}-${opt.value}"
                                onclick="selectorSelectOption('${q.id}','${opt.value}')"
                                style="padding:7px 16px;border-radius:20px;border:1.5px solid var(--border);
                                       background:#f8fafc;color:var(--text);cursor:pointer;font-size:13px;
                                       transition:all 0.15s;">
                            ${opt.label}
                        </button>
                    `).join('')}
                </div>
            </div>
        `).join('');
    }

    // ── user selects an option ────────────────────────────────────────────────
    window.selectorSelectOption = function (qId, value) {
        selectorAnswers[qId] = value;

        // deselect all options in this question, select the clicked one
        selectorQuestions.find(q => q.id === qId).options.forEach(opt => {
            const btn = document.getElementById(`sqopt-${qId}-${opt.value}`);
            if (!btn) return;
            const selected = opt.value === value;
            btn.style.background = selected ? 'var(--primary)' : '#f8fafc';
            btn.style.color = selected ? 'white' : 'var(--text)';
            btn.style.borderColor = selected ? 'var(--primary)' : 'var(--border)';
            btn.style.fontWeight = selected ? '600' : '400';
        });

        // enable recommend button when all answered
        const allAnswered = selectorQuestions.every(q => selectorAnswers[q.id]);
        const btn = document.getElementById('selectorRecommendBtn');
        btn.disabled = !allAnswered;
        btn.style.opacity = allAnswered ? '1' : '0.45';
        btn.style.cursor = allAnswered ? 'pointer' : 'not-allowed';
    };

    // ── diagnose (body exam, auto-called on tab open) ─────────────────────────
    window.selectorRefreshDiagnose = async function () {
        document.getElementById('selectorTopGaps').innerHTML =
            '<div class="sub text-neutral" style="font-size:13px;">体检中...</div>';
        await selectorRunDiagnose();
    };

    async function selectorRunDiagnose() {
        try {
            const res = await fetch('/api/selector/diagnose', { method: 'POST' });
            const json = await res.json();
            if (json.status !== 'ok') throw new Error(json.msg);
            selectorDiagData = json.data;
            renderDiagSummary(json.data);
        } catch (e) {
            document.getElementById('selectorTopGaps').innerHTML =
                `<div class="sub" style="color:var(--danger);font-size:13px;">体检失败: ${e.message}</div>`;
        }
    }

    function renderDiagSummary(data) {
        const gaps = data.gaps || [];
        document.getElementById('selectorGapCount').textContent = gaps.length;

        const top3 = gaps.slice(0, 3);
        const html = top3.length === 0
            ? '<div class="sub text-neutral" style="font-size:13px;">暂无权益基金穿透数据，请先在「持仓透视」Tab 抓取数据</div>'
            : top3.map(g => `
                <div style="display:flex;align-items:center;gap:8px;margin-bottom:6px;">
                    <span style="display:inline-block;width:8px;height:8px;border-radius:50%;
                                 background:${priorityColor(g.priority)};flex-shrink:0;"></span>
                    <span style="font-size:13px;">
                        <strong>${g.name}</strong> 暴露 ${(g.current_pct || 0).toFixed(1)}%
                        ${g.suggest_add_pct > 0 ? `→ 建议 ${g.suggest_add_pct.toFixed(0)}%` : ''}
                    </span>
                </div>
            `).join('');

        document.getElementById('selectorTopGaps').innerHTML =
            `<div class="label" style="margin-bottom:8px;">Top 缺口</div>${html}`;
    }

    function priorityColor(p) {
        if (p >= 0.8) return 'var(--danger)';
        if (p >= 0.5) return 'var(--warning)';
        return 'var(--success)';
    }

    // ── get recommendation ────────────────────────────────────────────────────
    window.selectorGetRecommendation = async function () {
        const btn = document.getElementById('selectorRecommendBtn');
        btn.disabled = true;
        btn.textContent = '分析中...';

        try {
            const res = await fetch('/api/selector/recommend', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ answers: selectorAnswers }),
            });
            const json = await res.json();
            if (json.status !== 'ok') throw new Error(json.msg);
            selectorSessionId = json.data.session_id;
            renderResult(json.data);
            document.getElementById('selectorResult').style.display = 'block';
            document.getElementById('selectorResult').scrollIntoView({ behavior: 'smooth', block: 'start' });
        } catch (e) {
            selectorShowToast(`推荐失败: ${e.message}`, 4000);
        } finally {
            btn.disabled = false;
            btn.textContent = '生成选基推荐';
            btn.style.opacity = '1';
        }
    };

    // ── render result ────────────────────────────────────────────────────────
    function renderResult(data) {
        const gaps = (data.gaps || []).slice(0, 3);
        const candidates = data.candidates || [];
        const warnings = data.warnings || [];

        // gap cards
        const gapContainer = document.getElementById('selectorGapCards');
        if (gaps.length === 0) {
            gapContainer.innerHTML = '<div class="sub text-neutral" style="font-size:13px;">暂无诊断缺口</div>';
        } else {
            gapContainer.innerHTML = gaps.map(g => `
                <div style="background:#fff;border:1.5px solid ${priorityColor(g.priority)};border-radius:12px;
                            padding:14px 18px;min-width:180px;flex:1;">
                    <div style="font-weight:700;font-size:15px;color:${priorityColor(g.priority)};">${g.name}</div>
                    <div style="font-size:12px;color:var(--text-secondary);margin-top:4px;">
                        当前 ${(g.current_pct || 0).toFixed(1)}%
                        ${g.suggest_add_pct > 0 ? ` → 建议 ${g.suggest_add_pct.toFixed(0)}%` : ''}
                    </div>
                    <div style="font-size:11px;color:var(--text-secondary);margin-top:2px;">
                        优先级 ${(g.priority * 100).toFixed(0)}%
                    </div>
                </div>
            `).join('');
        }

        // warning banner
        if (warnings.length > 0) {
            gapContainer.insertAdjacentHTML('afterend', `
                <div style="background:#fef3c7;border:1px solid #fbbf24;border-radius:8px;padding:10px 14px;
                            font-size:13px;color:#92400e;margin-bottom:12px;">
                    ⚠️ ${warnings.join(' · ')}
                </div>
            `);
        }

        // candidate cards
        const candContainer = document.getElementById('selectorCandidateCards');
        if (candidates.length === 0) {
            candContainer.innerHTML = '<div class="sub text-neutral" style="font-size:13px;grid-column:1/-1;">暂无候选基金，请尝试放宽问诊条件</div>';
        } else {
            candContainer.innerHTML = candidates.map(c => renderCandidateCard(c)).join('');
            // 异步加载每张卡的仓位建议预览
            candidates.forEach(c => loadCandidateSizingHint(c.code));
        }
    }

    async function loadCandidateSizingHint(code) {
        try {
            const res = await fetch(`/api/position-sizing/${code}`);
            const json = await res.json();
            const el = document.getElementById(`sizing-hint-${code}`);
            if (!el || !json || json.status !== 'ok') return;
            const d = json.data;
            if (d.suggested_amount === 0) {
                el.innerHTML = '<span style="color:var(--danger);">建议暂不加仓（类别已超配）</span>';
            } else {
                el.innerHTML = `建议仓位: <strong style="color:var(--primary);">¥${d.suggested_amount.toLocaleString()}</strong> <span style="color:var(--text-secondary);font-size:10px;">(详见创建决策)</span>`;
            }
        } catch (_) { /* silent */ }
    }

    function renderCandidateCard(c) {
        const score = (c.composite * 100).toFixed(0);
        const scoreColor = score >= 70 ? 'var(--success)' : score >= 50 ? 'var(--warning)' : 'var(--danger)';
        const fragments = (c.reason_fragments || []).join(' · ');
        const overlap = c.overlap_rate != null
            ? `重叠率 ${(c.overlap_rate * 100).toFixed(0)}%`
            : '重叠率 N/A';
        const sourceBadge = c.profile_source === 'pierced'
            ? '<span style="background:#dcfce7;color:#15803d;padding:1px 6px;border-radius:4px;font-size:11px;">季报穿透</span>'
            : '<span style="background:#f1f5f9;color:var(--text-secondary);padding:1px 6px;border-radius:4px;font-size:11px;">名称推断</span>';

        return `
            <div style="background:#fff;border:1px solid var(--border);border-radius:12px;padding:16px;
                        box-shadow:0 1px 4px rgba(0,0,0,0.06);">
                <div style="display:flex;align-items:flex-start;justify-content:space-between;margin-bottom:10px;">
                    <div>
                        <div style="font-weight:700;font-size:15px;">${c.name || c.code}</div>
                        <div style="font-size:12px;color:var(--text-secondary);">${c.code} · ${sourceBadge}</div>
                    </div>
                    <div style="text-align:right;">
                        <div style="font-size:22px;font-weight:800;color:${scoreColor};">${score}</div>
                        <div style="font-size:11px;color:var(--text-secondary);">综合分</div>
                    </div>
                </div>
                <div style="font-size:12px;color:var(--text-secondary);margin-bottom:10px;line-height:1.8;">
                    ${fragments || '无详细指标数据'}
                </div>
                <div style="font-size:11px;color:var(--text-secondary);margin-bottom:8px;" id="sizing-hint-${c.code}">
                    建议仓位加载中...
                </div>
                <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap;">
                    <span style="font-size:12px;color:var(--text-secondary);">${overlap}</span>
                    <div style="display:flex;gap:6px;">
                        <button class="btn btn-primary"
                                onclick="selectorAdoptFund('${c.code}', this)"
                                style="font-size:12px;padding:5px 14px;">
                            + 加入自选
                        </button>
                        <button class="btn"
                                onclick="openDecisionModal('${c.code}', '${(c.name || '').replace(/'/g, '')}', selectorSessionId)"
                                style="font-size:12px;padding:5px 14px;background:#f8fafc;border:1px solid var(--border);">
                            📋 创建决策
                        </button>
                    </div>
                </div>
            </div>
        `;
    }

    // ── adopt fund to watchlist ───────────────────────────────────────────────
    window.selectorAdoptFund = async function (code, btn) {
        const orig = btn.textContent;
        btn.disabled = true;
        btn.textContent = '添加中...';
        try {
            const res = await fetch('/api/selector/adopt', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ code }),
            });
            const json = await res.json();
            if (json.status === 'ok') {
                btn.textContent = '✓ 已加入';
                btn.style.background = 'var(--success)';
                selectorShowToast(`${code} 已加入自选列表`);
            } else {
                throw new Error(json.msg || '添加失败');
            }
        } catch (e) {
            btn.textContent = orig;
            btn.disabled = false;
            selectorShowToast(`添加失败: ${e.message}`, 3000);
        }
    };

    // ── export Claude prompt ──────────────────────────────────────────────────
    window.selectorExportPrompt = async function () {
        if (!selectorSessionId) { selectorShowToast('请先生成推荐'); return; }
        const btn = document.getElementById('selectorExportBtn');
        btn.textContent = '复制中...';
        try {
            const res = await fetch(`/api/selector/export/${selectorSessionId}`);
            if (!res.ok) throw new Error('导出失败');
            const text = await res.text();
            await navigator.clipboard.writeText(text);
            btn.textContent = '✓ 已复制';
            selectorShowToast('提示词已复制到剪贴板，粘贴到 Claude 即可开始对话 🎉', 4000);
            setTimeout(() => { btn.textContent = '📋 导出 Claude 提示词'; }, 3000);
        } catch (e) {
            btn.textContent = '📋 导出 Claude 提示词';
            selectorShowToast(`导出失败: ${e.message}`, 3000);
        }
    };

    // ── export JSON backup ────────────────────────────────────────────────────
    window.selectorExportJSON = async function () {
        if (!selectorSessionId) { selectorShowToast('请先生成推荐'); return; }
        try {
            const res = await fetch(`/api/selector/session/${selectorSessionId}`);
            const json = await res.json();
            const blob = new Blob([JSON.stringify(json.data, null, 2)], { type: 'application/json' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = `selector_${selectorSessionId.slice(0, 8)}.json`;
            a.click();
            URL.revokeObjectURL(url);
            selectorShowToast('JSON 已下载');
        } catch (e) {
            selectorShowToast(`下载失败: ${e.message}`, 3000);
        }
    };

    // ── toast helper ─────────────────────────────────────────────────────────
    function selectorShowToast(msg, duration = 2500) {
        const el = document.getElementById('selectorToast');
        if (!el) return;
        el.textContent = msg;
        el.style.display = 'block';
        setTimeout(() => { el.style.display = 'none'; }, duration);
    }

    // ── hook into global switchTab ────────────────────────────────────────────
    // Patch the existing switchTab to trigger selectorInit when "selector" tab is opened.
    const _origSwitchTab = window.switchTab;
    window.switchTab = function (tabName) {
        _origSwitchTab && _origSwitchTab(tabName);
        if (tabName === 'selector') {
            selectorInit();
        }
    };

})();
