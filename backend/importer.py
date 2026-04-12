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
    from PIL import Image
    img = Image.open(io.BytesIO(image_bytes)).convert('RGB')
    if img.width > 1200:
        ratio = 1200 / img.width
        img = img.resize((1200, int(img.height * ratio)))

    reader = get_reader()
    results = reader.readtext(img)

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
    try:
        return float(s.replace(',', '').replace('，', ''))
    except (ValueError, AttributeError):
        return None


def _normalize_date(raw: str) -> str:
    cleaned = re.sub(r'[年月]', '-', raw).rstrip('日')
    parts = cleaned.split('-')
    if len(parts) == 3:
        return f"{parts[0]}-{parts[1].zfill(2)}-{parts[2].zfill(2)}"
    return cleaned


def parse_holdings_screenshot(lines: list[str]) -> list[dict]:
    full_text = '\n'.join(lines)
    rows = []
    i = 0
    while i < len(lines):
        line = lines[i]
        combined = line
        if i + 1 < len(lines):
            combined = line + ' ' + lines[i + 1]

        m = _HOLDINGS_RE.search(combined)
        if not m:
            i += 1
            continue

        shares = _clean_num(m.group(1))
        amount = _clean_num(m.group(3))
        profit_str = m.group(4).replace('＋', '+').replace('－', '-')
        profit = _clean_num(profit_str)

        raw_name = ''
        for j in range(i - 1, max(i - 5, -1), -1):
            candidate = lines[j].strip()
            if candidate and len(candidate) >= 4 and not re.fullmatch(r'[\d.,%+\-\s元份额市值收益]+', candidate):
                raw_name = candidate
                break

        if shares is None and amount is None:
            i += 1
            continue

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
            conn.execute(
                'INSERT INTO holdings (code, shares, cost_amount, buy_date) VALUES (?, ?, ?, ?)',
                (r['code'], r['shares'], r['cost_amount'], r.get('buy_date', ''))
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
