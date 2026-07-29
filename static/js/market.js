// === 市场板块（docs/product-specs/market.html）===
// Q1=Tab 第2位 · Q2=分组卡片+今日信号条 · Q3=AI 区页内分区 · Q4=预置名单可增删 · Q5=宏观内置随版本
//
// 不做自动轮询：用户每天只看 1-2 次（前置确认），打开时抓一次 + 手动刷新按钮。

let marketData = null;
let _marketLoading = false;

async function loadMarket(force) {
    if (_marketLoading) return;
    if (marketData && !force) return;   // 已有数据且不是手动刷新，就不重复抓

    _marketLoading = true;
    const btn = document.getElementById('marketRefreshBtn');
    if (btn) { btn.disabled = true; btn.textContent = '刷新中…'; }
    if (!marketData) {
        document.getElementById('marketBody').innerHTML =
            '<div class="card" style="text-align:center;color:var(--text-secondary);padding:40px;">正在获取全球行情…</div>';
    }

    try {
        const res = await fetch('/api/market/global');
        const json = await res.json();
        if (json.status !== 'ok') throw new Error(json.msg || '获取失败');
        marketData = json.data;
        renderMarket(marketData);
    } catch (e) {
        document.getElementById('marketBody').innerHTML = `
            <div class="card" style="text-align:center;padding:36px;">
                <div style="font-size:15px;font-weight:700;margin-bottom:6px;">行情获取失败</div>
                <div style="font-size:13px;color:var(--text-secondary);margin-bottom:16px;">${e.message}</div>
                <button class="btn btn-primary" onclick="loadMarket(true)">重试</button>
            </div>`;
    } finally {
        _marketLoading = false;
        if (btn) { btn.disabled = false; btn.textContent = '刷新行情'; }
    }
}

// 涨跌统一走全站的红涨绿跌（产品稿 Q2 已定，与总览页一致）
function _mCls(pct) { return pct > 0 ? 'text-profit' : pct < 0 ? 'text-loss' : 'text-neutral'; }
function _mPct(pct) {
    if (pct === null || pct === undefined) return '—';
    const s = pct > 0 ? '+' : '';
    return `${s}${pct.toFixed(2)}%`;
}
function _mPrice(it) {
    const digits = Math.abs(it.price) >= 1000 ? 2 : Math.abs(it.price) >= 10 ? 2 : 4;
    return (it.unit || '') + it.price.toLocaleString('zh-CN', {
        minimumFractionDigits: digits, maximumFractionDigits: digits,
    });
}

function renderMarket(d) {
    const parts = [];

    // ── 今日信号：只放异动（后端按 ±1% 挑过），安静的日子这条就短甚至没有
    if (d.signals && d.signals.length) {
        parts.push(`
            <div style="display:flex;gap:10px;flex-wrap:wrap;margin-bottom:16px;">
                ${d.signals.map(it => `
                    <div class="card" style="padding:10px 16px;margin:0;">
                        <div style="font-size:12px;color:var(--text-secondary);">${it.name}</div>
                        <div style="font-size:16px;font-weight:800;margin-top:2px;" class="${_mCls(it.change_pct)}">
                            ${_mPrice(it)} <span style="font-size:13px;">${_mPct(it.change_pct)}</span>
                        </div>
                    </div>`).join('')}
                <div class="card" style="padding:10px 16px;margin:0;display:flex;align-items:center;font-size:12px;color:var(--text-secondary);">
                    更新于 ${(d.fetch_at || '').slice(11, 16)}
                </div>
            </div>`);
    } else {
        parts.push(`
            <div class="card" style="padding:12px 18px;margin-bottom:16px;font-size:13px;color:var(--text-secondary);">
                今日无明显异动（涨跌幅均未超过 1%）　·　更新于 ${(d.fetch_at || '').slice(11, 16)}
            </div>`);
    }

    if (d.warnings && d.warnings.length) {
        parts.push(`
            <div class="card" style="margin-bottom:16px;background:#fff7ed;border:1px solid #fed7aa;padding:11px 16px;font-size:13px;color:#9a3412;">
                ${d.warnings.join('；')}
            </div>`);
    }

    parts.push(renderMacro(d.macro));

    // ── 四组指标卡
    parts.push(`<div class="grid-2">${d.groups.map(renderGroup).join('')}</div>`);

    // ── AI 特别关注（Q3=甲：页内分区，接着往下滚）
    parts.push(renderAISection(d));

    document.getElementById('marketBody').innerHTML = parts.join('');
}

function renderMacro(m) {
    if (!m) return '';
    const cell = (f) => {
        if (!f) return '';
        const val = f.pending
            ? `<span style="opacity:.72;font-weight:600;">待补充</span>`
            : f.value;
        const asOf = f.as_of ? `<div style="font-size:10px;opacity:.7;margin-top:1px;">${f.as_of}</div>` : '';
        return `<div style="font-size:11.5px;opacity:.85;">${f.label}
                    <b style="display:block;font-size:15px;font-weight:700;opacity:1;margin-top:3px;">${val}</b>${asOf}
                </div>`;
    };

    const hint = m.all_pending
        ? `<div style="font-size:11.5px;opacity:.8;margin-top:12px;padding-top:10px;border-top:1px solid rgba(255,255,255,.22);">
             宏观数字随版本内置维护（无可靠免费接口），当前尚未填入 —— 填好后这里会显示数值与数据截至日期
           </div>`
        : '';

    return `
        <div class="hero-card" style="margin-bottom:16px;">
            <div class="hero-label">宏观关键少数</div>
            <div style="display:flex;gap:34px;flex-wrap:wrap;margin-top:10px;">
                ${cell(m.rate)}${cell(m.next_fomc)}${cell(m.cpi)}${cell(m.nfp)}${cell(m.treasury)}
            </div>
            ${hint}
        </div>`;
}

function renderGroup(g) {
    // 单源失败：卡片降级显示缓存 + 单卡重试，而不是整页报错（产品稿 M4）
    const staleHead = g.stale
        ? `<span style="font-size:11px;color:#c2410c;background:#fff7ed;border:1px solid #fed7aa;
                        border-radius:6px;padding:1px 7px;margin-left:8px;">${g.error || '数据异常'}</span>`
        : '';
    const foot = g.stale
        ? `<div style="font-size:11.5px;color:#c2410c;margin-top:10px;">
             显示的是 ${g.fetch_at || '上次'} 的缓存　·
             <a onclick="loadMarket(true)" style="color:var(--primary);cursor:pointer;">点此重试</a>
           </div>`
        : '';

    const rows = g.items.length
        ? g.items.map(it => `
            <div style="display:flex;justify-content:space-between;align-items:baseline;
                        padding:8px 0;border-bottom:1px solid #f4f5fb;font-size:13.5px;">
                <span>${it.name}${it.sub ? `<span style="font-size:11px;color:var(--text-secondary);margin-left:6px;">${it.sub}</span>` : ''}</span>
                <span style="display:flex;gap:12px;align-items:baseline;">
                    <b style="font-variant-numeric:tabular-nums;">${_mPrice(it)}</b>
                    <b class="${_mCls(it.change_pct)}" style="width:66px;text-align:right;font-variant-numeric:tabular-nums;">${_mPct(it.change_pct)}</b>
                </span>
            </div>`).join('')
        : `<div style="font-size:13px;color:var(--text-secondary);padding:14px 0;">暂无数据</div>`;

    return `
        <div class="card" style="${g.stale ? 'opacity:.72;' : ''}">
            <div class="card-title" style="display:flex;align-items:center;">${g.title}${staleHead}</div>
            ${rows}${foot}
        </div>`;
}

function renderAISection(d) {
    const usCards = d.ai_us.items.length
        ? d.ai_us.items.map(it => `
            <div class="card" style="margin:0;padding:13px 15px;display:flex;justify-content:space-between;align-items:center;">
                <div>
                    <div style="font-size:14px;font-weight:700;">${it.name}</div>
                    <div style="font-size:11px;color:var(--text-secondary);margin-top:1px;">${it.sub || ''}</div>
                </div>
                <div style="text-align:right;">
                    <div style="font-size:15px;font-weight:800;font-variant-numeric:tabular-nums;">${_mPrice(it)}</div>
                    <div class="${_mCls(it.change_pct)}" style="font-size:12.5px;font-weight:700;">${_mPct(it.change_pct)}</div>
                </div>
            </div>`).join('')
        : `<div class="card" style="margin:0;font-size:13px;color:var(--text-secondary);">名单为空，点右上「管理名单」添加</div>`;

    const events = (d.ai_events && d.ai_events.length)
        ? d.ai_events.map(e => `
            <div style="display:flex;gap:10px;align-items:baseline;padding:7px 0;border-bottom:1px dashed #eef0fa;font-size:13px;">
                <span style="color:var(--primary);font-weight:700;width:56px;flex:none;">${e.date.slice(5)}</span>
                <span>${e.title}</span>
                ${e.soon ? '<span style="margin-left:auto;font-size:11px;color:#c2410c;">即将到来</span>' : ''}
            </div>`).join('')
        : `<div style="font-size:13px;color:var(--text-secondary);padding:12px 0;line-height:1.9;">
             事件日历随版本内置维护（财报日无稳定免费接口），当前尚未填入。
           </div>`;

    return `
        <div style="display:flex;align-items:center;gap:10px;margin:26px 0 12px;">
            <span style="background:var(--primary-soft);color:var(--primary);border-radius:8px;
                         padding:3px 10px;font-size:12px;font-weight:800;">AI</span>
            <span style="font-size:16px;font-weight:700;">特别关注</span>
            <button class="btn" onclick="openAIWatchModal()" style="margin-left:auto;font-size:12.5px;padding:5px 14px;">管理名单</button>
        </div>

        <div style="display:grid;grid-template-columns:repeat(3,1fr);gap:12px;margin-bottom:16px;">${usCards}</div>

        <div class="grid-2">
            ${renderGroup(d.ai_cn)}
            <div class="card">
                <div class="card-title">AI 事件日历</div>
                ${events}
            </div>
        </div>`;
}

// ── AI 名单管理（M5）────────────────────────────────────────────────────────

let _aiWatch = { us: [], cn: [] };

async function openAIWatchModal() {
    document.getElementById('aiWatchModal').style.display = 'flex';
    document.getElementById('aiWatchError').style.display = 'none';
    try {
        const res = await fetch('/api/market/ai-watchlist');
        const json = await res.json();
        _aiWatch = { us: json.data.us || [], cn: json.data.cn || [] };
        renderAIWatch();
    } catch (e) {
        document.getElementById('aiWatchError').textContent = '读取名单失败：' + e.message;
        document.getElementById('aiWatchError').style.display = 'block';
    }
}

function closeAIWatchModal(event) {
    if (event && event.target !== event.currentTarget) return;
    document.getElementById('aiWatchModal').style.display = 'none';
}

function renderAIWatch() {
    const chips = (list, market) => list.length
        ? list.map((w, i) => `
            <span style="background:var(--primary-soft);border-radius:14px;padding:4px 12px;font-size:12.5px;
                         display:inline-flex;align-items:center;gap:7px;">
                ${w.name}
                <span onclick="aiWatchRemove('${market}',${i})"
                      style="cursor:pointer;color:var(--text-secondary);font-weight:700;">✕</span>
            </span>`).join('')
        : `<span style="font-size:12.5px;color:var(--text-secondary);">（空）</span>`;

    document.getElementById('aiWatchUS').innerHTML = chips(_aiWatch.us, 'us');
    document.getElementById('aiWatchCN').innerHTML = chips(_aiWatch.cn, 'cn');
    document.getElementById('aiWatchUSCount').textContent = _aiWatch.us.length;
    document.getElementById('aiWatchCNCount').textContent = _aiWatch.cn.length;
}

function aiWatchRemove(market, idx) {
    _aiWatch[market].splice(idx, 1);
    renderAIWatch();
}

// 添加前先查一次行情：代码填错当场拦住，不要等保存后在页面上看到一张空卡片
async function aiWatchAdd() {
    const input = document.getElementById('aiWatchInput');
    const err = document.getElementById('aiWatchError');
    const btn = document.getElementById('aiWatchAddBtn');
    const raw = input.value.trim();
    err.style.display = 'none';
    if (!raw) return;

    btn.disabled = true; btn.textContent = '查询中…';
    try {
        const res = await fetch(`/api/market/quote-lookup?code=${encodeURIComponent(raw)}`);
        const json = await res.json();
        if (!res.ok || json.status !== 'ok') throw new Error(json.msg || '查询失败');

        const q = json.data;
        const market = q.code.startsWith('gb_') ? 'us' : 'cn';
        if (_aiWatch[market].some(w => w.code === q.code)) {
            throw new Error(`${q.name} 已经在名单里了`);
        }
        _aiWatch[market].push({ code: q.code, name: q.name, sub: raw.toUpperCase(), unit: q.unit });
        input.value = '';
        renderAIWatch();
    } catch (e) {
        err.textContent = e.message;
        err.style.display = 'block';
    } finally {
        btn.disabled = false; btn.textContent = '添加';
    }
}

async function aiWatchSave(reset) {
    const btn = document.getElementById('aiWatchSaveBtn');
    btn.disabled = true; btn.textContent = '保存中…';
    try {
        const res = await fetch('/api/market/ai-watchlist', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(reset ? { reset: true } : _aiWatch),
        });
        const json = await res.json();
        if (json.status !== 'ok') throw new Error(json.msg || '保存失败');
        document.getElementById('aiWatchModal').style.display = 'none';
        await loadMarket(true);   // 名单变了，重新抓一次行情
    } catch (e) {
        const err = document.getElementById('aiWatchError');
        err.textContent = e.message; err.style.display = 'block';
    } finally {
        btn.disabled = false; btn.textContent = '完成';
    }
}
