"""支付宝基金截图 OCR 导入模块"""
from __future__ import annotations

import difflib
import io
import re
from typing import Optional

from backend.database import get_connection

# ── OCR 懒加载单例 ────────────────────────────────────────────────
_reader = None


def get_reader():
    global _reader
    if _reader is None:
        import easyocr
        _reader = easyocr.Reader(['ch_sim', 'en'], gpu=False, verbose=False)
    return _reader


def ocr_image(image_bytes: bytes) -> list[str]:
    """运行 EasyOCR，返回按 y 坐标排序的文本行列表"""
    import numpy as np
    from PIL import Image
    img = Image.open(io.BytesIO(image_bytes)).convert('RGB')
    if img.width > 1200:
        ratio = 1200 / img.width
        img = img.resize((1200, int(img.height * ratio)))

    reader = get_reader()
    results = reader.readtext(np.array(img))

    if not results:
        return []

    def y_center(item):
        bbox = item[0]
        return (bbox[0][1] + bbox[2][1]) / 2

    def x_center(item):
        bbox = item[0]
        return (bbox[0][0] + bbox[2][0]) / 2

    results.sort(key=y_center)

    lines = []
    current_line = [results[0]]
    for item in results[1:]:
        if abs(y_center(item) - y_center(current_line[0])) < 15:
            current_line.append(item)
        else:
            current_line.sort(key=x_center)
            lines.append(' '.join(r[1] for r in current_line))
            current_line = [item]
    if current_line:
        current_line.sort(key=x_center)
        lines.append(' '.join(r[1] for r in current_line))

    return lines


_HOLDINGS_RE = re.compile(
    r'持有份额[：:\s]*([\d,，]+\.?\d*)'
    r'.*?持有(市值|金额)[：:\s]*([\d,，]+\.?\d*)元'
    r'.*?持有收益[：:\s]*([+\-＋－]?[\d,，]+\.?\d*)元',
    re.DOTALL,
)

# 支付宝"持有"列表视图：基金名 / 金额 / 昨日收益 / 持有收益率
# 例：易方达中债3-5年国开行债券指数A / 198,680.99 / 0.00 / +2.27%
_ALIPAY_LIST_AMOUNT_RE = re.compile(r'^([\d,，]+\.\d{2})$')
_ALIPAY_LIST_RETURN_RE = re.compile(r'^([+\-＋－][\d,，]+\.\d{2})$')  # 昨日收益（绝对值）
_ALIPAY_LIST_PCT_RE    = re.compile(r'^([+\-＋－]?\d+\.\d+)%$')       # 持有收益率
_DATE_RE = re.compile(
    r'(\d{4}[-年]\d{1,2}[-月]\d{1,2}日?)\s*(买入成功|卖出成功|买入|卖出|赎回成功|赎回)'
)
_TX_RE = re.compile(
    r'(?:金额|成交金额)[：:\s]*([\d,，]+\.?\d*)元'
    r'.*?(?:份额|成交份额)[：:\s]*([\d,，]+\.?\d*)份'
    r'.*?(?:净值|成交净值)[：:\s]*([\d.]+)元',
    re.DOTALL,
)


def _clean_num(s: str) -> Optional[float]:
    """解析数字，兼容 OCR 常见错误：
    - 千位分隔符为句点（153.207.41 → 153207.41）
    - 小数点为逗号（15,046,72 → 15046.72）
    - 负号为波浪线（~953.28 → -953.28）
    """
    if not s:
        return None
    try:
        s = s.strip().replace('，', '').replace('＋', '+').replace('－', '-').replace('~', '-')
        dots = s.count('.')
        commas = s.count(',')
        sign = ''
        if s and s[0] in '+-':
            sign = s[0]
            s = s[1:]
        if dots >= 2:
            # 句点作千位分隔符：153.207.41 → 153207.41
            last = s.rfind('.')
            s = s[:last].replace('.', '') + '.' + s[last+1:]
        elif dots == 0 and commas >= 2:
            # 全用逗号：15,046,72 → 15046.72（最后一个逗号是小数点）
            last = s.rfind(',')
            s = s[:last].replace(',', '') + '.' + s[last+1:]
        elif dots == 1 and commas >= 1:
            # 标准格式 153,207.41
            s = s.replace(',', '')
        elif dots == 0 and commas == 1:
            # 逗号作小数点：2,190 → 2.190（少见，但 OCR 会出现）
            # 只有当逗号后面恰好 2 位时才视为小数点
            parts = s.split(',')
            if len(parts[1]) == 2:
                s = parts[0] + '.' + parts[1]
            else:
                s = s.replace(',', '')
        else:
            s = s.replace(',', '')
        return float(sign + s)
    except (ValueError, AttributeError):
        return None


def _normalize_date(raw: str) -> str:
    cleaned = re.sub(r'[年月]', '-', raw).rstrip('日')
    parts = cleaned.split('-')
    if len(parts) == 3:
        return f"{parts[0]}-{parts[1].zfill(2)}-{parts[2].zfill(2)}"
    return cleaned


def _is_fund_name(s: str) -> bool:
    """判断一行文字是否像基金名称（含基金名后缀如"债券A"、"合C"）"""
    if not s or len(s) < 2:
        return False
    if not re.search(r'[\u4e00-\u9fff]', s):
        return False
    if re.fullmatch(r'[\d.,%+\-\s元份额市值收益昨日持有排序全部偏股偏债指数黄金]+', s):
        return False
    # 过滤支付宝 UI 控件文字
    skip = {'收益明细', '持仓分析', '交易记录', '投资计划', '清仓分析', '反馈与投诉',
            '我的持有', '金额排序', '更多产品', '去市场看看', '热销基金', '去看看',
            '基金销售服务', '资金安全有保障', '我的总资产', '市场解读',
            '名称', '金额', '昨日收益', '持有收益', '持有收益率'}
    if s in skip:
        return False
    # "产品提醒 xxx"、"市场解读 xxx" 这类插屏广告行
    if re.match(r'^(产品提醒|市场解读|热销基金|反馈|纯债|超额收益|\[)', s):
        return False
    # OCR 噪音：含"[="、"囚"等乱码特征
    if re.search(r'\[=|囚|吕', s):
        return False
    if s in skip:
        return False
    # 含"/"的表头行（如"金额/昨日收益"、"持有收益/率"）
    if re.search(r'[\u4e00-\u9fff]+/[\u4e00-\u9fff]+', s):
        return False
    # 含括号的说明行（如"昨日收益（元）"）
    if re.search(r'[\u4e00-\u9fff]+[（(]元[）)]', s):
        return False
    return True


def parse_holdings_screenshot(lines: list[str]) -> list[dict]:
    """
    支持两种支付宝截图格式：
    A. 详情页：含"持有份额/持有市值/持有收益"关键词
    B. 列表页（持有Tab）：基金名 → 金额 → 昨日收益 → 持有收益率（逐行）
    """
    rows = []

    # ── 格式 A：详情页 ─────────────────────────────────────────────────────
    i = 0
    detail_found = False
    while i < len(lines):
        line = lines[i]
        combined = line
        if i + 1 < len(lines):
            combined = line + ' ' + lines[i + 1]

        m = _HOLDINGS_RE.search(combined)
        if m:
            detail_found = True
            shares = _clean_num(m.group(1))
            amount = _clean_num(m.group(3))
            profit_str = m.group(4).replace('＋', '+').replace('－', '-')
            profit = _clean_num(profit_str)

            raw_name = ''
            for j in range(i - 1, max(i - 5, -1), -1):
                candidate = lines[j].strip()
                if _is_fund_name(candidate):
                    raw_name = candidate
                    break

            if shares is not None or amount is not None:
                filled = sum(v is not None for v in [shares, amount, profit])
                confidence = 'high' if filled == 3 else ('medium' if filled == 2 else 'low')
                rows.append({
                    'raw_name': raw_name,
                    'shares': shares or 0.0,
                    'amount': amount or 0.0,
                    'profit': profit or 0.0,
                    'confidence': confidence,
                })
            i += 2
        else:
            i += 1

    if detail_found:
        return rows

    # ── 格式 B：支付宝列表页（持有Tab）──────────────────────────────────────
    # 实际 OCR 格式（每只基金占两行）：
    # 行1: "基金名前半 金额 昨日收益"  （金额和昨日收益和名字在同一行）
    # 行2: "基金名后半 0.00 收益率%"
    # 例：
    #   '易方达中债3-5年 198,680.99'
    #   '国开行债券指数A 0.00 +2.99%'
    #
    # 用正则从每行里同时提取：名称片段 + 数字 + 可选收益率
    # 预处理：规范化行内数字串，消除 OCR 千位符/小数点混淆
    def _normalize_line(line: str) -> str:
        # ~ 是 OCR 误读的负号
        line = line.replace('~', '-')
        def fix_num(m):
            s = m.group(0)
            dots = s.count('.')
            commas = s.count(',')
            if dots >= 2:
                # 153.207.41 → 153207.41（多点：最后一个是小数）
                last = s.rfind('.')
                return s[:last].replace('.', '') + '.' + s[last+1:]
            if dots == 0 and commas >= 2:
                # 15,046,72 → 15046.72（多逗号：最后一个是小数）
                last = s.rfind(',')
                return s[:last].replace(',', '') + '.' + s[last+1:]
            if dots == 1 and commas == 1:
                # 位置靠后的分隔符是小数点
                if s.index('.') < s.index(','):
                    # 2.190,27 → dot千位，comma小数 → 2190.27
                    return s.replace('.', '').replace(',', '.')
                else:
                    # 2,190.27 → comma千位，dot小数 → 2190.27
                    return s.replace(',', '')
            if dots == 0 and commas == 1:
                # 单逗号：后面恰好2位视为小数点，否则千位符
                last = s.rfind(',')
                after = s[last+1:]
                if len(after) == 2:
                    return s[:last] + '.' + after
                return s.replace(',', '')
            return s.replace(',', '')
        return re.sub(r'\d[\d,\.]+\d', fix_num, line)

    # 从行里提取所有数字 token（含可选符号）
    _NUM_TOKEN = re.compile(r'[+\-]?\d[\d]*(?:\.\d+)?')

    i = 0
    while i < len(lines):
        line = _normalize_line(lines[i].strip())

        # 找行内第一个"大数字"（≥1000 或含小数的≥100）且其前有中文名称
        nums = list(_NUM_TOKEN.finditer(line))
        big_nums = [m for m in nums if _clean_num(m.group()) is not None
                    and abs(_clean_num(m.group())) >= 100
                    and ('.' in m.group() or abs(_clean_num(m.group())) >= 1000)]
        if not big_nums:
            i += 1
            continue

        # 找第一个名称前有中文字符的候选金额
        first_num = None
        name_part1 = ''
        for candidate in big_nums:
            candidate_name = line[:candidate.start()].strip()
            if re.search(r'[\u4e00-\u9fff]', candidate_name):
                # 确保名称不以孤立数字结尾（如"纳斯达克100"里的100是名称一部分）
                # 若候选金额有小数位，才是真正的金额
                if '.' in candidate.group():
                    first_num = candidate
                    name_part1 = candidate_name
                    break
        if first_num is None:
            i += 1
            continue

        if not _is_fund_name(name_part1):
            i += 1
            continue

        amount = _clean_num(first_num.group())
        if amount is None or amount < 100:
            i += 1
            continue

        # 第一行的其余数字：昨日收益（有符号）或收益率（%结尾）
        profit = None
        pct = None
        rest = line[first_num.end():]
        # 收益率（%）
        pct_m = re.search(r'([+\-]?\d+\.\d+)%', rest)
        if pct_m:
            pct = _clean_num(pct_m.group(1))
        # 昨日收益绝对值（有符号的数）
        profit_m = re.search(r'([+\-]\d[\d,.]+)', rest)
        if profit_m and not pct_m:  # 有符号数但不是收益率
            profit = _clean_num(profit_m.group(1))
        elif profit_m and pct_m and profit_m.start() < pct_m.start():
            profit = _clean_num(profit_m.group(1))

        raw_name = name_part1

        # 向后最多跳2行找续行（续行特征：含 % 收益率 且 首个数字 < 10）
        _CONT_RE = re.compile(
            r'^(.*?)\s*[\d.]+\s+([+\-]?\d+\.\d+)%'  # "name? 0.00 +2.99%"
        )
        for look in range(1, 3):
            if i + look >= len(lines):
                break
            line2 = _normalize_line(lines[i + look].strip())
            # 只剥除行首短噪音前缀（≤8个非空白字符），保留后面的数字和%
            line2_clean = re.sub(r'^[\[\(（【][^\s]{0,10}[\]\)）】]?\s*', '', line2)
            line2_clean = re.sub(r'^\S{1,8}超额收益\S{0,4}\s*', '', line2_clean)

            mc = _CONT_RE.match(line2_clean)
            if mc:
                name2 = mc.group(1).strip()
                pct_val = _clean_num(mc.group(2))
                if _is_fund_name(name2):
                    raw_name = name_part1 + name2
                if pct is None and pct_val is not None:
                    pct = pct_val
                i += look
                break

        rows.append({
            'raw_name': raw_name,
            'shares': 0.0,
            'amount': amount,
            'profit': profit or 0.0,
            'profit_pct': pct,
            'confidence': 'high' if pct is not None else 'medium',
        })
        i += 1

    return rows


def parse_transaction_screenshot(lines: list[str]) -> list[dict]:
    rows = []
    i = 0
    while i < len(lines):
        line = lines[i]
        dm = _DATE_RE.search(line)
        if not dm:
            i += 1
            continue

        date_raw = dm.group(1)
        direction = dm.group(2)
        date_str = _normalize_date(date_raw)
        tx_type = 'buy' if '买入' in direction else 'sell'

        raw_name = ''
        for j in range(i + 1, min(i + 4, len(lines))):
            candidate = lines[j].strip()
            if candidate and not _DATE_RE.search(candidate):
                raw_name = candidate
                break

        amount = shares = nav = None
        for j in range(i + 1, min(i + 6, len(lines))):
            combined = ' '.join(lines[max(0, j - 1):j + 2])
            m = _TX_RE.search(combined)
            if m:
                amount = _clean_num(m.group(1))
                shares = _clean_num(m.group(2))
                nav = _clean_num(m.group(3))
                break

        filled = sum(v is not None for v in [amount, shares, nav])
        confidence = 'high' if filled == 3 else ('medium' if filled >= 1 else 'low')

        rows.append({
            'raw_name': raw_name,
            'date': date_str,
            'tx_type': tx_type,
            'amount': amount or 0.0,
            'shares': shares or 0.0,
            'nav': nav or 0.0,
            'confidence': confidence,
        })
        i += 1

    return rows


def get_all_funds_from_db() -> list[dict]:
    conn = get_connection()
    rows = conn.execute('SELECT code, name FROM funds').fetchall()
    conn.close()
    return [dict(r) for r in rows]


def fuzzy_match_fund(raw_name: str, all_funds: list[dict]) -> list[dict]:
    scored = []
    for f in all_funds:
        ratio = difflib.SequenceMatcher(None, raw_name, f['name']).ratio()
        scored.append({'code': f['code'], 'name': f['name'], 'score': round(ratio, 3)})
    scored.sort(key=lambda x: x['score'], reverse=True)
    return scored[:5]


def bulk_replace_holdings(rows: list[dict]) -> dict:
    conn = get_connection()
    try:
        conn.execute('BEGIN')
        conn.execute('DELETE FROM holdings')
        for r in rows:
            shares = r['shares']
            cost_amount = r['cost_amount']
            # 截图列表页没有份额，用 持有金额 ÷ 最新净值 反算
            if shares == 0 and cost_amount > 0:
                nav_row = conn.execute(
                    'SELECT nav FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 1',
                    (r['code'],)
                ).fetchone()
                if nav_row and nav_row['nav'] and nav_row['nav'] > 0:
                    # amount = cost_amount + profit，前端传来的 cost_amount 已经是成本
                    # 用 (cost_amount + profit) ÷ nav 得到份额
                    current_value = cost_amount + r.get('profit', 0)
                    shares = round(current_value / nav_row['nav'], 4)
            conn.execute(
                'INSERT INTO holdings (code, shares, cost_amount, buy_date) VALUES (?, ?, ?, ?)',
                (r['code'], shares, cost_amount, r.get('buy_date', ''))
            )
        # 自动把导入的持仓基金加入关注列表
        for r in rows:
            conn.execute(
                "INSERT OR IGNORE INTO watchlist (code, added_at) VALUES (?, datetime('now'))",
                (r['code'],)
            )
        conn.commit()
        return {'status': 'ok', 'imported': len(rows), 'skipped': 0}
    except Exception as e:
        conn.rollback()
        return {'status': 'error', 'message': str(e)}
    finally:
        conn.close()


def bulk_import_transactions(rows: list[dict]) -> dict:
    from backend.portfolio import record_transaction

    conn = get_connection()
    valid_codes = {r['code'] for r in conn.execute('SELECT code FROM funds').fetchall()}
    conn.close()

    imported = skipped = 0
    for r in rows:
        if r.get('code') not in valid_codes:
            skipped += 1
            continue
        try:
            record_transaction(
                code=r['code'],
                tx_type=r['tx_type'],
                date=r['date'],
                amount=r['amount'],
                nav=r.get('nav') or None,
                fee=r.get('fee', 0),
            )
            imported += 1
        except Exception:
            skipped += 1

    return {'status': 'ok', 'imported': imported, 'skipped': skipped}
