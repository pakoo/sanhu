/* AI 分析中心 + 统一提示词弹窗 + 全局悬浮球 + 结论台账
 *
 * 产品稿：docs/product-specs/ai-everywhere.html
 *   Q2 = A  新增顶级 Tab「🤖 AI 分析」
 *   Q3 = 甲+丙  各页轻按钮为主，右下角悬浮球兜底
 *   Q4 = A  只复制，不开任何外链 —— 全文件不出现 window.open / target=_blank
 *   Q7 = B  默认带真实金额，弹窗里可一键脱敏
 *   Q8 = C  结论可回填存档，但不与决策模块联动
 */
(function () {
    'use strict';

    let scenesCache = null;
    let current = null;      // 当前弹窗里的 {scene, code, id, result}
    let redact = false;      // Q7=B：默认关闭脱敏

    // ── 工具 ──────────────────────────────────────────────────────────────
    const $ = (id) => document.getElementById(id);
    const esc = (s) => String(s == null ? '' : s)
        .replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));

    function toast(msg) {
        let t = $('aiToast');
        if (!t) {
            t = document.createElement('div');
            t.id = 'aiToast';
            t.className = 'ai-toast';
            document.body.appendChild(t);
        }
        t.textContent = msg;
        t.classList.add('show');
        clearTimeout(t._timer);
        t._timer = setTimeout(() => t.classList.remove('show'), 2200);
    }

    async function getJSON(url) {
        const res = await fetch(url);
        const body = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(body.message || body.msg || '请求失败');
        return body.data !== undefined ? body.data : body;
    }

    // ── AI 中心（H1 / H2）─────────────────────────────────────────────────
    async function loadAICenter(force) {
        const wrap = $('aiCenterBody');
        if (!wrap) return;
        if (scenesCache && !force) { renderCenter(scenesCache); return; }

        wrap.innerHTML = '<div class="loading">加载中</div>';
        try {
            scenesCache = await getJSON('/api/ai/scenes');
            renderCenter(scenesCache);
        } catch (e) {
            wrap.innerHTML = `<div class="ai-empty">场景加载失败：${esc(e.message)}</div>`;
        }
        loadNotes();
    }

    function renderCenter(data) {
        const wrap = $('aiCenterBody');
        const asOf = $('aiCenterAsOf');
        if (asOf) asOf.textContent = `数据截至 ${data.generated_at || ''}`;

        // 空态（H2）：没有任何持仓时不给「生成」按钮 ——
        // 这个功能的价值全在本地数据上，没数据生成出来只会是一段套话
        if (!data.held_funds) {
            wrap.innerHTML = `
              <div class="ai-empty-wrap">
                <div class="ai-empty-icon">🤖</div>
                <div class="ai-empty-title">还没有可以给 AI 看的数据</div>
                <div class="ai-empty-desc">
                  这里生成的提示词，值钱的部分全部来自你的真实持仓和工具算出的指标。<br>
                  现在没有这些数据，随便生成一份只会得到一段套话。先做下面任意一件：
                </div>
                <div class="ai-scene-grid" style="max-width:720px;margin:20px auto 0;text-align:left">
                  <div class="ai-scene"><div class="ai-scene-ic">✍️</div>
                    <div class="ai-scene-t">录入持仓</div>
                    <div class="ai-scene-d">照支付宝持有页逐只填，3 分钟。九个场景里有七个立刻可用</div>
                    <div class="ai-scene-ft"><button class="ai-btn solid" data-goto="dashboard">去录入</button></div></div>
                  <div class="ai-scene"><div class="ai-scene-ic">⭐</div>
                    <div class="ai-scene-t">加个关注</div>
                    <div class="ai-scene-d">还没买？先加关注列表，工具 24 小时内抓好持仓和评分</div>
                    <div class="ai-scene-ft"><button class="ai-btn" data-goto="dashboard">去关注</button></div></div>
                  <div class="ai-scene"><div class="ai-scene-ic">🧭</div>
                    <div class="ai-scene-t">答选基问卷</div>
                    <div class="ai-scene-d">一只都没有也能答，出「补什么基金」的提示词</div>
                    <div class="ai-scene-ft"><button class="ai-btn" data-goto="selector">去选基</button></div></div>
                </div>
              </div>`;
            wrap.querySelectorAll('[data-goto]').forEach((b) => {
                b.onclick = () => switchTabTo(b.dataset.goto);
            });
            return;
        }

        // 按分组渲染场景网格
        const groups = [];
        data.scenes.forEach((s) => {
            let g = groups.find((x) => x.name === s.group);
            if (!g) { g = { name: s.group, items: [] }; groups.push(g); }
            g.items.push(s);
        });

        wrap.innerHTML = groups.map((g) => `
          <div class="card ai-group">
            <div class="card-title">${esc(g.name)}
              <span class="ai-group-count">${g.items.length} 个场景</span></div>
            <div class="ai-scene-grid">
              ${g.items.map(renderScene).join('')}
            </div>
          </div>`).join('');

        wrap.querySelectorAll('[data-scene]').forEach((btn) => {
            btn.onclick = () => {
                const s = data.scenes.find((x) => x.id === btn.dataset.scene);
                if (!s) return;
                if (s.needs_code) return pickFund(s);
                if (s.needs_id && s.id === 'decision_review') return pickDecision(s);
                openPrompt(s.id);
            };
        });
    }

    function renderScene(s) {
        // 徽章三态：🟢 数据齐 / 🟡 部分缺 / ⚪ 前置条件没满足
        let badge = '<span class="ai-rdy g">🟢 数据齐</span>';
        if (!s.ready) badge = `<span class="ai-rdy w">⚪ ${esc(s.note || '暂不可用')}</span>`;
        else if (s.note) badge = `<span class="ai-rdy y">🟡 ${esc(s.note)}</span>`;

        let action = `<button class="ai-btn solid" data-scene="${esc(s.id)}">生成 →</button>`;
        if (!s.ready) action = `<button class="ai-btn dis" disabled>生成 →</button>`;
        else if (s.needs_code) action = `<button class="ai-btn" data-scene="${esc(s.id)}">选择基金</button>`;
        else if (s.needs_id && s.id === 'decision_review') action = `<button class="ai-btn solid" data-scene="${esc(s.id)}">选择决策</button>`;

        return `
          <div class="ai-scene${s.ready ? '' : ' locked'}">
            <div class="ai-scene-ic">${s.icon || '🤖'}</div>
            <div class="ai-scene-t">${esc(s.title)}</div>
            <div class="ai-scene-d">${esc(s.desc)}</div>
            <div class="ai-scene-ft">${badge}${action}</div>
          </div>`;
    }

    function switchTabTo(tab) {
        const btns = document.querySelectorAll('.tab-btn');
        for (const b of btns) {
            if ((b.getAttribute('onclick') || '').includes(`'${tab}'`)) { b.click(); return; }
        }
    }

    // ── 选基金 / 选决策（needs_code / needs_id 的场景）────────────────────
    async function pickFund(scene) {
        let list = [];
        try {
            const p = await getJSON('/api/portfolio');
            list = (p.holdings || p.funds || []).map((x) => ({ code: x.code, name: x.name }));
        } catch (e) { /* 拿不到就让用户手填 */ }

        const hint = list.length
            ? '你的持仓：\n' + list.map((x) => `${x.code} ${x.name}`).join('\n') + '\n\n'
            : '';
        const code = window.prompt(`${hint}输入要分析的基金代码（6 位）：`, list[0] ? list[0].code : '');
        if (!code) return;
        openPrompt(scene.id, { code: code.trim() });
    }

    async function pickDecision(scene) {
        let list = [];
        try { list = (await getJSON('/api/decisions?limit=20')) || []; } catch (e) { /* ignore */ }
        if (!list.length) { toast('还没有记录过决策'); return; }
        const hint = list.map((d) => `#${d.id} ${d.code} ${d.decision_type} ${d.created_at || ''}`).join('\n');
        const id = window.prompt(`选一笔要复盘的决策（填 # 后面的编号）：\n\n${hint}`, String(list[0].id));
        if (!id) return;
        openPrompt(scene.id, { id: id.trim() });
    }

    // ── 统一提示词弹窗（P1–P5）────────────────────────────────────────────
    async function openPrompt(sceneId, params) {
        current = Object.assign({ scene: sceneId }, params || {});
        const modal = $('aiPromptModal');
        if (!modal) return;
        modal.style.display = 'flex';
        showPane('gen');
        $('aiPromptTitle').textContent = '正在生成…';
        $('aiPromptMeta').textContent = '';
        $('aiPromptText').value = '';
        $('aiIncluded').innerHTML = '';
        $('aiPromptNotReady').style.display = 'none';
        $('aiPromptMain').style.display = 'none';

        const qs = [];
        if (current.code) qs.push('code=' + encodeURIComponent(current.code));
        if (current.id) qs.push('id=' + encodeURIComponent(current.id));
        if (redact) qs.push('redact=1');

        try {
            const data = await getJSON(`/api/ai/prompts/${encodeURIComponent(sceneId)}?${qs.join('&')}`);
            current.result = data;
            renderPrompt(data);
        } catch (e) {
            $('aiPromptTitle').textContent = '生成失败';
            $('aiPromptNotReady').style.display = 'block';
            $('aiPromptNotReady').innerHTML = `<div class="ai-notready-msg">${esc(e.message)}</div>`;
        }
    }

    function renderPrompt(d) {
        $('aiPromptTitle').textContent = d.title || 'AI 提示词';

        // P3 数据不足态：不生成半截提示词，直接给补数据的路径
        if (!d.ready) {
            $('aiPromptMain').style.display = 'none';
            $('aiPromptNotReady').style.display = 'block';
            $('aiPromptNotReady').innerHTML = `
              <div class="ai-notready-title">这个场景的数据还不够</div>
              <div class="ai-notready-msg">${esc(d.ready_note)}</div>
              <div class="ai-notready-hint">宁可不给，也不给你一份空壳提示词 —— 那只会让 AI 回你一段套话。</div>`;
            $('aiPromptMeta').textContent = '';
            return;
        }

        $('aiPromptNotReady').style.display = 'none';
        $('aiPromptMain').style.display = 'block';
        $('aiPromptText').value = d.prompt || '';

        const gapCount = (d.gaps || []).length;
        $('aiPromptMeta').innerHTML =
            `数据截至 ${esc(d.as_of)} ｜ 全文 <b>${d.chars}</b> 字` +
            (gapCount ? ` ｜ <span class="ai-gap-count">⚠️ ${gapCount} 项工具拿不到（已在提示词里标注给 AI）</span>` : '');

        // P2 数据清单：included 全绿，gaps 按 🟢🟡🔴 分类
        const inc = (d.included || []).map((i) =>
            `<div class="ai-dl-li"><span class="s">✅</span><span>${esc(i.label)}${i.detail ? ' · <b>' + esc(i.detail) + '</b>' : ''}</span></div>`).join('');
        const gapIcon = { green: '🟢', yellow: '🟡', red: '🔴' };
        const gaps = (d.gaps || []).map((g) =>
            `<div class="ai-dl-li"><span class="s">${gapIcon[g.level] || '⚠️'}</span><span><b>${esc(g.label)}</b>${g.howto ? ' —— ' + esc(g.howto) : ''}</span></div>`).join('');

        $('aiIncluded').innerHTML =
            `<div class="ai-dl"><div class="ai-dl-h ok">✅ 已带上（${(d.included || []).length} 类）</div>${inc || '<div class="ai-dl-li">（无）</div>'}</div>` +
            (gaps ? `<div class="ai-dl warn"><div class="ai-dl-h warn">⚠️ 拿不到（已按能否补齐分类）</div>${gaps}</div>` : '');

        $('aiRedactToggle').checked = !!d.redacted;
        $('aiRedactHint').textContent = d.redacted
            ? '当前不含任何金额，AI 只能用百分比给建议'
            : '当前含真实持仓金额 —— 复制出去就离开本机了';
    }

    function showPane(which) {
        $('aiPanePrompt').style.display = which === 'gen' ? 'block' : 'none';
        $('aiPaneNote').style.display = which === 'note' ? 'block' : 'none';
        $('aiTabPrompt').classList.toggle('active', which === 'gen');
        $('aiTabNote').classList.toggle('active', which === 'note');
    }

    // Q4=A：只复制，不开任何外链。复制完给一句去哪儿用的说明，但不替用户跳转。
    async function copyPrompt() {
        const text = $('aiPromptText').value;
        if (!text) { toast('没有可复制的内容'); return; }
        try {
            await navigator.clipboard.writeText(text);
        } catch (e) {
            const ta = $('aiPromptText');
            ta.select();
            document.execCommand('copy');
            window.getSelection().removeAllRanges();
        }
        toast(`已复制 ${text.length} 字 —— 打开 Claude 或 ChatGPT 新建对话，粘贴发送即可`);
        $('aiCopyHint').style.display = 'block';
    }

    // ── 回填与台账（R1 / R2，Q8=C）────────────────────────────────────────
    async function saveNote() {
        const content = $('aiNoteContent').value.trim();
        if (!content) { toast('先把 AI 的回答粘进来'); return; }
        const codes = [];
        if (current && current.code) codes.push(current.code);

        try {
            await fetch('/api/ai/notes', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    scene: current ? current.scene : '',
                    codes,
                    summary: $('aiNoteSummary').value.trim(),
                    content,
                }),
            }).then((r) => { if (!r.ok) throw new Error('存档失败'); });
            $('aiNoteContent').value = '';
            $('aiNoteSummary').value = '';
            toast('已存档 —— 之后能在「AI 分析」页回溯这次判断');
            loadNotes();
            showPane('gen');
        } catch (e) {
            toast(e.message);
        }
    }

    async function loadNotes() {
        const box = $('aiNotesList');
        if (!box) return;
        try {
            const d = await getJSON('/api/ai/notes?limit=30');
            const notes = d.notes || [];
            if (!notes.length) {
                box.innerHTML = '<div class="ai-notes-empty">还没有存档过 AI 结论。生成提示词、拿到回答后，点弹窗里的「回填 AI 结论」存下来，之后就能回溯当时是怎么判断的。</div>';
                return;
            }
            box.innerHTML = notes.map((n) => `
              <div class="ai-note-row">
                <div class="ai-note-main">
                  <div class="ai-note-t">${esc(n.scene_title)}
                    ${(n.codes || []).map((c) => `<span class="ai-note-code">${esc(c)}</span>`).join('')}
                  </div>
                  <div class="ai-note-s">${esc(n.summary || n.content.slice(0, 60) + '…')}</div>
                </div>
                <div class="ai-note-side">
                  <div class="ai-note-date">${esc(n.created_at)}</div>
                  <button class="ai-btn ghost" data-note-view="${n.id}">看全文</button>
                  <button class="ai-btn ghost danger" data-note-del="${n.id}">删除</button>
                </div>
              </div>`).join('');

            box.querySelectorAll('[data-note-view]').forEach((b) => {
                b.onclick = () => {
                    const n = notes.find((x) => String(x.id) === b.dataset.noteView);
                    if (n) window.alert(n.content);
                };
            });
            box.querySelectorAll('[data-note-del]').forEach((b) => {
                b.onclick = async () => {
                    if (!window.confirm('删掉这条存档？')) return;
                    await fetch('/api/ai/notes/' + b.dataset.noteDel, { method: 'DELETE' });
                    loadNotes();
                };
            });
        } catch (e) {
            box.innerHTML = `<div class="ai-notes-empty">台账加载失败：${esc(e.message)}</div>`;
        }
    }

    // ── 悬浮球（Q3 丙）────────────────────────────────────────────────────
    // 感知当前 Tab，把该页最相关的场景排在最前 —— 不点开就不知道能问什么，
    // 所以 title 里直接写出来
    const TAB_SCENE = {
        dashboard: 'portfolio_checkup',
        risk: 'risk_alerts',
        rebalance: 'rebalance_review',
        holdings: 'holdings_overlap',
        selector: 'selector_gap',
        decisions: 'decision_review',
    };

    function currentTab() {
        const el = document.querySelector('.tab-content.active');
        return el ? el.id.replace('tab-', '') : 'dashboard';
    }

    function onFabClick() {
        const scene = TAB_SCENE[currentTab()];
        if (scene) {
            const s = (scenesCache && scenesCache.scenes || []).find((x) => x.id === scene);
            if (!s || s.ready !== false) { openPrompt(scene); return; }
        }
        switchTabTo('ai');
    }

    // ── 就地入口（Q3 甲）──────────────────────────────────────────────────
    // 页面上任何带 data-ai-scene 的按钮都直接接到统一弹窗，
    // 不用每个页面各写一遍点击逻辑
    function bindInlineButtons() {
        document.querySelectorAll('[data-ai-scene]').forEach((btn) => {
            if (btn._aiBound) return;
            btn._aiBound = true;
            btn.onclick = (ev) => {
                ev.preventDefault();
                ev.stopPropagation();
                const code = btn.dataset.aiCode || '';
                openPrompt(btn.dataset.aiScene, code ? { code } : undefined);
            };
        });
    }

    // ── 对外接口 ──────────────────────────────────────────────────────────
    window.aiOpenPrompt = openPrompt;
    window.aiLoadCenter = loadAICenter;
    window.aiBindInline = bindInlineButtons;

    document.addEventListener('DOMContentLoaded', () => {
        bindInlineButtons();

        const fab = $('aiFab');
        if (fab) fab.onclick = onFabClick;

        const bind = (id, fn) => { const el = $(id); if (el) el.onclick = fn; };
        bind('aiCopyBtn', copyPrompt);
        bind('aiCloseBtn', () => { $('aiPromptModal').style.display = 'none'; });
        bind('aiTabPrompt', () => showPane('gen'));
        bind('aiTabNote', () => showPane('note'));
        bind('aiNoteSave', saveNote);
        bind('aiToggleList', () => {
            const el = $('aiIncluded');
            const open = el.style.display !== 'none';
            el.style.display = open ? 'none' : 'block';
            $('aiToggleList').textContent = open ? '展开清单 ▾' : '收起清单 ▴';
        });

        const rt = $('aiRedactToggle');
        if (rt) rt.onchange = () => {
            redact = rt.checked;
            if (current) openPrompt(current.scene, { code: current.code, id: current.id });
        };

        const modal = $('aiPromptModal');
        if (modal) modal.onclick = (ev) => { if (ev.target === modal) modal.style.display = 'none'; };
    });
})();
