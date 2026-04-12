// === 截图导入模块 ===

let _importRows = [];
let _importType = 'holdings';
let _importFile = null;

// ── 开启弹窗 ───────────────────────────────────────────────────
function openImportModal() {
    _importRows = [];
    _importFile = null;
    _importType = 'holdings';

    // 重置 Step 1
    document.getElementById('importStep1').style.display = 'block';
    document.getElementById('importStep2').style.display = 'none';
    document.getElementById('importPreview').style.display = 'none';
    document.getElementById('importParseError').style.display = 'none';
    document.getElementById('importParseBtn').disabled = true;
    document.getElementById('importFileInput').value = '';
    document.getElementById('importPreviewImg').src = '';
    document.querySelectorAll('input[name="importType"]').forEach(r => {
        r.checked = r.value === 'holdings';
    });

    document.getElementById('importModal').style.display = 'flex';
}

function closeImportModal(event) {
    if (!event || event.target === document.getElementById('importModal')) {
        document.getElementById('importModal').style.display = 'none';
    }
}

// ── 文件选择 ───────────────────────────────────────────────────
function onImportFileSelect(file) {
    if (!file) return;
    _importFile = file;

    const reader = new FileReader();
    reader.onload = e => {
        document.getElementById('importPreviewImg').src = e.target.result;
        document.getElementById('importPreview').style.display = 'block';
    };
    reader.readAsDataURL(file);

    document.getElementById('importParseBtn').disabled = false;
    document.getElementById('importParseError').style.display = 'none';
}

function _setupDropZone() {
    const zone = document.getElementById('importDropZone');
    const input = document.getElementById('importFileInput');

    zone.onclick = () => input.click();
    input.onchange = e => onImportFileSelect(e.target.files[0]);

    zone.ondragover = e => { e.preventDefault(); zone.style.borderColor = 'var(--primary)'; };
    zone.ondragleave = () => { zone.style.borderColor = 'var(--border)'; };
    zone.ondrop = e => {
        e.preventDefault();
        zone.style.borderColor = 'var(--border)';
        const file = e.dataTransfer.files[0];
        if (file) onImportFileSelect(file);
    };
}
// 页面加载后初始化拖拽区（等 DOM ready）
document.addEventListener('DOMContentLoaded', _setupDropZone);

// ── 解析截图 ────────────────────────────────────────────────────
async function parseImport() {
    if (!_importFile) return;

    _importType = document.querySelector('input[name="importType"]:checked').value;

    const btn = document.getElementById('importParseBtn');
    const errEl = document.getElementById('importParseError');
    const loading = document.getElementById('importParseLoading');

    btn.disabled = true;
    errEl.style.display = 'none';
    if (loading) loading.style.display = 'flex';

    try {
        const fd = new FormData();
        fd.append('file', _importFile);
        fd.append('import_type', _importType);

        const result = await API.parseImport(fd);

        if (loading) loading.style.display = 'none';

        if (result.error) {
            errEl.textContent = result.error;
            errEl.style.display = 'block';
            btn.disabled = false;
            return;
        }

        _importRows = result.rows;
        _showImportStep2();

    } catch (e) {
        if (loading) loading.style.display = 'none';
        errEl.textContent = '网络错误，请重试：' + e.message;
        errEl.style.display = 'block';
        btn.disabled = false;
    }
}

// ── Step 2：核对表格 ────────────────────────────────────────────
function _showImportStep2() {
    document.getElementById('importStep1').style.display = 'none';
    document.getElementById('importStep2').style.display = 'block';

    const title = document.getElementById('importStep2Title');
    title.textContent = _importType === 'holdings'
        ? `持仓数据核对（共 ${_importRows.length} 条）`
        : `交易记录核对（共 ${_importRows.length} 条）`;

    // 持仓模式显示覆盖警告
    const warning = document.getElementById('importReplaceWarning');
    if (warning) warning.style.display = _importType === 'holdings' ? 'block' : 'none';

    const commitBtn = document.getElementById('importCommitBtn');
    commitBtn.disabled = _importType === 'holdings';  // 持仓需勾选确认

    document.getElementById('importCommitError').style.display = 'none';

    _renderImportReviewTable();
}

function _renderImportReviewTable() {
    const table = document.getElementById('importReviewTable');
    const isHoldings = _importType === 'holdings';

    const thead = isHoldings
        ? '<tr><th></th><th>基金匹配</th><th>持有份额</th><th>持有金额(元)</th><th>持有收益(元)</th><th>置信</th></tr>'
        : '<tr><th></th><th>基金匹配</th><th>类型</th><th>日期</th><th>金额(元)</th><th>份额</th><th>净值</th><th>置信</th></tr>';

    const rowsHtml = _importRows.map((row, i) => {
        const conf = row.confidence;
        const rowStyle = conf === 'low' ? 'border-left:3px solid #ef4444;' : conf === 'medium' ? 'border-left:3px solid #f59e0b;' : '';
        const confBadge = conf === 'high'
            ? '<span style="color:#22c55e;">●</span>'
            : conf === 'medium'
            ? '<span style="color:#f59e0b;">●</span>'
            : '<span style="color:#ef4444;">●</span>';

        // 基金匹配下拉
        const matches = row.matches || [];
        const selectOptions = matches.length
            ? matches.map((m, mi) => `<option value="${m.code}" ${mi === 0 ? 'selected' : ''}>${m.name}(${m.code}) ${Math.round(m.score * 100)}%</option>`).join('')
            : '<option value="">— 请先在系统中添加此基金 —</option>';
        const selectStyle = conf === 'low' ? 'border-color:#ef4444;' : '';
        const fundSelect = `<select id="importFund_${i}" style="font-size:12px;padding:2px 4px;max-width:200px;${selectStyle}">${selectOptions}</select>`;

        if (isHoldings) {
            return `<tr style="${rowStyle}">
                <td><button onclick="deleteImportRow(${i})" style="background:none;border:none;color:#ef4444;cursor:pointer;font-size:14px;">✕</button></td>
                <td>${fundSelect}</td>
                <td><input type="number" id="importShares_${i}" value="${row.shares}" style="width:100px;font-size:12px;" step="0.01"></td>
                <td><input type="number" id="importAmount_${i}" value="${row.amount}" style="width:100px;font-size:12px;" step="0.01"></td>
                <td><input type="number" id="importProfit_${i}" value="${row.profit}" style="width:90px;font-size:12px;" step="0.01"></td>
                <td title="${row.raw_name || ''}">${confBadge}</td>
            </tr>`;
        } else {
            const typeSelect = `<select id="importTxType_${i}" style="font-size:12px;padding:2px 4px;">
                <option value="buy" ${row.tx_type === 'buy' ? 'selected' : ''}>买入</option>
                <option value="sell" ${row.tx_type === 'sell' ? 'selected' : ''}>卖出</option>
            </select>`;
            return `<tr style="${rowStyle}">
                <td><button onclick="deleteImportRow(${i})" style="background:none;border:none;color:#ef4444;cursor:pointer;font-size:14px;">✕</button></td>
                <td>${fundSelect}</td>
                <td>${typeSelect}</td>
                <td><input type="text" id="importDate_${i}" value="${row.date}" style="width:90px;font-size:12px;" placeholder="YYYY-MM-DD"></td>
                <td><input type="number" id="importAmt_${i}" value="${row.amount}" style="width:90px;font-size:12px;" step="0.01"></td>
                <td><input type="number" id="importShrs_${i}" value="${row.shares}" style="width:80px;font-size:12px;" step="0.01"></td>
                <td><input type="number" id="importNav_${i}" value="${row.nav}" style="width:70px;font-size:12px;" step="0.0001"></td>
                <td title="${row.raw_name || ''}">${confBadge}</td>
            </tr>`;
        }
    }).join('');

    table.innerHTML = `<thead style="background:var(--bg-secondary);font-size:12px;color:var(--text-secondary);">${thead}</thead>
    <tbody>${rowsHtml}</tbody>`;
}

function deleteImportRow(i) {
    _importRows.splice(i, 1);
    _renderImportReviewTable();
}

function updateCommitBtn() {
    if (_importType !== 'holdings') return;
    const cb = document.getElementById('importReplaceConfirm');
    document.getElementById('importCommitBtn').disabled = !cb?.checked;
}

function goBackToImportStep1() {
    document.getElementById('importStep2').style.display = 'none';
    document.getElementById('importStep1').style.display = 'block';
    document.getElementById('importParseBtn').disabled = false;
    document.getElementById('importParseError').style.display = 'none';
}

// ── 提交导入 ────────────────────────────────────────────────────
async function commitImport() {
    const errEl = document.getElementById('importCommitError');
    errEl.style.display = 'none';

    // 从表格读取当前值
    const rows = _buildCommitRows();
    if (!rows) return; // 验证失败，错误已显示

    const commitBtn = document.getElementById('importCommitBtn');
    commitBtn.disabled = true;
    commitBtn.textContent = '导入中...';

    try {
        const result = await API.commitImport({ import_type: _importType, rows });

        if (result.status === 'error') {
            errEl.textContent = result.message || '导入失败';
            errEl.style.display = 'block';
            commitBtn.disabled = false;
            commitBtn.textContent = '确认导入';
            return;
        }

        // 成功
        closeImportModal(null);
        alert(`导入成功：${result.imported} 条${result.skipped > 0 ? `，跳过 ${result.skipped} 条（基金未在系统中）` : ''}`);

        // 刷新持仓数据
        if (typeof loadDashboard === 'function') {
            loadDashboard();
        }

    } catch (e) {
        errEl.textContent = '网络错误：' + e.message;
        errEl.style.display = 'block';
        commitBtn.disabled = false;
        commitBtn.textContent = '确认导入';
    }
}

function _buildCommitRows() {
    const errEl = document.getElementById('importCommitError');
    const rows = [];
    const isHoldings = _importType === 'holdings';

    for (let i = 0; i < _importRows.length; i++) {
        const code = document.getElementById(`importFund_${i}`)?.value?.trim();
        if (!code) {
            errEl.textContent = `第 ${i + 1} 行未选择基金`;
            errEl.style.display = 'block';
            return null;
        }

        if (isHoldings) {
            const shares = parseFloat(document.getElementById(`importShares_${i}`)?.value || 0);
            const amount = parseFloat(document.getElementById(`importAmount_${i}`)?.value || 0);
            const profit = parseFloat(document.getElementById(`importProfit_${i}`)?.value || 0);
            rows.push({
                code,
                shares,
                cost_amount: parseFloat((amount - profit).toFixed(4)),
                buy_date: '',
            });
        } else {
            const tx_type = document.getElementById(`importTxType_${i}`)?.value;
            const date = document.getElementById(`importDate_${i}`)?.value?.trim();
            const amount = parseFloat(document.getElementById(`importAmt_${i}`)?.value || 0);
            const shares = parseFloat(document.getElementById(`importShrs_${i}`)?.value || 0);
            const nav = parseFloat(document.getElementById(`importNav_${i}`)?.value || 0);

            if (!date || !/^\d{4}-\d{2}-\d{2}$/.test(date)) {
                errEl.textContent = `第 ${i + 1} 行日期格式错误，请填写 YYYY-MM-DD`;
                errEl.style.display = 'block';
                return null;
            }
            rows.push({ code, tx_type, date, amount, nav, fee: 0 });
        }
    }

    if (rows.length === 0) {
        errEl.textContent = '没有可导入的数据行';
        errEl.style.display = 'block';
        return null;
    }

    return rows;
}
