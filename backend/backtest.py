"""策略回测模块 - 定投/止盈/组合回测"""
from __future__ import annotations
import pandas as pd
import numpy as np
from datetime import datetime, timedelta
from backend.database import get_connection


def _get_nav_df(code: str, start_date: str = "", end_date: str = "") -> pd.DataFrame:
    """获取净值DataFrame"""
    conn = get_connection()

    sql = "SELECT date, nav, acc_nav FROM nav_history WHERE code=?"
    params = [code]

    if start_date:
        sql += " AND date >= ?"
        params.append(start_date)
    if end_date:
        sql += " AND date <= ?"
        params.append(end_date)

    sql += " ORDER BY date ASC"
    rows = conn.execute(sql, tuple(params)).fetchall()
    conn.close()

    if not rows:
        return pd.DataFrame()

    df = pd.DataFrame([dict(r) for r in rows])
    df["date"] = pd.to_datetime(df["date"])
    df = df.set_index("date")
    return df


def _get_dca_dates(start_date: str, end_date: str, frequency: str) -> list[str]:
    """生成定投日期序列"""
    start = datetime.strptime(start_date, "%Y-%m-%d")
    end = datetime.strptime(end_date, "%Y-%m-%d")

    dates = []
    current = start

    if frequency == "weekly":
        delta = timedelta(weeks=1)
    elif frequency == "biweekly":
        delta = timedelta(weeks=2)
    else:  # monthly
        delta = None  # 月频特殊处理

    if delta:
        while current <= end:
            dates.append(current.strftime("%Y-%m-%d"))
            current += delta
    else:
        while current <= end:
            dates.append(current.strftime("%Y-%m-%d"))
            # 下个月同一天
            month = current.month + 1
            year = current.year
            if month > 12:
                month = 1
                year += 1
            day = min(current.day, 28)
            current = datetime(year, month, day)

    return dates


def dca_backtest(code: str, amount: float, frequency: str = "monthly",
                 start_date: str = "", end_date: str = "") -> dict:
    """定投回测

    Args:
        code: 基金代码
        amount: 每期定投金额
        frequency: weekly / biweekly / monthly
        start_date: 开始日期
        end_date: 结束日期

    Returns:
        {
            "total_invested": 60000,
            "final_value": 65000,
            "total_return": 8.33,
            "annualized_return": 5.2,
            "max_drawdown": -10.5,
            "total_periods": 12,
            "avg_cost": 1.05,
            "current_nav": 1.12,
            "curve": [{"date": "...", "invested": ..., "value": ..., "return_pct": ...}, ...]
        }
    """
    df = _get_nav_df(code, start_date, end_date)
    if df.empty:
        return {"error": "没有足够的净值数据"}

    if not start_date:
        start_date = df.index[0].strftime("%Y-%m-%d")
    if not end_date:
        end_date = df.index[-1].strftime("%Y-%m-%d")

    dca_dates = _get_dca_dates(start_date, end_date, frequency)

    total_shares = 0
    total_invested = 0
    curve = []

    for target_date in dca_dates:
        target_dt = pd.Timestamp(target_date)

        # 找到最近的交易日
        valid_dates = df.index[df.index >= target_dt]
        if valid_dates.empty:
            valid_dates = df.index[df.index <= target_dt]
            if valid_dates.empty:
                continue
            actual_date = valid_dates[-1]
        else:
            actual_date = valid_dates[0]

        nav = df.loc[actual_date, "nav"]
        shares = amount / nav
        total_shares += shares
        total_invested += amount

        current_value = total_shares * nav
        return_pct = (current_value / total_invested - 1) * 100 if total_invested > 0 else 0

        curve.append({
            "date": actual_date.strftime("%Y-%m-%d"),
            "invested": round(total_invested, 2),
            "value": round(current_value, 2),
            "return_pct": round(return_pct, 2),
            "nav": round(nav, 4),
        })

    if not curve:
        return {"error": "没有有效的定投记录"}

    final_nav = df.iloc[-1]["nav"]
    final_value = total_shares * final_nav
    total_return = (final_value / total_invested - 1) * 100 if total_invested > 0 else 0

    # 年化收益率
    days = (df.index[-1] - pd.Timestamp(start_date)).days
    years = days / 365.25
    annualized_return = ((final_value / total_invested) ** (1 / years) - 1) * 100 if years > 0 and total_invested > 0 else 0

    # 计算定投过程中的最大回撤
    values = [c["value"] for c in curve]
    invested_list = [c["invested"] for c in curve]
    returns_curve = [(v / i - 1) * 100 if i > 0 else 0 for v, i in zip(values, invested_list)]

    max_return = float('-inf')
    max_dd = 0
    for r in returns_curve:
        max_return = max(max_return, r)
        dd = r - max_return
        max_dd = min(max_dd, dd)

    avg_cost = total_invested / total_shares if total_shares > 0 else 0

    return {
        "total_invested": round(total_invested, 2),
        "final_value": round(final_value, 2),
        "total_return": round(total_return, 2),
        "annualized_return": round(annualized_return, 2),
        "max_drawdown": round(max_dd, 2),
        "total_periods": len(curve),
        "avg_cost": round(avg_cost, 4),
        "current_nav": round(final_nav, 4),
        "start_date": start_date,
        "end_date": end_date,
        "curve": curve,
    }


def take_profit_backtest(code: str, amount: float, frequency: str = "monthly",
                         take_profit_pct: float = 15.0, stop_loss_pct: float | None = None,
                         start_date: str = "", end_date: str = "") -> dict:
    """止盈定投回测

    当累计收益率达到 take_profit_pct 时全部卖出，然后重新开始定投。

    Returns:
        {
            "cycles": [{"start": "...", "end": "...", "invested": ..., "sold_value": ..., "return": ...}, ...],
            "total_invested": 60000,
            "total_returned": 68000,
            "total_return": 13.3,
            "num_cycles": 3,
            "comparison": {"pure_dca_return": 8.33}  # 与纯定投对比
        }
    """
    df = _get_nav_df(code, start_date, end_date)
    if df.empty:
        return {"error": "没有足够的净值数据"}

    if not start_date:
        start_date = df.index[0].strftime("%Y-%m-%d")
    if not end_date:
        end_date = df.index[-1].strftime("%Y-%m-%d")

    dca_dates = _get_dca_dates(start_date, end_date, frequency)

    cycles = []
    total_invested = 0
    total_returned = 0

    # 当前周期
    cycle_shares = 0
    cycle_invested = 0
    cycle_start = None
    curve = []

    for target_date in dca_dates:
        target_dt = pd.Timestamp(target_date)
        valid_dates = df.index[df.index >= target_dt]
        if valid_dates.empty:
            continue
        actual_date = valid_dates[0]
        nav = df.loc[actual_date, "nav"]

        # 定投买入
        shares = amount / nav
        cycle_shares += shares
        cycle_invested += amount
        total_invested += amount

        if cycle_start is None:
            cycle_start = actual_date.strftime("%Y-%m-%d")

        current_value = cycle_shares * nav
        return_pct = (current_value / cycle_invested - 1) * 100

        curve.append({
            "date": actual_date.strftime("%Y-%m-%d"),
            "invested": round(total_invested, 2),
            "value": round(current_value + total_returned, 2),
            "return_pct": round((current_value + total_returned) / total_invested * 100 - 100, 2) if total_invested > 0 else 0,
        })

        # 检查止盈
        if return_pct >= take_profit_pct:
            cycles.append({
                "start": cycle_start,
                "end": actual_date.strftime("%Y-%m-%d"),
                "invested": round(cycle_invested, 2),
                "sold_value": round(current_value, 2),
                "return_pct": round(return_pct, 2),
            })
            total_returned += current_value
            cycle_shares = 0
            cycle_invested = 0
            cycle_start = None

        # 检查止损
        if stop_loss_pct and return_pct <= -stop_loss_pct:
            cycles.append({
                "start": cycle_start,
                "end": actual_date.strftime("%Y-%m-%d"),
                "invested": round(cycle_invested, 2),
                "sold_value": round(current_value, 2),
                "return_pct": round(return_pct, 2),
            })
            total_returned += current_value
            cycle_shares = 0
            cycle_invested = 0
            cycle_start = None

    # 未完成的周期
    if cycle_shares > 0:
        final_nav = df.iloc[-1]["nav"]
        final_value = cycle_shares * final_nav
        total_returned += final_value

    total_return = (total_returned / total_invested - 1) * 100 if total_invested > 0 else 0

    # 对比纯定投
    pure_dca = dca_backtest(code, amount, frequency, start_date, end_date)
    pure_dca_return = pure_dca.get("total_return", 0)

    return {
        "cycles": cycles,
        "total_invested": round(total_invested, 2),
        "total_returned": round(total_returned, 2),
        "total_return": round(total_return, 2),
        "num_cycles": len(cycles),
        "take_profit_pct": take_profit_pct,
        "stop_loss_pct": stop_loss_pct,
        "start_date": start_date,
        "end_date": end_date,
        "curve": curve,
        "comparison": {
            "pure_dca_return": pure_dca_return,
            "advantage": round(total_return - pure_dca_return, 2),
        },
    }


def portfolio_backtest(allocations: dict[str, float], total_amount: float,
                       rebalance_freq: str = "quarterly",
                       start_date: str = "", end_date: str = "") -> dict:
    """组合回测

    Args:
        allocations: {"007171": 50, "006195": 30, ...} code -> weight%
        total_amount: 初始总金额
        rebalance_freq: monthly / quarterly / yearly / never

    Returns:
        {"curve": [...], "total_return": ..., "annualized_return": ..., "max_drawdown": ..., "sharpe": ...}
    """
    # 获取所有基金净值
    dfs = {}
    for code in allocations:
        df = _get_nav_df(code, start_date, end_date)
        if not df.empty:
            dfs[code] = df[["nav"]]

    if not dfs:
        return {"error": "没有足够的净值数据"}

    # 合并所有净值数据，取交集日期
    combined = pd.concat(dfs, axis=1)
    combined.columns = combined.columns.droplevel(1)
    combined = combined.dropna()

    if combined.empty or len(combined) < 2:
        return {"error": "没有足够的重叠净值数据"}

    # 归一化权重
    total_weight = sum(allocations.values())
    weights = {code: w / total_weight for code, w in allocations.items() if code in combined.columns}

    # 初始配置
    holdings = {code: total_amount * w / combined.iloc[0][code] for code, w in weights.items()}

    # 再平衡日期
    rebalance_months = {"monthly": 1, "quarterly": 3, "yearly": 12, "never": 9999}
    rb_months = rebalance_months.get(rebalance_freq, 3)
    last_rebalance = combined.index[0]

    curve = []
    for i, (date, row) in enumerate(combined.iterrows()):
        # 计算当前组合价值
        portfolio_value = sum(holdings[code] * row[code] for code in holdings)

        # 检查是否需要再平衡
        months_since = (date.year - last_rebalance.year) * 12 + (date.month - last_rebalance.month)
        if months_since >= rb_months and i > 0:
            # 再平衡
            for code, w in weights.items():
                holdings[code] = portfolio_value * w / row[code]
            last_rebalance = date

        return_pct = (portfolio_value / total_amount - 1) * 100

        curve.append({
            "date": date.strftime("%Y-%m-%d"),
            "value": round(portfolio_value, 2),
            "return_pct": round(return_pct, 2),
        })

    if not curve:
        return {"error": "回测无数据"}

    final_value = curve[-1]["value"]
    total_return = (final_value / total_amount - 1) * 100

    days = (combined.index[-1] - combined.index[0]).days
    years = days / 365.25
    annualized_return = ((final_value / total_amount) ** (1 / years) - 1) * 100 if years > 0 else 0

    # 最大回撤
    values = [c["value"] for c in curve]
    running_max = np.maximum.accumulate(values)
    drawdowns = (np.array(values) - running_max) / running_max * 100
    max_dd = float(np.min(drawdowns))

    # Sharpe ratio (简化版, 无风险利率按2%算)
    daily_returns = pd.Series(values).pct_change().dropna()
    if len(daily_returns) > 0:
        sharpe = (daily_returns.mean() * 252 - 0.02) / (daily_returns.std() * np.sqrt(252)) if daily_returns.std() > 0 else 0
    else:
        sharpe = 0

    return {
        "total_amount": total_amount,
        "final_value": round(final_value, 2),
        "total_return": round(total_return, 2),
        "annualized_return": round(annualized_return, 2),
        "max_drawdown": round(max_dd, 2),
        "sharpe_ratio": round(sharpe, 3),
        "start_date": combined.index[0].strftime("%Y-%m-%d"),
        "end_date": combined.index[-1].strftime("%Y-%m-%d"),
        "rebalance_freq": rebalance_freq,
        "curve": curve,
    }
