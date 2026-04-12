"""持仓管理模块 - 组合跟踪与盈亏计算"""
from __future__ import annotations
from backend.database import get_connection


def get_portfolio_summary() -> dict:
    """获取持仓总览

    Returns:
        {
            "total_value": 415000,
            "total_cost": 405000,
            "total_profit": 10000,
            "total_profit_rate": 2.47,
            "daily_profit": 2748,
            "holdings": [...],
            "allocation": {"bond": 85.2, "equity": 5.0, ...},
            "allocation_amount": {"bond": 353489, ...}
        }
    """
    conn = get_connection()

    # 获取所有持仓
    holdings_rows = conn.execute("""
        SELECT h.code, h.shares, h.cost_amount, h.buy_date,
               f.name, f.category, f.fund_type
        FROM holdings h
        JOIN funds f ON h.code = f.code
        ORDER BY h.shares * COALESCE(
            (SELECT nav FROM nav_history WHERE code = h.code ORDER BY date DESC LIMIT 1), 1
        ) DESC
    """).fetchall()

    # 预加载所有持仓基金的 top-2 行业（单查一次、按 code 分组），
    # 避免为每只基金重复查询 fund_industry。
    all_codes = [r["code"] for r in holdings_rows]
    top_industries_by_code: dict[str, list[dict]] = {}
    if all_codes:
        placeholders = ",".join("?" * len(all_codes))
        rows_ind = conn.execute(f"""
            SELECT code, industry, weight
            FROM fund_industry
            WHERE code IN ({placeholders})
            ORDER BY code, weight DESC
        """, all_codes).fetchall()
        for r in rows_ind:
            top_industries_by_code.setdefault(r["code"], []).append(
                {"industry": r["industry"], "weight": r["weight"]}
            )

    holdings = []
    total_value = 0
    total_cost = 0
    total_daily_profit = 0
    allocation_amount = {}

    for row in holdings_rows:
        code = row["code"]

        # 获取最新净值、昨日收益率，以及近90天NAV序列（用于计算区间收益和曲线）
        nav_rows = conn.execute(
            "SELECT date, nav, daily_return FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 90",
            (code,)
        ).fetchall()

        current_nav = nav_rows[0]["nav"] if nav_rows else 1.0
        daily_return_pct = nav_rows[0]["daily_return"] if nav_rows and nav_rows[0]["daily_return"] else 0.0

        # 区间收益率：取最近 N 条记录的首尾净值差（按交易日计）
        def period_return(rows, n):
            if len(rows) >= n:
                start = rows[n - 1]["nav"]
                end = rows[0]["nav"]
                if start and start > 0:
                    return round((end - start) / start * 100, 2)
            return None

        ret_7d = period_return(nav_rows, 7)
        ret_1m = period_return(nav_rows, 21)
        ret_3m = period_return(nav_rows, 63)

        # 近一个月收益曲线（最近21条，日期升序，归一化为相对首日的%）
        curve_rows = list(reversed(nav_rows[:21]))
        nav_curve = []
        if curve_rows:
            base = curve_rows[0]["nav"]
            if base and base > 0:
                nav_curve = [
                    {"date": r["date"], "pct": round((r["nav"] - base) / base * 100, 3)}
                    for r in curve_rows
                ]

        shares = row["shares"]
        cost_amount = row["cost_amount"]
        current_value = shares * current_nav
        profit = current_value - cost_amount
        profit_rate = (profit / cost_amount * 100) if cost_amount > 0 else 0
        daily_profit = current_value * daily_return_pct / 100

        category = row["category"] or "mixed"
        allocation_amount[category] = allocation_amount.get(category, 0) + current_value

        holdings.append({
            "code": code,
            "name": row["name"],
            "category": category,
            "fund_type": row["fund_type"] or "",
            "shares": round(shares, 2),
            "cost_amount": round(cost_amount, 2),
            "current_nav": round(current_nav, 4),
            "current_value": round(current_value, 2),
            "profit": round(profit, 2),
            "profit_rate": round(profit_rate, 2),
            "daily_return": round(daily_return_pct, 2),
            "daily_profit": round(daily_profit, 2),
            "ret_7d": ret_7d,
            "ret_1m": ret_1m,
            "ret_3m": ret_3m,
            "nav_curve": nav_curve,
            "top2_industries": top_industries_by_code.get(code, [])[:2],
        })

        total_value += current_value
        total_cost += cost_amount
        total_daily_profit += daily_profit

    conn.close()

    total_profit = total_value - total_cost
    total_profit_rate = (total_profit / total_cost * 100) if total_cost > 0 else 0

    # 计算配置比例
    allocation = {}
    for cat, amount in allocation_amount.items():
        allocation[cat] = round(amount / total_value * 100, 2) if total_value > 0 else 0

    return {
        "total_value": round(total_value, 2),
        "total_cost": round(total_cost, 2),
        "total_profit": round(total_profit, 2),
        "total_profit_rate": round(total_profit_rate, 2),
        "daily_profit": round(total_daily_profit, 2),
        "holdings": holdings,
        "allocation": allocation,
        "allocation_amount": {k: round(v, 2) for k, v in allocation_amount.items()},
    }


def get_holding_detail(code: str) -> dict | None:
    """获取单只基金持仓详情（含基金档案与同类排名）

    注意：对于仅在 watchlist 中、不在 holdings 里的基金，返回的 shares/cost/profit 为 0，
    其他档案/净值/排名信息仍可展示。
    """
    conn = get_connection()

    holding = conn.execute("""
        SELECT h.code, h.shares, h.cost_amount, h.buy_date,
               f.name, f.category, f.fund_type, f.manager, f.risk_level,
               f.company, f.scope, f.inception_date, f.aum
        FROM holdings h
        JOIN funds f ON h.code = f.code
        WHERE h.code = ?
    """, (code,)).fetchone()

    # 兜底：watchlist-only 基金没有 holdings 行
    if not holding:
        fund = conn.execute("""
            SELECT code, name, category, fund_type, manager, risk_level,
                   company, scope, inception_date, aum
            FROM funds WHERE code = ?
        """, (code,)).fetchone()
        if not fund:
            conn.close()
            return None
        holding = {**dict(fund), "shares": 0, "cost_amount": 0, "buy_date": None}

    # 获取净值历史
    nav_history = conn.execute(
        "SELECT date, nav, acc_nav, daily_return FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 365",
        (code,)
    ).fetchall()

    # 获取交易记录
    transactions = conn.execute(
        "SELECT * FROM transactions WHERE code=? ORDER BY date DESC",
        (code,)
    ).fetchall()

    # 查 top 5 行业（用于弹窗行业集中度 + 展示）
    top_industry_rows = conn.execute("""
        SELECT industry, weight FROM fund_industry
        WHERE code=? ORDER BY weight DESC LIMIT 5
    """, (code,)).fetchall()
    top_industries = [dict(r) for r in top_industry_rows]
    concentration = sum((r["weight"] or 0) for r in top_industries[:3]) if top_industries else 0

    current_nav = nav_history[0]["nav"] if nav_history else 1.0
    shares = holding["shares"] or 0
    cost_amount = holding["cost_amount"] or 0
    current_value = shares * current_nav
    profit = current_value - cost_amount

    conn.close()

    # 同类排名（延迟 import 避免 akshare 未安装时启动即崩）
    try:
        from backend.peer_ranks import get_peer_rank
        peer_rank = get_peer_rank(code)
    except Exception:
        peer_rank = None

    return {
        "code": code,
        "name": holding["name"],
        "category": holding["category"],
        "fund_type": holding["fund_type"],
        "manager": holding["manager"] or "",
        "risk_level": holding["risk_level"] or "",
        "company": holding["company"] or "",
        "scope": holding["scope"] or "",
        "inception_date": holding["inception_date"] or "",
        "aum": holding["aum"],
        "peer_rank": peer_rank,
        "top_industries": top_industries,
        "industry_concentration": round(concentration, 2),
        "shares": round(shares, 2),
        "cost_amount": round(cost_amount, 2),
        "current_nav": round(current_nav, 4),
        "current_value": round(current_value, 2),
        "profit": round(profit, 2),
        "profit_rate": round(profit / cost_amount * 100, 2) if cost_amount > 0 else 0,
        "buy_date": holding["buy_date"],
        "nav_history": [dict(r) for r in nav_history],
        "transactions": [dict(r) for r in transactions],
    }


def record_transaction(code: str, tx_type: str, date: str, amount: float,
                       nav: float | None = None, fee: float = 0, notes: str = "") -> dict:
    """记录交易并更新持仓

    Args:
        code: 基金代码
        tx_type: buy / sell / dividend
        date: 交易日期
        amount: 交易金额(RMB)
        nav: 交易时净值(可选，自动查询)
        fee: 手续费
        notes: 备注
    """
    conn = get_connection()

    # 如果没有提供净值，查询当天净值
    if nav is None:
        nav_row = conn.execute(
            "SELECT nav FROM nav_history WHERE code=? AND date<=? ORDER BY date DESC LIMIT 1",
            (code, date)
        ).fetchone()
        nav = nav_row["nav"] if nav_row else 1.0

    actual_amount = amount - fee
    shares = actual_amount / nav if nav > 0 else 0

    # 记录交易
    conn.execute(
        """INSERT INTO transactions (code, type, date, amount, nav_at_trade, shares, fee, notes)
           VALUES (?, ?, ?, ?, ?, ?, ?, ?)""",
        (code, tx_type, date, amount, nav, shares, fee, notes)
    )

    # 更新持仓
    holding = conn.execute("SELECT * FROM holdings WHERE code=?", (code,)).fetchone()

    if tx_type == "buy":
        if holding:
            new_shares = holding["shares"] + shares
            new_cost = holding["cost_amount"] + actual_amount
            conn.execute(
                "UPDATE holdings SET shares=?, cost_amount=? WHERE code=?",
                (new_shares, new_cost, code)
            )
        else:
            conn.execute(
                "INSERT INTO holdings (code, shares, cost_amount, buy_date) VALUES (?, ?, ?, ?)",
                (code, shares, actual_amount, date)
            )
    elif tx_type == "sell":
        if holding and holding["shares"] > 0:
            # 按比例减少成本
            sell_ratio = min(shares / holding["shares"], 1.0)
            new_shares = holding["shares"] - shares
            new_cost = holding["cost_amount"] * (1 - sell_ratio)
            if new_shares <= 0:
                conn.execute("DELETE FROM holdings WHERE code=?", (code,))
            else:
                conn.execute(
                    "UPDATE holdings SET shares=?, cost_amount=? WHERE code=?",
                    (new_shares, new_cost, code)
                )

    conn.commit()
    conn.close()

    return {"status": "ok", "shares": round(shares, 2), "nav": nav}


def get_nav_history(code: str, days: int = 365) -> list[dict]:
    """获取基金净值历史"""
    conn = get_connection()
    rows = conn.execute(
        "SELECT date, nav, acc_nav, daily_return FROM nav_history WHERE code=? ORDER BY date DESC LIMIT ?",
        (code, days)
    ).fetchall()
    conn.close()
    return [dict(r) for r in rows]
