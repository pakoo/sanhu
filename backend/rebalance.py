"""调仓建议模块"""
from __future__ import annotations
from backend.database import get_connection


CATEGORY_NAMES = {
    "bond": "债券型",
    "equity": "股票型",
    "mixed": "混合型",
    "qdii": "QDII/海外",
}


def get_target_allocation() -> dict:
    """获取目标配置"""
    conn = get_connection()
    rows = conn.execute("SELECT * FROM target_allocation").fetchall()
    conn.close()
    return {r["category"]: dict(r) for r in rows}


def set_target_allocation(allocations: dict[str, float]) -> dict:
    """设置目标配置

    Args:
        allocations: {"bond": 60, "equity": 20, "mixed": 10, "qdii": 10}
    """
    total = sum(allocations.values())
    if abs(total - 100) > 0.1:
        return {"error": f"配置比例之和为{total}%，需要等于100%"}

    conn = get_connection()
    for cat, pct in allocations.items():
        conn.execute(
            """INSERT OR REPLACE INTO target_allocation (category, target_pct, min_pct, max_pct)
               VALUES (?, ?, ?, ?)""",
            (cat, pct, max(0, pct - 10), min(100, pct + 10))
        )
    conn.commit()
    conn.close()
    return {"status": "ok"}


def get_rebalance_suggestions(holdings: list[dict], total_value: float) -> dict:
    """生成调仓建议

    Args:
        holdings: 持仓列表
        total_value: 总资产

    Returns:
        {
            "suggestions": [...],
            "current_allocation": {"bond": 85.2, ...},
            "target_allocation": {"bond": 60.0, ...},
            "total_adjustment": 50000
        }
    """
    targets = get_target_allocation()
    if not targets:
        return {"suggestions": [], "current_allocation": {}, "target_allocation": {}, "total_adjustment": 0}

    # 计算当前配置
    current = {}
    current_amounts = {}
    for h in holdings:
        cat = h.get("category", "mixed")
        current_amounts[cat] = current_amounts.get(cat, 0) + h["current_value"]

    for cat in targets:
        current[cat] = (current_amounts.get(cat, 0) / total_value * 100) if total_value > 0 else 0

    # 生成建议
    suggestions = []
    total_adjustment = 0

    for cat, target_info in targets.items():
        target_pct = target_info["target_pct"]
        current_pct = current.get(cat, 0)
        delta = target_pct - current_pct
        delta_amount = delta / 100 * total_value

        if abs(delta) < 1:
            action = "hold"
        elif delta > 0:
            action = "buy"
        else:
            action = "sell"

        # 推荐具体操作的基金
        fund_suggestions = []
        if action == "buy":
            cat_holdings = [h for h in holdings if h.get("category") == cat]

            # 查询综合评分，按评分降序排（无评分的排最后）
            try:
                from backend.database import get_connection as _gc
                conn_s = _gc()
                score_rows = conn_s.execute(
                    "SELECT code, total_score FROM fund_scores "
                    "WHERE date = (SELECT MAX(date) FROM fund_scores WHERE code = fund_scores.code)"
                ).fetchall()
                conn_s.close()
                score_map = {r["code"]: r["total_score"] for r in score_rows}
            except Exception:
                score_map = {}

            cat_holdings.sort(key=lambda x: score_map.get(x["code"], -1), reverse=True)
            for h in cat_holdings[:3]:
                score = score_map.get(h["code"])
                score_str = f" [{score:.0f}分]" if score is not None else ""
                fund_suggestions.append(f"{h['name']}({h['code']}){score_str}")

            if not fund_suggestions:
                if cat == "equity":
                    fund_suggestions.append("可考虑宽基指数基金如沪深300、中证500")
                elif cat == "qdii":
                    fund_suggestions.append("可考虑纳斯达克100、标普500等海外指数")
                elif cat == "mixed":
                    fund_suggestions.append("可考虑优秀的偏股混合型基金")

        # 估值信号注释
        note = ""
        if action == "buy" and cat in ("equity", "mixed", "qdii"):
            try:
                from backend.indices import get_all_valuation_signals as _gvs
                signals = _gvs()
                hs_pct = signals.get("hs300", {}).get("percentile")
                if hs_pct is not None:
                    if hs_pct > 70:
                        note = f"市场高估（沪深300 {hs_pct:.0f}%分位），建议分批小额加仓"
                    elif hs_pct < 30:
                        note = f"市场低估（沪深300 {hs_pct:.0f}%分位），可积极加仓"
            except Exception:
                pass

        suggestions.append({
            "category": cat,
            "category_name": CATEGORY_NAMES.get(cat, cat),
            "current_pct": round(current_pct, 2),
            "target_pct": target_pct,
            "delta_pct": round(delta, 2),
            "action": action,
            "amount": round(delta_amount, 2),
            "fund_suggestions": fund_suggestions,
            "note": note,
        })

        total_adjustment += abs(delta_amount)

    # 按调整幅度排序
    suggestions.sort(key=lambda x: abs(x["delta_pct"]), reverse=True)

    return {
        "suggestions": suggestions,
        "current_allocation": {k: round(v, 2) for k, v in current.items()},
        "target_allocation": {cat: info["target_pct"] for cat, info in targets.items()},
        "total_adjustment": round(total_adjustment / 2, 2),  # 买入=卖出，实际调整量取一半
        "comparison": {
            cat: {
                "current_pct": round(current.get(cat, 0), 2),
                "target_pct": info["target_pct"],
                "diff": round(info["target_pct"] - current.get(cat, 0), 2),
            }
            for cat, info in targets.items()
        },
    }


def gradual_transition_plan(holdings: list[dict], total_value: float,
                            months: int = 6, monthly_invest: float = 5000) -> list[dict]:
    """生成渐进式调仓计划

    Args:
        holdings: 当前持仓
        total_value: 总资产
        months: 过渡期(月)
        monthly_invest: 每月可投入金额

    Returns:
        [{"month": 1, "actions": [{"category": "equity", "action": "buy", "amount": 5000}], ...}, ...]
    """
    targets = get_target_allocation()
    if not targets:
        return []

    # 当前配置金额
    current_amounts = {}
    for h in holdings:
        cat = h.get("category", "mixed")
        current_amounts[cat] = current_amounts.get(cat, 0) + h["current_value"]

    plan = []

    for month in range(1, months + 1):
        # 计算当前各类别的缺口
        projected_total = total_value + monthly_invest * month
        actions = []

        gaps = {}
        for cat, target_info in targets.items():
            target_amount = projected_total * target_info["target_pct"] / 100
            current_cat = current_amounts.get(cat, 0)
            gap = target_amount - current_cat
            gaps[cat] = gap

        # 将新增资金分配给缺口最大的类别
        remaining = monthly_invest
        sorted_gaps = sorted(gaps.items(), key=lambda x: x[1], reverse=True)

        for cat, gap in sorted_gaps:
            if remaining <= 0 or gap <= 0:
                continue
            invest = min(remaining, gap)
            if invest >= 100:  # 最低投入100元
                actions.append({
                    "category": cat,
                    "category_name": CATEGORY_NAMES.get(cat, cat),
                    "action": "buy",
                    "amount": round(invest, 2),
                })
                current_amounts[cat] = current_amounts.get(cat, 0) + invest
                remaining -= invest

        # 如果还有剩余，投入债券
        if remaining > 100:
            actions.append({
                "category": "bond",
                "category_name": "债券型",
                "action": "buy",
                "amount": round(remaining, 2),
            })
            current_amounts["bond"] = current_amounts.get("bond", 0) + remaining

        # 当月配置比例
        month_total = sum(current_amounts.values())
        month_allocation = {
            cat: round(amt / month_total * 100, 2)
            for cat, amt in current_amounts.items()
        }

        plan.append({
            "month": month,
            "actions": actions,
            "allocation_after": month_allocation,
            "total_value": round(month_total, 2),
        })

    return plan
