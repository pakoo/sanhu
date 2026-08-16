// === 板块资金流（产品稿 docs/product-specs/sector-fund-flow.html）===
// Q1=A 双向排行榜 + B 热力图切换，点击下钻分时 · Q2=盘中每 30 分钟 · Q3=行业+概念 Tab
// Q4=日线全量 + Top20 分时落库 · Q5=图表库（见下方说明）
//
// 关于图表库：产品稿 Q5 原定引 TradingView Lightweight Charts，前提是「项目没有图表库」。
// 实际上 index.html 早就从 CDN 载入了 ECharts 5 且 app.js 在用 —— 那个前提是错的。
// 既然 ECharts 已在，treemap（热力图）也现成，再引第二个图表库不划算，故改用 ECharts。
//
// 榜单条形**不用图表库**：它需要展开/折叠被合并的板块、点击下钻，
// 用 HTML+CSS 画条比塞进图表库更好控制，也更容易做无障碍。

const FF_TOPN = 10;

let ffState = {
    kind: 'industry',   // industry | concept
    view: 'rank',       // rank | heat
    dedup: true,
    data: null,
    loading: false,
};

// ── 数值格式 ────────────────────────────────────────────────────────────────
// 后端一律给「元」，避免早早丢精度；换算成人能读的单位在这里做。
function ffYi(v) {
    if (v === null || v === undefined) return '—';
    const yi = v / 1e8;
    if (Math.abs(yi) >= 100) return (yi > 0 ? '+' : '') + yi.toFixed(0) + '亿';
    if (Math.abs(yi) >= 1) return (yi > 0 ? '+' : '') + yi.toFixed(1) + '亿';
    return (yi > 0 ? '+' : '') + (v / 1e4).toFixed(0) + '万';
}
function ffPct(p) {
    if (p === null || p === undefined) return '—';
    return (p > 0 ? '+' : '') + p.toFixed(2) + '%';
}
// 红涨绿跌，与全站一致
function ffCls(v) { return v > 0 ? 'text-profit' : v < 0 ? 'text-loss' : 'text-neutral'; }
function ffEsc(s) {
    return String(s == null ? '' : s).replace(/[&<>"]/g,
        c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
}

// ── 加载 ────────────────────────────────────────────────────────────────────
async function loadFundFlow(force) {
    if (ffState.loading) return;
    if (ffState.data && !force) return;

    ffState.loading = true;
    const btn = document.getElementById('ffRefreshBtn');
    if (btn) { btn.disabled = true; btn.textContent = '刷新中…'; }
    if (!ffState.data) {
        document.getElementById('ffBody').innerHTML =
            '<div class="card" style="text-align:center;color:var(--text-secondary);padding:40px;">正在获取板块资金流…</div>';
    }
    try {
        const url = `/api/fundflow/rank?kind=${ffState.kind}&dedup=${ffState.dedup ? 1 : 0}`;
        const res = await fetch(url);
        const json = await res.json();
        if (json.status !== 'ok') throw new Error(json.msg || '获取失败');
        ffState.data = json.data;
        renderFundFlow();
    } catch (e) {
        document.getElementById('ffBody').innerHTML = `
            <div class="card" style="text-align:center;padding:36px;">
                <div style="font-size:15px;font-weight:700;margin-bottom:6px;">板块资金流获取失败</div>
                <div style="font-size:13px;color:var(--text-secondary);margin-bottom:16px;">${ffEsc(e.message)}</div>
                <button class="btn btn-primary" onclick="loadFundFlow(true)">重试</button>
            </div>`;
    } finally {
        ffState.loading = false;
        if (btn) { btn.disabled = false; btn.textContent = '刷新'; }
        ffStartPolling();   // 每次加载后确保轮询在跑（重复调用会先清掉旧的定时器）
    }
}

function ffSwitchKind(kind) {
    if (ffState.kind === kind) return;
    ffState.kind = kind; ffState.data = null;
    loadFundFlow(true);
}
function ffSwitchView(view) {
    ffState.view = view;
    renderFundFlow();
}
function ffToggleDedup(el) {
    ffState.dedup = el.checked; ffState.data = null;
    loadFundFlow(true);
}

// ── 渲染 ────────────────────────────────────────────────────────────────────
function renderFundFlow() {
    const d = ffState.data;
    if (!d) return;
    const body = document.getElementById('ffBody');

    const notice = d.stale && d.notice ? `
        <div class="card" style="border-left:4px solid var(--warning);padding:12px 16px;margin-bottom:14px;">
            <div style="font-size:13px;font-weight:700;color:var(--warning);margin-bottom:2px;">
                ⚠️ 数据不是实时的${d.data_date ? '（数据日期 ' + ffEsc(d.data_date) + '）' : ''}
            </div>
            <div style="font-size:12.5px;color:var(--text-secondary);line-height:1.7;">${ffEsc(d.notice)}</div>
            ${d.detail ? `<details style="margin-top:6px;"><summary style="font-size:11.5px;color:var(--text-secondary);cursor:pointer;">技术细节（排查用）</summary>
                <div style="font-size:11px;color:var(--text-secondary);word-break:break-all;margin-top:4px;">${ffEsc(d.detail)}</div></details>` : ''}
        </div>` : '';

    const empty = (!d.all || d.all.length === 0);
    const main = empty
        ? `<div class="card" style="text-align:center;padding:40px;color:var(--text-secondary);">
               <div style="font-size:14px;font-weight:600;margin-bottom:6px;">暂时没有可显示的数据</div>
               <div style="font-size:12.5px;">等接口恢复后点「刷新」即可。已抓到的数据会自动落库，下次打开优先用本地的。</div>
           </div>`
        : (ffState.view === 'rank' ? renderFFRank(d) : renderFFHeatShell());

    body.innerHTML = notice + renderFFHeader(d) + main;

    if (!empty && ffState.view === 'heat') renderFFHeat(d);
}

function renderFFHeader(d) {
    // 显示「数据属于哪个交易日」，而不是「我什么时候抓的」。
    // 周末/节假日打接口拿回的是上一交易日的数据，只显示抓取时刻（如「数据 08:52」）
    // 会让人以为是今早的行情 —— 数据日期不是今天时，必须把日期摆出来。
    const today = new Date().toLocaleDateString('sv');   // sv locale 恰好是 YYYY-MM-DD
    let at = '';
    if (d.data_date && d.data_date !== today) {
        at = d.data_date + ' 收盘';
    } else if (d.fetch_at) {
        at = d.fetch_at.slice(11, 16);
    } else if (d.data_date) {
        at = d.data_date;
    }
    return `
    <div class="card" style="padding:12px 16px;margin-bottom:14px;display:flex;align-items:center;gap:14px;flex-wrap:wrap;">
        <div class="tab-btn-group" style="display:flex;gap:6px;">
            <button class="btn ${ffState.kind === 'industry' ? 'btn-primary' : ''}" style="font-size:12.5px;padding:5px 14px;"
                onclick="ffSwitchKind('industry')">行业板块</button>
            <button class="btn ${ffState.kind === 'concept' ? 'btn-primary' : ''}" style="font-size:12.5px;padding:5px 14px;"
                onclick="ffSwitchKind('concept')">概念板块</button>
        </div>
        <div style="width:1px;height:20px;background:var(--border);"></div>
        <div style="display:flex;gap:6px;">
            <button class="btn ${ffState.view === 'rank' ? 'btn-primary' : ''}" style="font-size:12.5px;padding:5px 14px;"
                onclick="ffSwitchView('rank')">榜单</button>
            <button class="btn ${ffState.view === 'heat' ? 'btn-primary' : ''}" style="font-size:12.5px;padding:5px 14px;"
                onclick="ffSwitchView('heat')">热力图</button>
        </div>
        <label style="display:flex;align-items:center;gap:6px;font-size:12.5px;color:var(--text-secondary);cursor:pointer;">
            <input type="checkbox" ${ffState.dedup ? 'checked' : ''} onchange="ffToggleDedup(this)">
            合并同源板块
            <span class="ff-help" onclick="event.preventDefault();ffExplainDedup()">?</span>
        </label>
        <div style="margin-left:auto;display:flex;align-items:center;gap:10px;">
            <span style="font-size:11.5px;color:var(--text-secondary);">${at ? '数据 ' + ffEsc(at) : ''}</span>
            <button class="btn" style="font-size:12.5px;padding:5px 14px;" onclick="ffOpenReplay()"
                title="回放某个交易日的资金流变化">📽 回放</button>
            <button class="btn btn-primary" id="ffRefreshBtn" style="font-size:12.5px;padding:5px 16px;"
                onclick="loadFundFlow(true)">刷新</button>
        </div>
    </div>`;
}

// 双向排行榜：左流出、右流入。用 HTML 条而不是图表库，方便做展开和点击下钻。
function renderFFRank(d) {
    const inflow = (d.inflow || []).slice(0, FF_TOPN);
    const outflow = (d.outflow || []).slice(0, FF_TOPN);
    const max = Math.max(
        ...inflow.map(g => Math.abs(g.main_net)),
        ...outflow.map(g => Math.abs(g.main_net)), 1);

    const col = (list, side) => list.map((g, i) => {
        const w = Math.max(Math.abs(g.main_net) / max * 100, 1.5);
        const cls = side === 'in' ? 'ff-bar-in' : 'ff-bar-out';
        const merged = (g.merged && g.merged.length) ? `
            <div class="ff-merged">
                <span class="ff-merged-t" onclick="ffToggleMerged(this)">
                    含 ${g.merged.length} 个同源板块 ▾<span class="ff-why">${ffEsc(g.reason || '')}</span>
                </span>
                <div class="ff-merged-list" style="display:none;">
                    ${g.merged.map(m => `<div class="ff-merged-row">
                        <span>${ffEsc(m.name)}</span>
                        <span class="${ffCls(m.main_net)}">${ffYi(m.main_net)}</span>
                    </div>`).join('')}
                </div>
            </div>` : '';
        return `
        <div class="ff-row">
            <div class="ff-row-head" onclick="ffOpenDetail('${ffEsc(g.code)}','${ffEsc(g.name)}')">
                <span class="ff-rank">${i + 1}</span>
                <span class="ff-name">${ffEsc(g.name)}</span>
                <span class="ff-chg ${ffCls(g.change_pct)}">${ffPct(g.change_pct)}</span>
                <span class="ff-amt ${ffCls(g.main_net)}">${ffYi(g.main_net)}</span>
            </div>
            <div class="ff-track"><div class="${cls}" style="width:${w}%"></div></div>
            ${merged}
        </div>`;
    }).join('') || '<div style="font-size:12.5px;color:var(--text-secondary);padding:12px;">这一侧没有数据</div>';

    return `
    ${ffTermsCard()}
    <div class="ff-cols">
        <div class="card ff-col">
            <div class="ff-col-t text-profit">主力净流入 TOP${FF_TOPN}</div>
            ${col(inflow, 'in')}
        </div>
        <div class="card ff-col">
            <div class="ff-col-t text-loss">主力净流出 TOP${FF_TOPN}</div>
            ${col(outflow, 'out')}
        </div>
    </div>
    <div style="font-size:11.5px;color:var(--text-secondary);margin-top:10px;line-height:1.7;">
        点任意板块可展开它的分时曲线与历史资金流。共 ${d.all.length} 个板块${ffState.dedup ? '（已合并同源）' : ''}。
    </div>`;
}

// 术语提示：产品稿第二节的强制项。缺了它这张图就是在误导人。
function ffTermsCard() {
    return `
    <div class="card ff-terms">
        <span class="ff-terms-i">ℹ️</span>
        <div>
            <b>「主力净流入」不是真有这么多钱进场。</b>
            每笔成交都有买方和卖方，钱没有净进出。这个数字是把成交按<b>单笔金额</b>分档
            （超大单≥100万、大单20–100万、中单4–20万、小单&lt;4万），
            再按主动买/主动卖方向<b>推断</b>出来的统计指标，衡量「大额成交里主动买比主动卖多多少」。
            主力 = 超大单 + 大单。
            <br>
            它适合用来<b>横向比较板块的相对强弱、观察趋势</b>；不能当成「资金进场了多少」的绝对值，
            也<b>不能和其它软件的数字混用</b>（各家分档阈值不同，同一只股票能差 50%）。
        </div>
    </div>`;
}

function ffToggleMerged(el) {
    const list = el.parentElement.querySelector('.ff-merged-list');
    const open = list.style.display !== 'none';
    list.style.display = open ? 'none' : 'block';
    el.innerHTML = el.innerHTML.replace(open ? '▴' : '▾', open ? '▾' : '▴');
}

function ffExplainDedup() {
    alert(
        '东财把父子层级混在同一张榜里。实测 2026-08-14 行业榜前七名有六个是通信系' +
        '（通信 / 通信设备 / 通信线缆及配套 / 通信网络设备及器件 / 通信服务 / 通信终端及配件），' +
        '同一批钱被反复计入，一个热点就能刷屏整张榜。\n\n' +
        '打开「合并同源板块」后，这类板块会折叠成一行，代表值取其中净额最大的那个' +
        '（不是求和 —— 求和就是重复计算）。被折叠的板块点开就能看到，不会静默丢弃。\n\n' +
        '判定依据有两个：名称同源（一个名字是另一个的前缀），或龙头股相同。'
    );
}

// ── 热力图 ──────────────────────────────────────────────────────────────────
// 面积 = |主力净额|，颜色 = **连续色阶**（红=流入、绿=流出，越深越强）。
//
// 早期版本用的是二值红绿：139 亿和 5 亿一个颜色，等于把热力图最有价值的
// 那条信息通道浪费掉了 —— 面积已经表达了量级，颜色再重复一遍「正负」意义不大。
// 改成连续色阶后，一眼能看出「哪几块是真的重仓在动」。
//
// 色阶上限固定 ±100 亿而不是按当日最大值自适应：**固定刻度才能跨日比较**。
// 自适应的话，平淡的一天和暴动的一天看起来一样红，反而误导。超出的钳到最深。
const FF_HEAT_CAP = 100e8;

// 深色底：热力图靠颜色深浅传信息，浅色底上「浅红/浅绿」和背景混在一起分不出来。
// 只有这一块用深色，其余页面保持全站浅色 —— 金融类产品里图表深色是常见做法。
function ffHeatColor(v) {
    const t = Math.min(Math.abs(v) / FF_HEAT_CAP, 1);
    // 从接近背景的暗色渐到高饱和色，t 开三次方根让中低值之间的差异更容易分辨
    const k = Math.pow(t, 0.55);
    if (v >= 0) {
        // 暗红 #3a1420 → 亮红 #e33f56
        return ffMix([58, 20, 32], [227, 63, 86], k);
    }
    return ffMix([13, 51, 42], [22, 173, 118], k); // 暗绿 → 亮绿
}
function ffMix(a, b, k) {
    const c = a.map((x, i) => Math.round(x + (b[i] - x) * k));
    return `rgb(${c[0]},${c[1]},${c[2]})`;
}

function renderFFHeatShell() {
    // 图例用 HTML 画而不是 ECharts 的 visualMap：要配文字说明，HTML 更好排版。
    // 用真渐变而不是几个色块 —— 色阶本身是连续的，画成分段会让人以为是分档。
    // 取样点密一些，因为 ffHeatColor 里做了非线性映射（pow 0.55），
    // 线性插值两端会和实际色阶对不上。
    const n = 12;
    const stops = Array.from({ length: n + 1 }, (_, i) => {
        const f = 1 - (i / n) * 2;                       // 1 → -1
        return `${ffHeatColor(f * FF_HEAT_CAP)} ${(i / n * 100).toFixed(1)}%`;
    }).join(',');
    return `
    <div class="ff-heatwrap">
        <div class="ff-legend">
            <div class="ff-legend-t">资金净流入（亿元）
                <span class="ff-help" onclick="ffExplainHeat()">?</span></div>
            <div class="ff-legend-body">
                <div class="ff-legend-bar" style="background:linear-gradient(180deg,${stops})"></div>
                <div class="ff-legend-ticks">
                    <span>强 +100亿</span><span>+50亿</span><span>0</span><span>−50亿</span><span>强 −100亿</span>
                </div>
            </div>
            <div class="ff-legend-note">
                <div><b style="color:#e33f56">红色：净流入</b><br>颜色越深，流入越多</div>
                <div style="margin-top:8px"><b style="color:#16ad76">绿色：净流出</b><br>颜色越深，流出越多</div>
                <div class="ff-legend-sub">面积 = 净额绝对值<br>点击任意板块看分时曲线</div>
            </div>
        </div>
        <div id="ffHeat"></div>
    </div>`;
}

function ffExplainHeat() {
    alert(
        '色块面积 = 主力净额的绝对值，颜色深浅 = 净额大小，红入绿出。\n\n' +
        '色阶刻度固定在 ±100 亿，不随当日数据自适应 —— 这样不同日期之间才能比较：' +
        '平淡的一天整体偏暗，资金大进大出的一天整体鲜艳。超过 ±100 亿的钳到最深色。\n\n' +
        '只画净额最大的前 60 个板块：再往后的色块会小到读不出字，画了也没用。'
    );
}

function renderFFHeat(d) {
    const el = document.getElementById('ffHeat');
    if (!el || typeof echarts === 'undefined') {
        if (el) el.innerHTML = '<div style="padding:40px;text-align:center;color:var(--text-secondary);font-size:13px;">' +
            '图表库未能加载（ECharts 走的是 CDN，离线时不可用），热力图暂时看不了。榜单视图不受影响。</div>';
        return;
    }
    const chart = ffChart(el);

    // 只画前 60：长尾在 treemap 里会挤成读不出字的碎块
    const rows = d.all.slice().sort((a, b) => Math.abs(b.main_net) - Math.abs(a.main_net)).slice(0, 60);
    const data = rows.map(g => ({
        name: g.name,
        value: Math.abs(g.main_net),
        itemStyle: { color: ffHeatColor(g.main_net) },
        _net: g.main_net, _code: g.code, _chg: g.change_pct,
        _merged: (g.merged || []).length, _reason: g.reason || '',
    }));

    chart.setOption({
        backgroundColor: 'transparent',
        tooltip: {
            backgroundColor: 'rgba(20,26,34,.95)', borderWidth: 0,
            textStyle: { color: '#e6edf3', fontSize: 12 },
            formatter: p => {
                const x = p.data;
                return `<b>${p.name}</b><br/>主力净额 ${ffYi(x._net)}<br/>涨跌 ${ffPct(x._chg)}` +
                    (x._merged ? `<br/><span style="opacity:.7">含 ${x._merged} 个同源板块</span>` : '');
            },
        },
        series: [{
            type: 'treemap', data, roam: false, nodeClick: false,
            breadcrumb: { show: false }, animationDuration: 300,
            left: 0, top: 0, right: 0, bottom: 0,
            itemStyle: { borderColor: '#0e141b', borderWidth: 2, gapWidth: 2 },
            // 大块给两行（名称 + 数值），小块只留名称，再小就不显示 —— 不然全是省略号
            levels: [{
                itemStyle: { borderColor: '#0e141b', borderWidth: 2, gapWidth: 2 },
                label: { show: true },
            }],
            label: {
                show: true, color: '#fff', fontSize: 12, lineHeight: 17,
                overflow: 'truncate', ellipsis: '…',
                formatter: p => {
                    const w = p.rect ? p.rect.width : 999, h = p.rect ? p.rect.height : 999;
                    if (w < 44 || h < 26) return '';           // 太小，写什么都是省略号
                    if (w < 82 || h < 44) return p.name;       // 只放得下名字
                    return `${p.name}\n${ffYi(p.data._net)}`;
                },
            },
            emphasis: { itemStyle: { borderColor: '#fff', borderWidth: 2 } },
        }],
    }, true);

    chart.off('click');
    chart.on('click', p => { if (p.data && p.data._code) ffOpenDetail(p.data._code, p.name); });
}

// ── 下钻：分时 + 历史 ────────────────────────────────────────────────────────
async function ffOpenDetail(code, name) {
    const modal = document.getElementById('ffDetailModal');
    document.getElementById('ffDetailTitle').textContent = `${name}（${code}）资金流`;
    document.getElementById('ffDetailBody').innerHTML =
        '<div style="padding:40px;text-align:center;color:var(--text-secondary);font-size:13px;">加载中…</div>';
    modal.style.display = 'flex';

    try {
        const [mRes, dRes] = await Promise.all([
            fetch(`/api/fundflow/minute?bk=${encodeURIComponent(code)}`).then(r => r.json()),
            fetch(`/api/fundflow/daily?bk=${encodeURIComponent(code)}&n=120`).then(r => r.json()),
        ]);
        const m = mRes.data || {}, dd = dRes.data || {};
        document.getElementById('ffDetailBody').innerHTML = `
            ${(m.notice || dd.notice) ? `<div style="font-size:12px;color:var(--text-secondary);background:var(--bg-secondary);
                border-radius:8px;padding:8px 12px;margin-bottom:12px;line-height:1.7;">
                ${ffEsc(m.notice || '')}${m.notice && dd.notice ? '<br>' : ''}${ffEsc(dd.notice || '')}</div>` : ''}
            <div style="font-size:13px;font-weight:700;margin-bottom:6px;">当日分时（主力累计净流入）</div>
            <div id="ffMinChart" style="height:230px;margin-bottom:18px;"></div>
            <div style="font-size:13px;font-weight:700;margin-bottom:6px;">历史每日主力净流入（近 ${dd.count || 0} 个交易日）</div>
            <div id="ffDayChart" style="height:230px;"></div>`;

        ffDrawMinute(m.points || []);
        ffDrawDaily(dd.days || []);
    } catch (e) {
        document.getElementById('ffDetailBody').innerHTML =
            `<div style="padding:30px;text-align:center;font-size:13px;color:var(--text-secondary);">加载失败：${ffEsc(e.message)}</div>`;
    }
}

function ffCloseDetail(ev) {
    if (ev && ev.target !== ev.currentTarget) return;
    document.getElementById('ffDetailModal').style.display = 'none';
}

// 取图表实例：优先用 app.js 的 initChart（复用实例 + ResizeObserver 跟随容器）。
// 再补一次下一帧 resize：弹窗刚 display 出来时容器宽度可能还是 0，
// 那样 canvas 会被建成 0 宽 —— 图空白但控制台一声不响，极难查。
function ffChart(el) {
    const chart = (typeof initChart === 'function')
        ? initChart(el)
        : (echarts.getInstanceByDom(el) || echarts.init(el));
    requestAnimationFrame(() => { try { chart.resize(); } catch (e) { /* 实例已销毁，忽略 */ } });
    return chart;
}

function ffNoChartLib(id) {
    const el = document.getElementById(id);
    if (el) el.innerHTML = '<div style="padding:30px;text-align:center;color:var(--text-secondary);font-size:12.5px;">' +
        '图表库未加载（ECharts 走 CDN，离线不可用）</div>';
}

function ffDrawMinute(points) {
    if (typeof echarts === 'undefined') return ffNoChartLib('ffMinChart');
    const el = document.getElementById('ffMinChart');
    if (!el) return;
    if (!points.length) {
        el.innerHTML = '<div style="padding:30px;text-align:center;color:var(--text-secondary);font-size:12.5px;">这一天没有分时数据</div>';
        return;
    }
    const chart = ffChart(el);
    // 用 category 轴而不是时间轴：A 股有 11:30–13:00 午休，时间轴会画出一段
    // 平坦空档，看起来像「这段时间没成交」。category 轴按点排列，天然没有这个问题。
    chart.setOption({
        grid: { left: 58, right: 16, top: 16, bottom: 28 },
        tooltip: {
            trigger: 'axis',
            formatter: p => `${p[0].axisValue}<br/>主力累计 ${ffYi(p[0].data)}`,
        },
        xAxis: {
            type: 'category', data: points.map(p => p.time), boundaryGap: false,
            axisLabel: { fontSize: 10, color: '#6b7590', interval: (i, v) => ['09:31', '10:30', '11:30', '14:00', '15:00'].includes(v) },
            axisLine: { lineStyle: { color: '#eceffa' } },
        },
        yAxis: {
            type: 'value', axisLabel: { fontSize: 10, color: '#6b7590', formatter: v => (v / 1e8).toFixed(0) + '亿' },
            splitLine: { lineStyle: { color: '#eceffa' } },
        },
        series: [{
            type: 'line', data: points.map(p => p.main_net), showSymbol: false, smooth: false,
            lineStyle: { width: 2, color: '#f43f5e' },
            areaStyle: { color: { type: 'linear', x: 0, y: 0, x2: 0, y2: 1,
                colorStops: [{ offset: 0, color: 'rgba(244,63,94,.28)' }, { offset: 1, color: 'rgba(244,63,94,.02)' }] } },
            markLine: { silent: true, symbol: 'none', data: [{ yAxis: 0 }],
                lineStyle: { color: '#6b7590', width: 1 }, label: { show: false } },
        }],
    }, true);
}

function ffDrawDaily(days) {
    if (typeof echarts === 'undefined') return ffNoChartLib('ffDayChart');
    const el = document.getElementById('ffDayChart');
    if (!el) return;
    if (!days.length) {
        el.innerHTML = '<div style="padding:30px;text-align:center;color:var(--text-secondary);font-size:12.5px;">还没有历史数据</div>';
        return;
    }
    const chart = ffChart(el);
    chart.setOption({
        grid: { left: 58, right: 16, top: 16, bottom: 28 },
        tooltip: {
            trigger: 'axis',
            formatter: p => `${p[0].axisValue}<br/>主力净额 ${ffYi(p[0].data)}`,
        },
        xAxis: {
            type: 'category', data: days.map(d => d.date.slice(5)),
            axisLabel: { fontSize: 10, color: '#6b7590', interval: Math.max(Math.floor(days.length / 6), 0) },
            axisLine: { lineStyle: { color: '#eceffa' } },
        },
        yAxis: {
            type: 'value', axisLabel: { fontSize: 10, color: '#6b7590', formatter: v => (v / 1e8).toFixed(0) + '亿' },
            splitLine: { lineStyle: { color: '#eceffa' } },
        },
        series: [{
            type: 'bar', data: days.map(d => ({
                value: d.main_net,
                itemStyle: { color: d.main_net >= 0 ? '#f43f5e' : '#10b981' },
            })),
        }],
    }, true);
}

// ══════════════════════════════════════════════════════════════════════════
// 自动刷新（产品稿 fundflow-replay.html · Q1=B：前端 60 秒 + 后端 TTL 5 分钟）
// ══════════════════════════════════════════════════════════════════════════
//
// 前端轮询打的是**本地后端**，后端有 5 分钟 TTL，所以 60 秒轮询并不会变成
// 60 次/小时的东财请求 —— 东财那边的量只由 TTL 决定。这个解耦是前提，
// 没有它就不能开自动刷新（见 sectorflow.CacheTTL 的注释）。

const FF_POLL_MS = 60 * 1000;
let ffPollTimer = null;

// 交易时段判断（与后端 InTradingSession 同口径）。收盘后数据不再变，
// 继续轮询纯属浪费 —— 虽然打的是本地，但没意义的定时器该停就停。
function ffInTradingSession(d) {
    d = d || new Date();
    const wd = d.getDay();
    if (wd === 0 || wd === 6) return false;
    const m = d.getHours() * 60 + d.getMinutes();
    return (m >= 9 * 60 + 45 && m <= 11 * 60 + 35) || (m >= 13 * 60 && m <= 15 * 60 + 5);
}

function ffStartPolling() {
    ffStopPolling();
    ffPollTimer = setInterval(() => {
        // 三个闸门：页面不可见（切走/最小化）不刷、非交易时段不刷、回放模式不刷。
        // 回放时刷新会把用户正在看的动画打断，那比数据旧几分钟糟糕得多。
        if (document.visibilityState !== 'visible') return;
        if (!ffInTradingSession()) return;
        if (ffReplay.open) return;
        const tab = document.getElementById('tab-fundflow');
        if (!tab || !tab.classList.contains('active')) return;
        ffPoll();
    }, FF_POLL_MS);
}
function ffStopPolling() {
    if (ffPollTimer) { clearInterval(ffPollTimer); ffPollTimer = null; }
}

// ffPoll 静默拉取：不显示 loading、不动按钮文案。
// 自动刷新应该是「你没注意到它发生了」，一闪一闪反而烦人。
async function ffPoll() {
    try {
        const url = `/api/fundflow/rank?kind=${ffState.kind}&dedup=${ffState.dedup ? 1 : 0}`;
        const res = await fetch(url);
        const json = await res.json();
        if (json.status !== 'ok' || !json.data) return;
        const d = json.data;
        // 数据没变就什么都不做 —— TTL 5 分钟、轮询 60 秒，五次里有四次是这种情况
        if (ffState.data && d.fetch_at === ffState.data.fetch_at && d.source === ffState.data.source) return;
        const prev = ffState.data;
        ffState.data = d;
        // 能原地更新就原地更新，避免整块重绘把用户展开的「同源板块」收起来、
        // 或者把他正在看的那一行挪走
        if (!ffPatchRank(prev, d)) renderFundFlow();
    } catch (e) { /* 轮询失败保持现状即可，下一轮再说 */ }
}

// ffPatchRank 原地更新榜单的数值与条宽。
// 返回 false 表示结构变了（板块换了、视图不是榜单），得走整块重绘。
function ffPatchRank(prev, d) {
    if (ffState.view !== 'rank' || !prev) return false;
    const body = document.getElementById('ffBody');
    if (!body || !body.querySelector('.ff-row')) return false;
    // 提示条/日期这类头部信息变了就整体重绘，省得逐个补
    if (!!prev.stale !== !!d.stale || prev.data_date !== d.data_date) return false;

    const inflow = (d.inflow || []).slice(0, FF_TOPN);
    const outflow = (d.outflow || []).slice(0, FF_TOPN);
    const cols = body.querySelectorAll('.ff-col');
    if (cols.length !== 2) return false;

    const max = Math.max(
        ...inflow.map(g => Math.abs(g.main_net)),
        ...outflow.map(g => Math.abs(g.main_net)), 1);

    const patch = (col, list) => {
        const rows = col.querySelectorAll('.ff-row');
        if (rows.length !== list.length) return false;
        for (let i = 0; i < list.length; i++) {
            const g = list[i], row = rows[i];
            // 板块换位了就重绘：原地改名字会让人以为数字在乱跳
            if (row.querySelector('.ff-name').textContent !== g.name) return false;
            row.querySelector('.ff-amt').textContent = ffYi(g.main_net);
            row.querySelector('.ff-amt').className = 'ff-amt ' + ffCls(g.main_net);
            row.querySelector('.ff-chg').textContent = ffPct(g.change_pct);
            row.querySelector('.ff-chg').className = 'ff-chg ' + ffCls(g.change_pct);
            const bar = row.querySelector('.ff-track > div');
            if (bar) bar.style.width = Math.max(Math.abs(g.main_net) / max * 100, 1.5) + '%';
        }
        return true;
    };
    return patch(cols[0], inflow) && patch(cols[1], outflow);
}

// 页面重新可见时补一次，别让用户切回来看到一份五分钟前的数据
document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible' && ffState.data && !ffReplay.open) ffPoll();
});

// ══════════════════════════════════════════════════════════════════════════
// 一天回放（产品稿 fundflow-replay.html）
// Q3 = A+B 都做，共用控制条 · Q4 = 乙 每帧重排
// ══════════════════════════════════════════════════════════════════════════
//
// 数据一次性拿全（/api/fundflow/replay），因为回放要在时间轴上来回拖动，
// 逐帧请求会让拖动变成灾难。50 个板块 × 240 点约 120 KB，一次加载完事。
//
// 关于 Q4「每帧重排」：如实反映当下格局，但色块会一直换位。
// 化解办法是**布局补间** —— ECharts 的 animationDurationUpdate 会让色块
// 平滑移动到新位置而不是瞬间跳，既保留了诚实性又盯得住。

const FF_SPEEDS = [1, 4, 16];        // 1× 约 4 分钟放完，4× 约 1 分钟
const FF_REPLAY_TOP = 15;            // 赛跑榜显示前 N 名（再多看不清）
const FF_HEAT_TOP = 40;              // 热力图画前 N 个

let ffReplay = {
    open: false, loading: false,
    date: '', times: [], sectors: [], dates: [],
    // 默认 4×（产品稿）：1× 放完 240 分钟要 4 分钟，太久；4× 约 1 分钟，
    // 差不多是一杯咖啡的注意力长度。16× 用来快速扫一遍找异动时点。
    frame: 0, playing: false, speed: 4, timer: null,
    view: 'race',                    // race | heat
};

async function ffOpenReplay(date) {
    ffReplay.open = true;
    ffReplay.loading = true;
    document.getElementById('ffBody').innerHTML =
        '<div class="card" style="text-align:center;padding:40px;color:var(--text-secondary);">正在加载回放数据…</div>';
    try {
        const res = await fetch('/api/fundflow/replay' + (date ? '?date=' + encodeURIComponent(date) : ''));
        const json = await res.json();
        if (json.status !== 'ok') throw new Error(json.msg || '加载失败');
        const d = json.data;
        Object.assign(ffReplay, {
            date: d.date || '', times: d.times || [], sectors: d.sectors || [],
            dates: d.available_dates || [], frame: 0, playing: false, notice: d.notice || '',
        });
    } catch (e) {
        ffReplay.times = []; ffReplay.sectors = []; ffReplay.notice = '加载失败：' + e.message;
    } finally {
        ffReplay.loading = false;
        renderFFReplay();
    }
}

function ffCloseReplay() {
    ffStopPlay();
    ffReplay.open = false;
    renderFundFlow();
}

function renderFFReplay() {
    const r = ffReplay;
    const body = document.getElementById('ffBody');
    const dateSel = r.dates.length
        ? `<select onchange="ffOpenReplay(this.value)" style="font-size:12.5px;padding:5px 9px;border-radius:8px;border:1px solid var(--border);background:#fff;">
             ${r.dates.map(x => `<option value="${ffEsc(x)}" ${x === r.date ? 'selected' : ''}>${ffEsc(x)}</option>`).join('')}
           </select>`
        : '<span style="font-size:12px;color:var(--text-secondary);">暂无可回放日期</span>';

    const header = `
    <div class="card" style="padding:12px 16px;margin-bottom:14px;display:flex;align-items:center;gap:14px;flex-wrap:wrap;">
        <b style="font-size:14px;">📽 一天回放</b>
        ${dateSel}
        <div style="width:1px;height:20px;background:var(--border);"></div>
        <div style="display:flex;gap:6px;">
            <button class="btn ${r.view === 'race' ? 'btn-primary' : ''}" style="font-size:12.5px;padding:5px 14px;"
                onclick="ffReplaySetView('race')">曲线赛跑</button>
            <button class="btn ${r.view === 'heat' ? 'btn-primary' : ''}" style="font-size:12.5px;padding:5px 14px;"
                onclick="ffReplaySetView('heat')">热力图</button>
        </div>
        <button class="btn" style="font-size:12.5px;padding:5px 14px;margin-left:auto;" onclick="ffCloseReplay()">← 返回榜单</button>
    </div>`;

    if (!r.times.length) {
        body.innerHTML = header + `
        <div class="card" style="text-align:center;padding:44px;color:var(--text-secondary);">
            <div style="font-size:14px;font-weight:600;margin-bottom:8px;">还没有可回放的数据</div>
            <div style="font-size:12.5px;line-height:1.8;max-width:560px;margin:0 auto;">${ffEsc(r.notice || '')}</div>
        </div>`;
        return;
    }

    body.innerHTML = header + `
        <div class="ff-heatwrap" style="display:block;padding:0;">
            <div id="ffReplayChart" style="height:${r.view === 'race' ? 560 : 600}px;"></div>
        </div>
        ${ffReplayControls()}`;
    ffDrawReplayFrame();
    ffBindReplayKeys();
}

function ffReplaySetView(v) {
    ffReplay.view = v;
    const el = document.getElementById('ffReplayChart');
    if (el && typeof echarts !== 'undefined') {
        const inst = echarts.getInstanceByDom(el);
        if (inst) inst.dispose();   // 两种图 option 结构差太多，复用实例会残留旧配置
    }
    renderFFReplay();
}

function ffReplayControls() {
    const r = ffReplay, last = r.times.length - 1;
    return `
    <div class="card ff-rc">
        <button class="ff-rc-play" onclick="ffTogglePlay()" title="空格">${r.playing ? '⏸' : '▶'}</button>
        <div class="ff-rc-mid">
            <div class="ff-rc-time">
                <b id="ffRcClock">${ffEsc(r.times[r.frame] || '')}</b>
                <span id="ffRcPct">已回放 ${Math.round(r.frame / Math.max(last, 1) * 100)}%</span>
                <span class="ff-rc-hint">空格播放/暂停 · ←→ 逐分钟 · 拖动跳转</span>
            </div>
            <input type="range" id="ffRcRange" min="0" max="${last}" value="${r.frame}"
                   oninput="ffSeek(+this.value)" class="ff-rc-range">
            <div class="ff-rc-ticks">
                <span>${ffEsc(r.times[0] || '')}</span>
                <span>午休不占时长</span>
                <span>${ffEsc(r.times[last] || '')}</span>
            </div>
        </div>
        <div class="ff-rc-speed">
            ${FF_SPEEDS.map(s => `<button class="${s === r.speed ? 'on' : ''}" onclick="ffSetSpeed(${s})">${s}×</button>`).join('')}
            <div class="ff-rc-splabel">播放速度</div>
        </div>
    </div>`;
}

// ── 播放控制 ────────────────────────────────────────────────────────────────
// 1× = 每个分钟点 1 秒；240 点约 4 分钟。用 setInterval 而不是 rAF：
// 我们要的是「每帧推进一个分钟点」，不是按屏幕刷新率插值。
function ffTogglePlay() { ffReplay.playing ? ffStopPlay() : ffStartPlay(); }

function ffStartPlay() {
    const r = ffReplay;
    if (!r.times.length) return;
    if (r.frame >= r.times.length - 1) r.frame = 0;   // 放完了再点就重头
    r.playing = true;
    ffSyncPlayBtn();
    clearInterval(r.timer);

    // 按**墙钟时间**推进，而不是每 tick 固定 +1。
    //
    // 原因：热力图每帧要重排 treemap，渲染本身约 300ms。固定 +1 的话，
    // 渲染慢于 tick 间隔时回调会被浏览器丢弃，表现是「速度调到 16× 却和 4× 一样慢」
    // （实测 16× 下 4 秒只推进 13 帧，而不是 64 帧）。
    // 改成按实际流逝时间算该推进几帧 —— 渲染跟不上就跳帧，
    // 保证「4× 约 1 分钟放完」这个承诺不受渲染成本影响。这也是视频播放器的做法。
    const TICK = Math.max(80, Math.floor(1000 / r.speed));
    // 单次最多跳 8 帧：浏览器把后台标签页的定时器节流到秒级时，elapsed 会变得很大，
    // 不设上限就会一次跳掉半天行情、直接冲到收盘（实测 16× 下 5 秒就放完了全天）。
    const MAX_STEP = 8;
    r.lastTick = performance.now();
    r.timer = setInterval(() => {
        // 页面不可见时暂停而不是追赶 —— 用户切回来该接着看，不是看到已经放完了
        if (document.visibilityState !== 'visible') { r.lastTick = performance.now(); return; }
        const now = performance.now();
        const advance = Math.min(MAX_STEP,
            Math.max(1, Math.round((now - r.lastTick) / (1000 / r.speed))));
        r.lastTick = now;
        if (r.frame >= r.times.length - 1) { ffStopPlay(); return; }
        r.frame = Math.min(r.frame + advance, r.times.length - 1);
        ffDrawReplayFrame();
        ffSyncControls();
    }, TICK);
}
function ffStopPlay() {
    ffReplay.playing = false;
    clearInterval(ffReplay.timer);
    ffReplay.timer = null;
    ffSyncPlayBtn();
}
function ffSetSpeed(s) {
    ffReplay.speed = s;
    document.querySelectorAll('.ff-rc-speed button').forEach(b =>
        b.classList.toggle('on', b.textContent === s + '×'));
    ffDrawReplayFrame();                 // 重画一次让新的动画时长立即生效
    if (ffReplay.playing) ffStartPlay();  // 重建定时器以套用新间隔
}
function ffSeek(i) {
    ffReplay.frame = Math.max(0, Math.min(i, ffReplay.times.length - 1));
    ffDrawReplayFrame();
    ffSyncControls(true);
}
function ffSyncPlayBtn() {
    const b = document.querySelector('.ff-rc-play');
    if (b) b.textContent = ffReplay.playing ? '⏸' : '▶';
}
// 只更新几个文本和滑块，不重建控制条 —— 每秒重建会让滑块拖不动
function ffSyncControls(skipRange) {
    const r = ffReplay, last = Math.max(r.times.length - 1, 1);
    const c = document.getElementById('ffRcClock'); if (c) c.textContent = r.times[r.frame] || '';
    const p = document.getElementById('ffRcPct'); if (p) p.textContent = `已回放 ${Math.round(r.frame / last * 100)}%`;
    if (!skipRange) { const s = document.getElementById('ffRcRange'); if (s) s.value = r.frame; }
}

function ffBindReplayKeys() {
    if (window._ffKeysBound) return;
    window._ffKeysBound = true;
    document.addEventListener('keydown', e => {
        if (!ffReplay.open) return;
        if (/^(INPUT|TEXTAREA|SELECT)$/.test((e.target.tagName || ''))) return;
        if (e.code === 'Space') { e.preventDefault(); ffTogglePlay(); }
        else if (e.code === 'ArrowRight') { e.preventDefault(); ffStopPlay(); ffSeek(ffReplay.frame + 1); }
        else if (e.code === 'ArrowLeft') { e.preventDefault(); ffStopPlay(); ffSeek(ffReplay.frame - 1); }
    });
}

// ── 逐帧渲染 ────────────────────────────────────────────────────────────────
function ffFrameValues() {
    const i = ffReplay.frame;
    return ffReplay.sectors
        .map(s => ({ code: s.code, name: s.name, v: s.values[i] || 0 }))
        .sort((a, b) => b.v - a.v);
}

// ffAnimMs 动画时长必须**短于帧间隔**，否则上一帧的补间还没放完下一帧就来了，
// 动画排队堆积，表现是「播放速度调到 4× 却几乎没变快」（实测 4× 下 3 秒只推进 3 帧）。
// 留 15% 余量给渲染本身。
function ffAnimMs() {
    return Math.max(60, Math.floor(1000 / ffReplay.speed * 0.85));
}

function ffDrawReplayFrame() {
    const el = document.getElementById('ffReplayChart');
    if (!el) return;
    if (typeof echarts === 'undefined') {
        el.innerHTML = '<div style="padding:40px;text-align:center;color:#8b9bab;font-size:13px;">图表库未加载（ECharts 走 CDN，离线不可用）</div>';
        return;
    }
    ffReplay.view === 'race' ? ffDrawRace(el) : ffDrawReplayHeat(el);
}

// ffRacePicks 赛跑视图的**固定行序**，按当日收盘净额排（流入在上、流出在下）。
//
// 初版是每帧按当前值重排的「名次赛跑」。看起来热闹，但 16 行一直互相换位，
// 4×/16× 播放时根本读不出任何一行 —— 眼睛还没找到「通信」在哪，它已经又挪走了。
// 所以和热力图一样锁死行序：**行不动，只有条长在变**。
// 名次变化改由「条长互相超越」来表达，一样看得出反超，但盯得住。
//
// 参演板块也一次选定（收盘流入前 N/2 + 流出前 N/2），
// 不然中途会有板块凭空冒出来又消失，比换位更让人困惑。
function ffRacePicks() {
    const last = ffReplay.times.length - 1;
    if (last < 0) return [];
    const byFinal = ffReplay.sectors
        .map(s => ({ code: s.code, name: s.name, final: s.values[last] || 0 }))
        .sort((a, b) => b.final - a.final);
    if (byFinal.length <= FF_REPLAY_TOP) return byFinal;
    const half = Math.ceil(FF_REPLAY_TOP / 2);
    return byFinal.slice(0, half).concat(byFinal.slice(-half));
}

function ffDrawRace(el) {
    const chart = ffChart(el);
    const i = ffReplay.frame;
    const byCode = {};
    ffReplay.sectors.forEach(s => { byCode[s.code] = s; });

    // 行序固定（升序，因为 ECharts 横向条从下往上画 → 大的在上）
    const picks = ffRacePicks().slice().sort((a, b) => a.final - b.final);
    const cur = picks.map(x => ({
        name: x.name,
        v: (byCode[x.code] && byCode[x.code].values[i]) || 0,
    }));

    chart.setOption({
        backgroundColor: 'transparent',
        grid: { left: 150, right: 92, top: 22, bottom: 22 },
        xAxis: {
            type: 'value',
            axisLabel: { color: '#8b9bab', fontSize: 10, formatter: v => (v / 1e8).toFixed(0) + '亿' },
            splitLine: { lineStyle: { color: '#1e2833' } },
            axisLine: { show: false }, axisTick: { show: false },
        },
        yAxis: {
            type: 'category', data: cur.map(x => x.name),
            axisLabel: { color: '#c4d0dc', fontSize: 12 },
            axisLine: { lineStyle: { color: '#1e2833' } }, axisTick: { show: false },
        },
        series: [{
            type: 'bar', data: cur.map(x => ({
                value: x.v, itemStyle: { color: x.v >= 0 ? '#e33f56' : '#16ad76', borderRadius: 3 },
            })),
            barMaxWidth: 20,
            label: {
                show: true, position: 'right', color: '#e6edf3', fontSize: 11, fontWeight: 700,
                formatter: p => ffYi(p.value),
            },
        }],
        // 行序不变了，只有条长在动 —— 补间只用来让条平滑伸缩
        animationDuration: ffAnimMs(), animationDurationUpdate: ffAnimMs(),
        animationEasing: 'linear', animationEasingUpdate: 'linear',
    });
}

function ffHeatLayoutOrder() {
    const last = ffReplay.times.length - 1;
    if (last < 0) return [];
    return ffReplay.sectors
        .map(s => ({ code: s.code, name: s.name, final: s.values[last] || 0 }))
        .sort((a, b) => Math.abs(b.final) - Math.abs(a.final))
        .slice(0, FF_HEAT_TOP);
}

function ffDrawReplayHeat(el) {
    const chart = ffChart(el);
    const i = ffReplay.frame;
    const byCode = {};
    ffReplay.sectors.forEach(s => { byCode[s.code] = s; });

    // 顺序与面积都锁在收盘值上 —— 位置全程不动
    const rows = ffHeatLayoutOrder().map(x => {
        const cur = (byCode[x.code] && byCode[x.code].values[i]) || 0;
        return { name: x.name, code: x.code, cur, final: x.final };
    });

    chart.setOption({
        backgroundColor: 'transparent',
        tooltip: {
            backgroundColor: 'rgba(20,26,34,.95)', borderWidth: 0,
            textStyle: { color: '#e6edf3', fontSize: 12 },
            formatter: p => `<b>${p.name}</b><br/>${ffReplay.times[ffReplay.frame]} 累计 ${ffYi(p.data._cur)}` +
                `<br/><span style="opacity:.65">收盘 ${ffYi(p.data._final)}</span>`,
        },
        series: [{
            type: 'treemap', roam: false, nodeClick: false, breadcrumb: { show: false },
            left: 0, top: 0, right: 0, bottom: 0,
            data: rows.map(x => ({
                name: x.name,
                value: Math.abs(x.final) || 1,   // 面积锁定收盘值 → 布局不变
                itemStyle: { color: ffHeatColor(x.cur) },  // 颜色随当前时刻变
                _cur: x.cur, _final: x.final,
            })),
            itemStyle: { borderColor: '#0e141b', borderWidth: 2, gapWidth: 2 },
            label: {
                show: true, color: '#fff', fontSize: 12, lineHeight: 16,
                overflow: 'truncate', ellipsis: '…',
                formatter: p => {
                    const w = p.rect ? p.rect.width : 999, h = p.rect ? p.rect.height : 999;
                    if (w < 44 || h < 26) return '';
                    if (w < 82 || h < 44) return p.name;
                    return `${p.name}\n${ffYi(p.data._cur)}`;
                },
            },
        }],
        // 布局不变，只有颜色在动 —— 补间短一点就够，长了反而拖慢高速播放
        animationDurationUpdate: Math.min(ffAnimMs(), 200), animationEasingUpdate: 'linear',
    });
}

