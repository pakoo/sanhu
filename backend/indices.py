"""指数估值模块 - PE/PB 历史数据与百分位分析"""
from __future__ import annotations

from backend.database import get_connection

SYMBOL_MAP = {
    "hs300": "沪深300",
    "csi500": "中证500",
}


def fetch_index_pe(index_code: str) -> dict:
    """从 akshare 拉取指数 PE 历史，存入 index_pe_history 表

    使用 ak.stock_index_pe_lg(symbol=...) — 返回列：
    ['日期', '指数', '等权静态市盈率', '静态市盈率', '静态市盈率中位数',
     '等权滚动市盈率', '滚动市盈率', '滚动市盈率中位数']
    使用「滚动市盈率」作为 PE（TTM），无 PB 数据。

    Returns:
        {"index_code": "hs300", "count": 120, "error": None}
    """
    try:
        import akshare as ak  # lazy import to avoid startup crash if not installed
    except ImportError:
        return {"index_code": index_code, "count": 0, "error": "akshare 未安装，请运行: pip install akshare"}

    symbol = SYMBOL_MAP.get(index_code)
    if not symbol:
        return {"index_code": index_code, "count": 0, "error": f"未知指数: {index_code}"}

    try:
        df = ak.stock_index_pe_lg(symbol=symbol)
    except Exception as e:
        return {"index_code": index_code, "count": 0, "error": str(e)}

    if df is None or df.empty:
        return {"index_code": index_code, "count": 0, "error": "返回数据为空"}

    # 列名：日期 / 滚动市盈率（TTM PE）
    date_col = "日期"
    pe_col = "滚动市盈率"

    if date_col not in df.columns or pe_col not in df.columns:
        return {"index_code": index_code, "count": 0,
                "error": f"列名不符，实际列: {list(df.columns)}"}

    conn = get_connection()
    count = 0
    for _, row in df.iterrows():
        try:
            raw_date = row[date_col]
            if hasattr(raw_date, "strftime"):
                date_str = raw_date.strftime("%Y-%m-%d")
            else:
                date_str = str(raw_date)[:10]

            pe_val = row[pe_col]
            pe = float(pe_val) if pe_val is not None and str(pe_val) not in ("", "nan") else None

            conn.execute(
                "INSERT OR REPLACE INTO index_pe_history (index_code, date, pe, pb) VALUES (?, ?, ?, ?)",
                (index_code, date_str, pe, None),
            )
            count += 1
        except Exception:
            continue

    conn.commit()
    conn.close()
    return {"index_code": index_code, "count": count, "error": None}


def get_pe_percentile(index_code: str, window_years: int = 10) -> dict:
    """计算当前PE在近N年历史中的百分位

    Returns:
        {"index_code": "hs300", "current_pe": 12.5, "current_pb": 1.2,
         "percentile": 28.3, "signal": "低估", "date": "2026-04-08",
         "history_count": 2500}
    """
    conn = get_connection()

    # 取最近 window_years 年数据
    limit = window_years * 252
    rows = conn.execute(
        "SELECT date, pe, pb FROM index_pe_history WHERE index_code=? AND pe IS NOT NULL ORDER BY date DESC LIMIT ?",
        (index_code, limit),
    ).fetchall()
    conn.close()

    if not rows:
        return {
            "index_code": index_code,
            "current_pe": None,
            "current_pb": None,
            "percentile": None,
            "signal": "无数据",
            "date": "",
            "history_count": 0,
        }

    latest = rows[0]
    current_pe = latest["pe"]
    current_pb = latest["pb"]
    current_date = latest["date"]

    pe_values = [r["pe"] for r in rows if r["pe"] is not None]
    history_count = len(pe_values)

    if history_count < 2:
        percentile = 50.0
    else:
        below = sum(1 for v in pe_values if v <= current_pe)
        percentile = round(below / history_count * 100, 1)

    if percentile < 30:
        signal = "低估"
    elif percentile < 70:
        signal = "合理"
    else:
        signal = "高估"

    return {
        "index_code": index_code,
        "current_pe": round(current_pe, 2) if current_pe else None,
        "current_pb": round(current_pb, 2) if current_pb else None,
        "percentile": percentile,
        "signal": signal,
        "date": current_date,
        "history_count": history_count,
    }


def get_all_valuation_signals() -> dict:
    """汇总 HS300 + CSI500 当前估值信号

    Returns:
        {"hs300": {...}, "csi500": {...}, "updated_at": "..."}
    """
    from datetime import datetime

    hs300 = get_pe_percentile("hs300")
    csi500 = get_pe_percentile("csi500")

    return {
        "hs300": hs300,
        "csi500": csi500,
        "updated_at": datetime.now().strftime("%Y-%m-%d %H:%M"),
    }


def refresh_index_pe() -> dict:
    """刷新两个指数的 PE 数据（调度器调用）"""
    r1 = fetch_index_pe("hs300")
    r2 = fetch_index_pe("csi500")
    return {"hs300": r1, "csi500": r2}
