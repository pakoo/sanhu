from __future__ import annotations

from typing import Any, Dict, List, Optional

from backend.database import get_connection
from backend.indices import get_all_valuation_signals
from backend.scoring import get_latest_scores


def _to_float(value: Any) -> Optional[float]:
    try:
        if value in (None, ""):
            return None
        return float(value)
    except (TypeError, ValueError):
        return None


def _iter_index_signals(payload: Any) -> List[Dict[str, Any]]:
    signals: List[Dict[str, Any]] = []

    if isinstance(payload, list):
        for item in payload:
            if isinstance(item, dict):
                signals.append(dict(item))
        return signals

    if isinstance(payload, dict):
        if isinstance(payload.get("index_code"), str):
            signals.append(dict(payload))
        for key, value in payload.items():
            if isinstance(value, dict):
                item = dict(value)
                item.setdefault("index_code", str(key))
                signals.append(item)
    return signals


def get_timing_signal(code: str) -> dict:
    try:
        market_level = "neutral"
        market_note = "市场估值数据暂缺"
        pe_pct: Optional[float] = None

        valuation_signals = get_all_valuation_signals()
        hs300_signal = None
        for item in _iter_index_signals(valuation_signals):
            index_code = str(item.get("index_code") or "").upper()
            if "000300" in index_code or "HS300" in index_code:
                hs300_signal = item
                break

        if hs300_signal:
            pe_pct = _to_float(hs300_signal.get("pe_pct"))
            if pe_pct is None:
                pe_pct = _to_float(hs300_signal.get("percentile"))

            if pe_pct is not None:
                if pe_pct < 30:
                    market_level = "favorable"
                    market_note = f"HS300 PE {pe_pct:.0f}% 低估区间"
                elif pe_pct > 70:
                    market_level = "unfavorable"
                    market_note = f"HS300 PE {pe_pct:.0f}% 高估区间"
                else:
                    market_level = "neutral"
                    market_note = f"HS300 PE {pe_pct:.0f}% 合理区间"

        quality_level = "neutral"
        quality_note = "暂无评分数据"
        total_score: Optional[float] = None

        for item in get_latest_scores():
            if str(item.get("code") or "") == code:
                total_score = _to_float(item.get("total_score"))
                if total_score is None:
                    break
                if total_score >= 75:
                    quality_level = "favorable"
                    quality_note = f"综合分 {total_score:.0f} 较高"
                elif total_score < 50:
                    quality_level = "unfavorable"
                    quality_note = f"综合分 {total_score:.0f} 偏低"
                else:
                    quality_level = "neutral"
                    quality_note = f"综合分 {total_score:.0f}"
                break

        price_level = "neutral"
        price_note = "历史净值数据不足"
        deviation: Optional[float] = None

        conn = get_connection()
        try:
            rows = conn.execute(
                """
                SELECT date, nav
                FROM (
                    SELECT date, nav
                    FROM nav_history
                    WHERE code=?
                    ORDER BY date DESC
                    LIMIT 20
                )
                ORDER BY date ASC
                """,
                (code,),
            ).fetchall()
        finally:
            conn.close()

        if len(rows) >= 20:
            navs = [float(row["nav"]) for row in rows if row["nav"] is not None]
            if len(navs) >= 20:
                sma20 = sum(navs) / 20.0
                latest_nav = navs[-1]
                if sma20 > 0:
                    deviation = latest_nav / sma20 - 1
                    if deviation <= -0.05:
                        price_level = "favorable"
                        price_note = f"现价低于20日均线 {abs(deviation) * 100:.1f}%"
                    elif deviation >= 0.05:
                        price_level = "unfavorable"
                        price_note = f"现价高于20日均线 {deviation * 100:.1f}%"
                    else:
                        price_level = "neutral"
                        price_note = f"现价接近20日均线 ({deviation * 100:+.1f}%)"

        level_score = {"favorable": 1, "neutral": 0, "unfavorable": -1}
        composite = (
            level_score[market_level]
            + level_score[quality_level]
            + level_score[price_level]
        )

        if composite >= 2:
            final_level = "favorable"
        elif composite <= -2:
            final_level = "unfavorable"
        else:
            final_level = "neutral"

        return {
            "code": code,
            "level": final_level,
            "composite_score": composite,
            "fragments": [market_note, quality_note, price_note],
            "market_signal": {"level": market_level, "pe_pct": pe_pct},
            "quality_signal": {"level": quality_level, "total_score": total_score},
            "price_signal": {
                "level": price_level,
                "deviation_pct": deviation * 100 if deviation is not None else None,
            },
        }
    except Exception as e:
        return {
            "code": code,
            "level": "neutral",
            "composite_score": 0,
            "fragments": ["数据加载失败"],
            "error": str(e),
        }
