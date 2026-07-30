/* 可复制的报错信息。
 *
 * 用户报障时最没用的一句是「提交卡住了」—— 没有代码、没有时间、没有原因，
 * 只能靠猜。这个模块的作用是：出错时当场生成一段结构化文本，用户点一下
 * 复制、直接发给作者，作者不用再来回追问。
 *
 * 三条原则：
 *  1. 只放排查用得上的：版本、时间、操作、涉及基金、错误原文、耗时。
 *  2. 不放任何金额和账户信息 —— 报障不该顺带把资产发出去。
 *  3. 用户看得懂上半部分（怎么办），看不懂下半部分也没关系（那是给作者的）。
 */
(function () {
    'use strict';

    let cachedVersion = null;

    async function appVersion() {
        if (cachedVersion) return cachedVersion;
        try {
            const r = await fetch('/api/system/version');
            const b = await r.json();
            const d = b.data || b;
            cachedVersion = d.version || d.current || 'unknown';
        } catch (e) {
            cachedVersion = 'unknown';
        }
        return cachedVersion;
    }

    function esc(s) {
        return String(s == null ? '' : s)
            .replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
    }

    /* buildReport 拼出给作者看的那段文本。
     * ctx: { action, codes, error, httpStatus, elapsedMs, detail }
     */
    async function buildReport(ctx) {
        const lines = [
            '【散户 sanhu · 问题反馈】',
            `版本：${await appVersion()}`,
            `时间：${new Date().toLocaleString('zh-CN')}`,
            `操作：${ctx.action || '未知'}`,
        ];
        if (ctx.codes && ctx.codes.length) lines.push(`涉及基金：${ctx.codes.join('、')}`);
        if (ctx.httpStatus) lines.push(`HTTP 状态：${ctx.httpStatus}`);
        if (typeof ctx.elapsedMs === 'number') lines.push(`耗时：${(ctx.elapsedMs / 1000).toFixed(1)} 秒`);
        lines.push(`错误：${ctx.error || '（无）'}`);
        if (ctx.detail) lines.push('', '明细：', ctx.detail);
        lines.push('', `环境：${navigator.platform || '未知'} · ${navigator.userAgent.slice(0, 120)}`);
        lines.push(`网络：${navigator.onLine ? '在线' : '离线'}`);
        return lines.join('\n');
    }

    /* showErrorBox 在指定容器里渲染报错面板。
     * human：给用户看的人话（发生了什么、现在该怎么办）
     * ctx：拼诊断文本用
     */
    async function showErrorBox(containerId, human, ctx) {
        const box = document.getElementById(containerId);
        if (!box) return;
        const report = await buildReport(ctx);

        box.style.display = 'block';
        box.innerHTML = `
            <div class="err-title">⚠️ ${esc(human.title)}</div>
            <div class="err-msg">${human.body}</div>
            <details class="err-detail">
                <summary>展开报错信息（发给作者用）</summary>
                <textarea class="err-text" readonly>${esc(report)}</textarea>
            </details>
            <div class="err-actions">
                <button class="btn btn-primary err-copy">📋 复制报错信息</button>
                <span class="err-hint">复制后发给作者即可，里面<b>不含金额和账户信息</b></span>
            </div>`;

        box.querySelector('.err-copy').onclick = async function () {
            const btn = this;
            try {
                await navigator.clipboard.writeText(report);
            } catch (e) {
                const ta = box.querySelector('.err-text');
                ta.select();
                document.execCommand('copy');
                window.getSelection().removeAllRanges();
            }
            btn.textContent = '✓ 已复制';
            setTimeout(() => { btn.textContent = '📋 复制报错信息'; }, 2500);
        };
    }

    function hideErrorBox(containerId) {
        const box = document.getElementById(containerId);
        if (box) { box.style.display = 'none'; box.innerHTML = ''; }
    }

    /* fetchJSON 带超时的请求。
     * 裸 fetch 没有超时 —— 服务端卡住时前端会永远等下去，用户看到的就是
     * 按钮一直停在「提交中…」，这正是本轮要修的那个问题。
     */
    async function fetchJSON(url, options, timeoutMs) {
        const started = Date.now();
        const ctrl = new AbortController();
        const timer = setTimeout(() => ctrl.abort(), timeoutMs || 20000);
        try {
            const resp = await fetch(url, Object.assign({}, options, { signal: ctrl.signal }));
            const body = await resp.json().catch(() => ({}));
            if (!resp.ok) {
                const err = new Error(body.message || body.msg || `服务返回 ${resp.status}`);
                err.httpStatus = resp.status;
                err.elapsedMs = Date.now() - started;
                throw err;
            }
            return body.data !== undefined ? body.data : body;
        } catch (e) {
            if (e.name === 'AbortError') {
                const err = new Error(`请求超过 ${(timeoutMs || 20000) / 1000} 秒没有响应`);
                err.timedOut = true;
                err.elapsedMs = Date.now() - started;
                throw err;
            }
            if (!e.elapsedMs) e.elapsedMs = Date.now() - started;
            throw e;
        } finally {
            clearTimeout(timer);
        }
    }

    window.ErrReport = { show: showErrorBox, hide: hideErrorBox, fetchJSON, buildReport };
})();
