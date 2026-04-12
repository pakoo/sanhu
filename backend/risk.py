"""风险分析模块"""
from __future__ import annotations
import numpy as np
import pandas as pd
from backend.database import get_connection


def concentration_risk(holdings: list[dict], total_value: float) -> list[dict]:
    """集中度风险分析

    Args:
        holdings: 持仓列表
        total_value: 总资产

    Returns:
        风险告警列表
    """
    alerts = []

    if total_value <= 0:
        return alerts

    # 单只基金集中度
    for h in holdings:
        pct = h["current_value"] / total_value * 100
        if pct > 40:
            alerts.append({
                "level": "high",
                "category": "concentration",
                "message": f"{h['name']}占比{pct:.1f}%，过度集中",
                "detail": f"单只基金占比超过40%，建议分散至30%以下",
            })
        elif pct > 25:
            alerts.append({
                "level": "medium",
                "category": "concentration",
                "message": f"{h['name']}占比{pct:.1f}%，较为集中",
                "detail": f"建议关注该基金的仓位管理",
            })

    # 类别集中度
    category_totals = {}
    for h in holdings:
        cat = h.get("category", "mixed")
        category_totals[cat] = category_totals.get(cat, 0) + h["current_value"]

    for cat, amount in category_totals.items():
        pct = amount / total_value * 100
        if cat == "bond" and pct > 80:
            alerts.append({
                "level": "medium",
                "category": "allocation",
                "message": f"债券配置占{pct:.1f}%，配置偏保守",
                "detail": "当前配置以债券为主，可能错过权益市场的增长机会。建议逐步增加权益配置。",
            })
        elif cat in ("equity", "mixed") and pct > 60:
            alerts.append({
                "level": "medium",
                "category": "allocation",
                "message": f"权益类配置占{pct:.1f}%，注意风险",
                "detail": "权益类占比较高，市场下跌时回撤可能较大",
            })

    return alerts


def max_drawdown(nav_series: list[float]) -> dict:
    """计算最大回撤

    Args:
        nav_series: 净值序列（按时间正序）

    Returns:
        {"max_drawdown": -15.2, "peak_idx": 10, "trough_idx": 25}
    """
    if not nav_series or len(nav_series) < 2:
        return {"max_drawdown": 0, "peak_idx": 0, "trough_idx": 0}

    arr = np.array(nav_series)
    running_max = np.maximum.accumulate(arr)
    drawdowns = (arr - running_max) / running_max * 100

    trough_idx = int(np.argmin(drawdowns))
    peak_idx = int(np.argmax(arr[:trough_idx + 1])) if trough_idx > 0 else 0

    return {
        "max_drawdown": round(float(drawdowns[trough_idx]), 2),
        "peak_idx": peak_idx,
        "trough_idx": trough_idx,
    }


def calculate_fund_drawdown(code: str, days: int = 365) -> dict:
    """计算单只基金的最大回撤"""
    conn = get_connection()
    rows = conn.execute(
        "SELECT date, nav FROM nav_history WHERE code=? ORDER BY date ASC",
        (code,)
    ).fetchall()
    conn.close()

    if not rows:
        return {"max_drawdown": 0, "peak_date": "", "trough_date": ""}

    # 取最近N天
    rows = rows[-days:] if len(rows) > days else rows
    navs = [r["nav"] for r in rows]
    dates = [r["date"] for r in rows]

    result = max_drawdown(navs)
    return {
        "max_drawdown": result["max_drawdown"],
        "peak_date": dates[result["peak_idx"]] if dates else "",
        "trough_date": dates[result["trough_idx"]] if dates else "",
    }


def correlation_matrix(fund_codes: list[str], days: int = 252) -> dict:
    """计算基金间收益率相关性矩阵

    Returns:
        {"codes": [...], "names": [...], "matrix": [[1.0, 0.8, ...], ...]}
    """
    conn = get_connection()

    # 获取所有基金的日收益率
    returns_dict = {}
    names = {}
    for code in fund_codes:
        rows = conn.execute(
            """SELECT nh.date, nh.daily_return, f.name
               FROM nav_history nh
               JOIN funds f ON nh.code = f.code
               WHERE nh.code=? AND nh.daily_return IS NOT NULL
               ORDER BY nh.date DESC LIMIT ?""",
            (code, days)
        ).fetchall()

        if rows:
            names[code] = rows[0]["name"]
            returns_dict[code] = {r["date"]: r["daily_return"] for r in rows}

    conn.close()

    if len(returns_dict) < 2:
        return {"codes": fund_codes, "names": [names.get(c, c) for c in fund_codes], "matrix": []}

    # 构建DataFrame
    df = pd.DataFrame(returns_dict)
    df = df.dropna()

    if df.empty or len(df) < 10:
        return {"codes": fund_codes, "names": [names.get(c, c) for c in fund_codes], "matrix": []}

    corr = df.corr()

    return {
        "codes": list(corr.columns),
        "names": [names.get(c, c) for c in corr.columns],
        "matrix": corr.values.round(3).tolist(),
    }


def portfolio_volatility(holdings: list[dict], days: int = 252) -> dict:
    """计算组合年化波动率

    Returns:
        {"portfolio_vol": 5.2, "fund_vols": {"007171": 2.1, ...}}
    """
    conn = get_connection()
    fund_vols = {}
    weights = {}
    total_value = sum(h["current_value"] for h in holdings)

    for h in holdings:
        code = h["code"]
        weights[code] = h["current_value"] / total_value if total_value > 0 else 0

        rows = conn.execute(
            "SELECT daily_return FROM nav_history WHERE code=? AND daily_return IS NOT NULL ORDER BY date DESC LIMIT ?",
            (code, days)
        ).fetchall()

        if rows and len(rows) > 5:
            returns = [r["daily_return"] for r in rows]
            daily_vol = np.std(returns)
            annual_vol = daily_vol * np.sqrt(252)
            fund_vols[code] = round(annual_vol, 2)
        else:
            fund_vols[code] = 0

    conn.close()

    # 组合波动率（简化：加权平均，未考虑相关性）
    portfolio_vol = sum(weights.get(code, 0) * vol for code, vol in fund_vols.items())

    return {
        "portfolio_vol": round(portfolio_vol, 2),
        "fund_vols": fund_vols,
    }


def risk_analysis(holdings: list[dict], total_value: float) -> dict:
    """综合风险分析

    Returns:
        {"alerts": [...], "drawdowns": {...}, "volatility": {...}, "score": 75}
    """
    alerts = concentration_risk(holdings, total_value)

    # 各基金回撤
    drawdowns = {}
    for h in holdings:
        dd = calculate_fund_drawdown(h["code"])
        drawdowns[h["code"]] = dd
        if dd["max_drawdown"] < -20:
            alerts.append({
                "level": "high",
                "category": "drawdown",
                "message": f"{h['name']}近一年最大回撤{dd['max_drawdown']:.1f}%",
                "detail": f"回撤较大，注意控制仓位。回撤区间: {dd['peak_date']} ~ {dd['trough_date']}",
            })
        elif dd["max_drawdown"] < -10:
            alerts.append({
                "level": "medium",
                "category": "drawdown",
                "message": f"{h['name']}近一年最大回撤{dd['max_drawdown']:.1f}%",
                "detail": f"回撤区间: {dd['peak_date']} ~ {dd['trough_date']}",
            })

    # 波动率
    vol = portfolio_volatility(holdings)

    # 亏损基金提醒
    for h in holdings:
        if h.get("profit_rate", 0) < -5:
            alerts.append({
                "level": "medium",
                "category": "loss",
                "message": f"{h['name']}当前亏损{h['profit_rate']:.1f}%",
                "detail": "建议评估是否需要止损或定投摊低成本",
            })

    # 估值信号预警
    try:
        from backend.indices import get_all_valuation_signals
        val_signals = get_all_valuation_signals()
        for idx_code, idx_label in [("hs300", "沪深300"), ("csi500", "中证500")]:
            sig = val_signals.get(idx_code, {})
            pct = sig.get("percentile")
            if pct is None:
                continue
            if pct < 30:
                alerts.append({
                    "level": "low",
                    "category": "valuation",
                    "message": f"{idx_label} PE处于历史低估区间（{pct:.0f}%分位）",
                    "detail": "市场估值偏低，可适当提高权益配置比例，分批加仓",
                })
            elif pct > 70:
                alerts.append({
                    "level": "medium",
                    "category": "valuation",
                    "message": f"{idx_label} PE处于历史高估区间（{pct:.0f}%分位）",
                    "detail": "市场估值偏高，建议谨慎加仓权益类资产，优先持有债券防守",
                })
    except Exception:
        pass

    # 风险评分 (0-100, 越高越安全)
    score = 80
    high_alerts = sum(1 for a in alerts if a["level"] == "high")
    medium_alerts = sum(1 for a in alerts if a["level"] == "medium")
    score -= high_alerts * 15
    score -= medium_alerts * 5
    score = max(0, min(100, score))

    return {
        "alerts": alerts,
        "drawdowns": drawdowns,
        "volatility": vol,
        "score": score,
    }
