// === AI 分析入口（统一组件）===
//
// 产品稿 docs/product-specs/ai-workflow.html 定案：
//   Q1 = 就地强化：入口留在各页原位，但四处共用同一个说明与弹窗
//   Q3 = 只复制，不给任何外链 —— 本文件不出现任何 <a href> 或 window.open
//   Q4 = 每个入口旁常驻一句话说明
//
// 原来的问题：四个入口措辞各不相同（「AI持仓解读」/「导出 Claude 提示词」/
// 「查看赛道分析」），点了之后不知道发生了什么。现在统一成
// 「一句话说明 + 一个按钮」，点开都是同一个弹窗，弹窗里说清三件事。

const AI_PROMPTS = {
    portfolio: {
        title: '持仓体检',
        entryHint: '把你的持仓结构整理成一段提示词，复制后粘给 AI，得到「这个组合有什么问题、该怎么调」的分析。',
        url: '/api/ai/context',
        sends: '资产配置比例、逐只持仓的占比与收益率、穿透后的行业暴露、你的风险偏好',
        gets: '组合最突出的问题（按严重程度排序）、配置与风险偏好是否匹配、有没有隐藏的过度集中、下次该补哪个方向',
    },
    selector: {
        title: '配置缺口分析',
        entryHint: '把这次诊断出的配置缺口和你的问卷答案整理成提示词，复制后粘给 AI，帮你判断哪些缺口值得补。',
        url: null, // 需要 session_id，调用时拼
        sends: '19 个行业缺口、你的 4 个问卷答案、当前的分类配置比例',
        gets: '哪 2-3 个缺口真正值得补、该用什么类型的基金、分几次买入、有什么风险没体现在清单里',
    },
    lookthrough: {
        title: '持仓穿透分析',
        entryHint: '把穿透到底层股票的结果整理成提示词，复制后粘给 AI，帮你看清「买了一堆基金是否其实买了同一批股票」。',
        url: '/api/holdings/ai-prompt',
        sends: '等效独立基金数、跨基金重叠持股清单、穿透后的行业分布',
        gets: '分散度够不够、重叠持股有没有需要警惕的、这个组合实际押注的是什么、想提高分散度该怎么调',
    },
    theme: {
        title: '赛道分析',
        entryHint: '把这只基金的季报持股、同类对比、市场估值和择时信号整理成提示词，复制后粘给 AI 做赛道判断。',
        url: null, // 需要基金代码
        sends: '基金基本信息、前十大持股与行业、同主题基金对比、沪深300/中证500 的 PE 百分位、三层择时信号',
        gets: '这个赛道处于什么周期位置、进场的先决条件、同类里选哪只、可能的失败情景',
    },
};

let _aiPromptText = '';

// renderEntryHint 生成入口处的常驻说明条（Q4 甲）。
// 在各页调用：容器 id + 类型 + 按钮点击的函数名。
function renderAIEntryHint(containerId, kind, onclickExpr) {
    const cfg = AI_PROMPTS[kind];
    const el = document.getElementById(containerId);
    if (!el || !cfg) return;
    el.innerHTML = `
        <div style="display:flex;align-items:flex-start;justify-content:space-between;gap:16px;flex-wrap:wrap;
                    background:var(--primary-soft, #eef0fd);border-radius:var(--radius, 14px);padding:13px 16px;">
            <div style="flex:1;min-width:240px;font-size:13px;line-height:1.8;color:var(--text-secondary);">
                <strong style="color:var(--text);">让 AI 分析：${cfg.title}</strong><br>
                ${cfg.entryHint}
            </div>
            <button class="btn btn-primary" onclick="${onclickExpr}" style="white-space:nowrap;">预览并复制</button>
        </div>`;
}

// openAIPromptModal 打开统一的预览弹窗
async function openAIPromptModal(kind, extra) {
    const cfg = AI_PROMPTS[kind];
    if (!cfg) return;

    const modal = document.getElementById('aiPromptModal');
    modal.style.display = 'flex';
    _aiPromptText = '';

    document.getElementById('aiPromptTitle').textContent = `让 AI 分析：${cfg.title}`;
    document.getElementById('aiPromptSends').textContent = cfg.sends;
    document.getElementById('aiPromptGets').textContent = cfg.gets;
    document.getElementById('aiPromptBody').value = '生成中…';
    document.getElementById('aiPromptCopyBtn').disabled = true;
    document.getElementById('aiPromptCopyBtn').textContent = '复制提示词';
    document.getElementById('aiPromptMeta').textContent = '';

    let url = cfg.url;
    if (kind === 'selector') url = `/api/selector/export/${extra}`;
    if (kind === 'theme') url = `/api/funds/${extra}/ai-prompts/theme-analysis`;

    try {
        const res = await fetch(url);
        if (!res.ok) {
            let msg = '生成失败';
            try { const j = await res.json(); msg = j.msg || msg; } catch (_) {}
            throw new Error(msg);
        }
        // selector 的导出接口返回 markdown 文本，其余返回 JSON
        const ct = res.headers.get('content-type') || '';
        if (ct.includes('json')) {
            const j = await res.json();
            _aiPromptText = (j.data && (j.data.prompt || j.data.context)) || '';
        } else {
            _aiPromptText = await res.text();
        }

        document.getElementById('aiPromptBody').value = _aiPromptText;
        document.getElementById('aiPromptCopyBtn').disabled = false;
        document.getElementById('aiPromptMeta').textContent =
            `约 ${_aiPromptText.length.toLocaleString()} 字 · 不含持仓金额与账户信息`;
    } catch (e) {
        document.getElementById('aiPromptBody').value = `生成失败：${e.message}`;
    }
}

async function copyAIPrompt() {
    if (!_aiPromptText) return;
    const btn = document.getElementById('aiPromptCopyBtn');
    try {
        await navigator.clipboard.writeText(_aiPromptText);
        btn.textContent = '✓ 已复制';
    } catch (_) {
        // 剪贴板不可用时退回选中文本，让用户自己 Cmd+C
        const ta = document.getElementById('aiPromptBody');
        ta.removeAttribute('readonly');
        ta.select();
        try { document.execCommand('copy'); btn.textContent = '✓ 已复制'; }
        catch (e) { btn.textContent = '复制失败，请手动全选'; }
        ta.setAttribute('readonly', 'readonly');
    }
    setTimeout(() => { btn.textContent = '复制提示词'; }, 2500);
}

function closeAIPromptModal(event) {
    if (!event || event.target.id === 'aiPromptModal') {
        document.getElementById('aiPromptModal').style.display = 'none';
    }
}

// 各页的入口函数（保持简短，方便写在 onclick 里）
function openPortfolioPrompt() { openAIPromptModal('portfolio'); }
function openLookThroughPrompt() { openAIPromptModal('lookthrough'); }
function openSelectorPrompt(sessionId) {
    if (!sessionId) { alert('请先点「生成选基推荐」'); return; }
    openAIPromptModal('selector', sessionId);
}
function openThemePrompt(code) { openAIPromptModal('theme', code); }
