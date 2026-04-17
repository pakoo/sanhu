"""支付宝基金截图 OCR 导入模块"""
from __future__ import annotations

import difflib
import io
import os
import re
from typing import Optional

from backend.database import get_connection


def ocr_image(image_bytes: bytes) -> list[str]:
    """调用 OCR.space API，返回按阅读顺序排列的文本行列表"""
    import requests

    api_key = os.environ.get('OCRSPACE_API_KEY') or _get_ocrspace_key() or 'K88263306488957'
    if not api_key:
        raise RuntimeError(
            'OCR API Key 未配置。请在设置页面填入 OCR.space API Key，'
            '或设置环境变量 OCRSPACE_API_KEY。'
            '免费 Key 申请：https://ocr.space/ocrapi/freekey'
        )

    # 压缩到 900KB 以内（OCR.space 免费版限制 1024KB），优先 WebP
    image_bytes = _compress_image(image_bytes, max_kb=900)
    # 检测实际格式
    mime = 'image/webp' if image_bytes[:4] == b'RIFF' or image_bytes[8:12] == b'WEBP' else 'image/jpeg'
    fname = 'screenshot.webp' if 'webp' in mime else 'screenshot.jpg'

    resp = requests.post(
        'https://api.ocr.space/parse/image',
        files={'file': (fname, image_bytes, mime)},
        data={
            'apikey': api_key,
            'language': 'chs',
            'isOverlayRequired': False,
            'OCREngine': 2,
        },
        timeout=30,
    )
    resp.raise_for_status()
    result = resp.json()

    if result.get('IsErroredOnProcessing'):
        raise RuntimeError(f"OCR.space 错误：{result.get('ErrorMessage', '未知错误')}")

    parsed = result.get('ParsedResults', [])
    if not parsed:
        return []

    text = parsed[0].get('ParsedText', '')
    lines = [l.strip() for l in text.splitlines() if l.strip()]
    return lines


def _compress_image(image_bytes: bytes, max_kb: int = 900) -> bytes:
    """压缩图片至 max_kb 以内，优先用 WebP，回退 JPEG"""
    from PIL import Image
    img = Image.open(io.BytesIO(image_bytes)).convert('RGB')

    # 先缩放：宽度超过 1200 按比例缩小
    if img.width > 1200:
        ratio = 1200 / img.width
        img = img.resize((1200, int(img.height * ratio)), Image.LANCZOS)

    max_bytes = max_kb * 1024

    # 尝试 WebP（压缩率更好）
    for quality in (85, 70, 55, 40):
        buf = io.BytesIO()
        img.save(buf, format='WEBP', quality=quality)
        if buf.tell() <= max_bytes:
            return buf.getvalue()

    # 回退 JPEG
    for quality in (85, 70, 55, 40):
        buf = io.BytesIO()
        img.save(buf, format='JPEG', quality=quality)
        if buf.tell() <= max_bytes:
            return buf.getvalue()

    return buf.getvalue()


def _get_ocrspace_key() -> str:
    """从 user_settings 读取 OCR.space API Key"""
    from backend.database import get_setting
    return get_setting('ocrspace_api_key') or ''


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
    # "产品提醒 xxx"、"市场解读 xxx"、"金选 xxx" 这类插屏广告/标签行
    if re.match(r'^(产品提醒|市场解读|热销基金|反馈|纯债|超额收益|\[|金选)', s):
        return False
    # 导航/UI 元素（以全角括号、圆圈符号开头）
    if re.match(r'^[＜＞〈〉◎○●★☆]', s):
        return False
    # 安全提示文字
    if '资金安全有保障' in s or '基金销售服务' in s:
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

        # 若没拿到绝对收益，但有收益率，反推：profit = amount * pct / (100 + pct)
        # 原理：持有收益率 = profit/cost*100，cost = amount - profit → 解方程
        if profit is None and pct is not None and amount > 0:
            profit = round(amount * pct / (100 + pct), 2)

        rows.append({
            'raw_name': raw_name,
            'shares': 0.0,
            'amount': amount,
            'profit': profit or 0.0,
            'profit_pct': pct,
            'confidence': 'high' if pct is not None else 'medium',
        })
        i += 1

    if rows:
        return rows

    # ── 格式 C：OCR.space 逐行格式（每项单独一行）─────────────────────────────
    # 特征：金额单独占一行，基金名在其前 1-5 行，收益信息在其后若干行
    _BIG_AMT_RE = re.compile(r'^\d{1,3}(?:,\d{3})*\.\d{2}$')

    # 找持仓金额锚点（大数字，且前面有基金名）
    anchor_indices = []
    for i, line in enumerate(lines):
        if not _BIG_AMT_RE.match(line.strip()):
            continue
        v = _clean_num(line.strip())
        if v is None or v < 500:
            continue
        if any(_is_fund_name(lines[j].strip()) for j in range(max(0, i - 5), i)):
            anchor_indices.append(i)

    rows = []
    for k, idx in enumerate(anchor_indices):
        amount = _clean_num(lines[idx].strip())

        # 向前找基金名（跳过非名称行，最多往前 6 行，最多取 2 段名称）
        name_parts = []
        j = idx - 1
        while j >= max(idx - 6, 0) and len(name_parts) < 2:
            cand = lines[j].strip()
            if _is_fund_name(cand):
                name_parts.insert(0, cand)
            j -= 1
        if not name_parts:
            continue
        raw_name = ''.join(name_parts)

        # 向后查找收益信息，止于下一个锚点
        end = anchor_indices[k + 1] if k + 1 < len(anchor_indices) else len(lines)
        explicit_pcts = []
        signed_vals = []
        for j in range(idx + 1, end):
            l = lines[j].strip()
            pm = re.match(r'^([+\-]?\d+\.\d+)%\.?$', l)
            if pm:
                explicit_pcts.append(_clean_num(pm.group(1)))
                continue
            sm = re.match(r'^([+\-]\d[\d,.]+)$', l)
            if sm:
                v = _clean_num(sm.group(1))
                if v is not None:
                    signed_vals.append(v)

        pct = None
        profit = None
        if explicit_pcts:
            pct = explicit_pcts[0]
            candidates = [v for v in signed_vals if abs(v) < amount]
            if candidates:
                profit = candidates[-1]
        elif signed_vals:
            # 无显式 % 符号：有符号小数且绝对值 < 100 → 视为收益率
            pct_cands = [v for v in signed_vals if abs(v) < 100]
            if pct_cands:
                pct = pct_cands[0]

        if profit is None and pct is not None and amount > 0:
            profit = round(amount * pct / (100 + pct), 2)

        rows.append({
            'raw_name': raw_name,
            'shares': 0.0,
            'amount': amount,
            'profit': profit or 0.0,
            'profit_pct': pct,
            'confidence': 'high' if pct is not None else 'medium',
        })

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
    """UPSERT：列表里的基金有则更新、无则新增，列表外的持仓不受影响。"""
    conn = get_connection()
    try:
        conn.execute('BEGIN')
        updated = 0
        inserted = 0
        for r in rows:
            shares = r['shares']
            cost_amount = r['cost_amount']
            profit = r.get('profit', 0) or 0
            # 从持有金额反推份额和成本
            if shares == 0 and cost_amount > 0:
                current_value = cost_amount + profit
                nav_row = conn.execute(
                    'SELECT nav FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 1',
                    (r['code'],)
                ).fetchone()
                if nav_row and nav_row['nav'] and nav_row['nav'] > 0:
                    nav = nav_row['nav']
                    shares = round(current_value / nav, 4)
                    cost_amount = round(shares * nav - profit, 4)
            # UPSERT：有则更新，无则插入
            existing = conn.execute(
                'SELECT id FROM holdings WHERE code=?', (r['code'],)
            ).fetchone()
            if existing:
                conn.execute(
                    'UPDATE holdings SET shares=?, cost_amount=? WHERE code=?',
                    (shares, cost_amount, r['code'])
                )
                updated += 1
            else:
                conn.execute(
                    'INSERT INTO holdings (code, shares, cost_amount, buy_date) VALUES (?, ?, ?, ?)',
                    (r['code'], shares, cost_amount, r.get('buy_date', ''))
                )
                inserted += 1
        # 自动把导入的持仓基金加入关注列表
        for r in rows:
            conn.execute(
                "INSERT OR IGNORE INTO watchlist (code, added_at) VALUES (?, datetime('now'))",
                (r['code'],)
            )
        conn.commit()
        return {'status': 'ok', 'updated': updated, 'inserted': inserted}
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
