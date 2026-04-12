"""基金综合评分模块"""
from __future__ import annotations

import math
from datetime import date
from typing import Dict, List, Optional

import numpy as np

from backend.database import get_connection
from backend.risk import max_drawdown

SCORE_WEIGHTS = {
    "sharpe": 0.30,
    "max_drawdown": 0.25,
    "annualized_return": 0.25,
    "volatility": 0.20,
}
RISK_FREE_RATE = 0.02  # 年化无风险利率 2%


def calculate_fund_metrics(code: str, days: int = 252) -> Optional[dict]:
    """计算单基金四项原始指标

    Returns:
        {"sharpe": 0.8, "max_drawdown": -12.3, "annualized_return": 8.5, "volatility": 15.2}
        或 None（数据不足）
    """
    conn = get_connection()
    rows = conn.execute(
        "SELECT date, nav, daily_return FROM nav_history WHERE code=? ORDER BY date ASC",
        (code,),
    ).fetchall()
    conn.close()

    rows = rows[-days:] if len(rows) > days else rows
    if len(rows) < 30:
        return None

    navs = [r["nav"] for r in rows]
    returns = [r["daily_return"] for r in rows if r["daily_return"] is not None]

    # 年化收益率（CAGR）
    years = len(rows) / 252
    annualized_return = ((navs[-1] / navs[0]) ** (1 / years) - 1) * 100 if years > 0 and navs[0] > 0 else 0.0

    # 年化波动率
    volatility = float(np.std(returns)) * math.sqrt(252) if returns else 0.0

    # 夏普比率
    sharpe = (annualized_return / 100 - RISK_FREE_RATE) / (volatility / 100) if volatility > 0 else 0.0

    # 最大回撤
    dd = max_drawdown(navs)

    return {
        "sharpe": round(sharpe, 4),
        "max_drawdown": round(dd["max_drawdown"], 2),
        "annualized_return": round(annualized_return, 2),
        "volatility": round(volatility, 2),
    }


def _percentile_scores(values_dict: dict, higher_is_better: bool = True) -> dict:
    """将一组原始值转换为 0-100 的百分位评分"""
    if not values_dict:
        return {}
    if len(values_dict) == 1:
        return {c: 50.0 for c in values_dict}

    sorted_codes = sorted(values_dict, key=lambda c: values_dict[c])
    n = len(sorted_codes)
    scores = {}
    for rank, code in enumerate(sorted_codes):
        pct = rank / (n - 1) * 100
        scores[code] = pct if higher_is_better else (100.0 - pct)
    return scores


def calculate_all_scores(
    codes: Optional[List[str]] = None,
    fallback_to_peer_rank: bool = True,
) -> List[dict]:
    """批量计算基金综合评分，写入 fund_scores 表"""
    conn = get_connection()
    try:
        if codes is None:
            resolved_codes = [r["code"] for r in conn.execute("SELECT code FROM holdings").fetchall()]
        else:
            resolved_codes = list(codes)

        deduped_codes: List[str] = []
        seen_codes = set()
        for code in resolved_codes:
            if code in seen_codes:
                continue
            seen_codes.add(code)
            deduped_codes.append(code)
        resolved_codes = deduped_codes

        if not resolved_codes:
            return []

        today = date.today().isoformat()
        results: List[dict] = []
        cached_rows: Dict[str, dict] = {}
        pending_codes: List[str] = []

        for code in resolved_codes:
            row = conn.execute(
                "SELECT * FROM fund_scores WHERE code = ? AND date = ?",
                (code, today),
            ).fetchone()
            if row:
                cached_rows[code] = dict(row)
            else:
                pending_codes.append(code)

        metrics: Dict[str, dict] = {}
        for code in pending_codes:
            metric = calculate_fund_metrics(code)
            if metric:
                metrics[code] = metric

        sharpe_scores = _percentile_scores(
            {code: metric["sharpe"] for code, metric in metrics.items()},
            higher_is_better=True,
        )
        drawdown_scores = _percentile_scores(
            {code: metric["max_drawdown"] for code, metric in metrics.items()},
            higher_is_better=True,
        )
        return_scores = _percentile_scores(
            {code: metric["annualized_return"] for code, metric in metrics.items()},
            higher_is_better=True,
        )
        vol_scores = _percentile_scores(
            {code: metric["volatility"] for code, metric in metrics.items()},
            higher_is_better=False,
        )

        small_pool_fallback = len(resolved_codes) < 10 and fallback_to_peer_rank
        peer_rank_scores: Dict[str, float] = {}
        if small_pool_fallback:
            placeholders = ",".join("?" for _ in resolved_codes)
            rows = conn.execute(
                f"SELECT code, pct_1y FROM fund_peer_ranks WHERE code IN ({placeholders})",
                tuple(resolved_codes),
            ).fetchall()
            peer_rank_scores = {
                row["code"]: round(100.0 - row["pct_1y"], 1) if row["pct_1y"] is not None else 50.0
                for row in rows
            }

        for code in resolved_codes:
            row = cached_rows.get(code)
            if row is None:
                metric = metrics.get(code)
                if metric:
                    total = (
                        sharpe_scores[code] * SCORE_WEIGHTS["sharpe"]
                        + drawdown_scores[code] * SCORE_WEIGHTS["max_drawdown"]
                        + return_scores[code] * SCORE_WEIGHTS["annualized_return"]
                        + vol_scores[code] * SCORE_WEIGHTS["volatility"]
                    )
                    row = {
                        "code": code,
                        "date": today,
                        "total_score": round(total, 1),
                        "sharpe_score": round(sharpe_scores[code], 1),
                        "drawdown_score": round(drawdown_scores[code], 1),
                        "return_score": round(return_scores[code], 1),
                        "volatility_score": round(vol_scores[code], 1),
                        "sharpe_raw": metric["sharpe"],
                        "max_drawdown_raw": metric["max_drawdown"],
                        "annualized_return_raw": metric["annualized_return"],
                        "volatility_raw": metric["volatility"],
                    }
                elif small_pool_fallback:
                    row = {
                        "code": code,
                        "date": today,
                        "total_score": 50.0,
                        "sharpe_score": None,
                        "drawdown_score": None,
                        "return_score": None,
                        "volatility_score": None,
                        "sharpe_raw": None,
                        "max_drawdown_raw": None,
                        "annualized_return_raw": None,
                        "volatility_raw": None,
                    }
                else:
                    continue

            if small_pool_fallback:
                row["total_score"] = peer_rank_scores.get(code, 50.0)

            conn.execute(
                """INSERT OR REPLACE INTO fund_scores
                   (code, date, total_score, sharpe_score, drawdown_score, return_score, volatility_score,
                    sharpe_raw, max_drawdown_raw, annualized_return_raw, volatility_raw)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                (
                    row["code"], row["date"], row["total_score"],
                    row["sharpe_score"], row["drawdown_score"], row["return_score"], row["volatility_score"],
                    row["sharpe_raw"], row["max_drawdown_raw"], row["annualized_return_raw"], row["volatility_raw"],
                ),
            )
            results.append(row)

        conn.commit()
        return results
    finally:
        conn.close()


def get_latest_scores() -> List[dict]:
    """获取所有基金最新评分（含基金名称）"""
    conn = get_connection()
    rows = conn.execute(
        """SELECT fs.*, f.name FROM fund_scores fs
           JOIN funds f ON fs.code = f.code
           WHERE fs.date = (SELECT MAX(date) FROM fund_scores WHERE code = fs.code)
           ORDER BY fs.total_score DESC"""
    ).fetchall()
    conn.close()
    return [dict(r) for r in rows]


def get_fund_score(code: str) -> Optional[dict]:
    """获取单只基金最新评分"""
    conn = get_connection()
    row = conn.execute(
        """SELECT fs.*, f.name FROM fund_scores fs
           JOIN funds f ON fs.code = f.code
           WHERE fs.code = ?
           ORDER BY fs.date DESC LIMIT 1""",
        (code,),
    ).fetchone()
    conn.close()
    return dict(row) if row else None
