"""
仓位建议模块 —— 从"记录决策"迈到"建议仓位"

核心原则：
- 不是黑盒。每一步用户都能看到，都能否决。
- 不要求用户输入任何新信息 —— 完全用已有数据：
    holdings + nav_history + target_allocation + timing + overlap
- 产品边界：工具只"建议"，最终决定权永远在用户手里。

公式（5 步）：
    Step 1  类别缺口     = max(0, target_pct - current_pct) × current_total
    Step 2  × 分批系数    (conservative 1/4, moderate 1/3, aggressive 1/2)
    Step 3  × Timing 折扣 (favorable 1.0, neutral 0.8, unfavorable 0.5)
    Step 4  × Overlap 折扣 (<20% 1.0, 20-40% 0.8, >40% 0.5)
    Step 5  边界 + 取整 (min 500, max min(30% × total, gap), round 100)
"""
from __future__ import annotations

from typing import Any, Dict, List, Optional, Set

from backend.database import get_connection
from backend.timing import get_timing_signal


# ── 常量 ──────────────────────────────────────────────────────────────────

_BATCH_RATIO = {
    "conservative": 0.25,
    "moderate": 1.0 / 3.0,
    "aggressive": 0.5,
}

_TIMING_DISCOUNT = {
    "favorable": 1.0,
    "neutral": 0.8,
    "unfavorable": 0.5,
}

_MIN_AMOUNT = 500.0
_SINGLE_FUND_CAP_PCT = 0.30   # 单只基金不超过总资产的 30%
_EMPTY_PORTFOLIO_SEED = 1000.0  # 空持仓时的初始试仓金额


# ── 数据读取 helpers ──────────────────────────────────────────────────────

def _get_category(conn, code: str) -> Optional[str]:
    """候选基金所属类别 (bond/equity/mixed/qdii)。fund_profile 优先，funds 次之。"""
    row = conn.execute(
        """
        SELECT COALESCE(
            (SELECT category FROM fund_profile WHERE code=?),
            (SELECT category FROM funds        WHERE code=?)
        ) AS category
        """,
        (code, code),
    ).fetchone()
    return row["category"] if row and row["category"] else None


def _get_fund_name(conn, code: str) -> Optional[str]:
    row = conn.execute(
        """
        SELECT COALESCE(
            (SELECT name FROM fund_profile WHERE code=?),
            (SELECT name FROM funds        WHERE code=?)
        ) AS name
        """,
        (code, code),
    ).fetchone()
    return row["name"] if row and row["name"] else None


def _get_holdings_snapshot(conn) -> Dict[str, Any]:
    """
    返回 {
      total_value: float,                    # 所有持仓最新市值总和
      cat_values:  {category: float, ...},   # 各类别最新市值
    }
    市值 = shares × latest_nav，若 nav 缺失回退到 cost_amount。
    """
    rows = conn.execute(
        """
        SELECT
            COALESCE(fp.category, f.category) AS category,
            COALESCE(h.shares * n.nav, h.cost_amount, 0) AS market_value
        FROM holdings h
        LEFT JOIN funds        f  ON f.code  = h.code
        LEFT JOIN fund_profile fp ON fp.code = h.code
        LEFT JOIN (
            SELECT nh.code, nh.nav
            FROM nav_history nh
            JOIN (
                SELECT code, MAX(date) AS max_date
                FROM nav_history
                GROUP BY code
            ) latest ON latest.code = nh.code AND latest.max_date = nh.date
        ) n ON n.code = h.code
        """
    ).fetchall()

    cat_values: Dict[str, float] = {}
    total = 0.0
    for row in rows:
        cat = row["category"] or "unknown"
        val = float(row["market_value"] or 0)
        total += val
        cat_values[cat] = cat_values.get(cat, 0.0) + val
    return {"total_value": total, "cat_values": cat_values}


def _get_target_allocation(conn) -> Dict[str, float]:
    """从 target_allocation 表读出各类别目标百分比（已除 100 转为小数）。"""
    rows = conn.execute(
        "SELECT category, target_pct FROM target_allocation"
    ).fetchall()
    return {row["category"]: float(row["target_pct"]) / 100.0 for row in rows}


def _compute_overlap_rate(conn, code: str) -> Optional[float]:
    """
    计算候选基金与用户当前持仓（equity/mixed/qdii 类）十大重仓股的重叠率。
    返回 0.0 ~ 1.0，数据不足时返回 None。
    复刻 backend/selector.py::_get_user_current_stock_set 逻辑。
    """
    # 1. user current stock set
    user_rows = conn.execute(
        """
        SELECT DISTINCT fh.stock_code
        FROM fund_holdings fh
        JOIN (
            SELECT code, MAX(report_date) AS report_date
            FROM fund_holdings
            GROUP BY code
        ) latest
          ON latest.code = fh.code AND latest.report_date = fh.report_date
        JOIN holdings h ON h.code = fh.code
        JOIN funds f    ON f.code = h.code
        WHERE f.category IN ('equity', 'mixed', 'qdii')
        """
    ).fetchall()
    user_stocks: Set[str] = {str(r["stock_code"]) for r in user_rows if r["stock_code"]}

    # 2. candidate stock set
    cand_rows = conn.execute(
        """
        SELECT fh.stock_code
        FROM fund_holdings fh
        JOIN (
            SELECT code, MAX(report_date) AS report_date
            FROM fund_holdings
            WHERE code=?
            GROUP BY code
        ) latest
          ON latest.code = fh.code AND latest.report_date = fh.report_date
        WHERE fh.code = ?
        """,
        (code, code),
    ).fetchall()
    cand_stocks: Set[str] = {str(r["stock_code"]) for r in cand_rows if r["stock_code"]}

    if not cand_stocks:
        return None
    if not user_stocks:
        return 0.0
    return len(cand_stocks & user_stocks) / float(len(cand_stocks))


# ── 折扣系数 helpers ──────────────────────────────────────────────────────

def _overlap_discount(overlap_rate: Optional[float]) -> float:
    if overlap_rate is None:
        return 1.0
    if overlap_rate < 0.20:
        return 1.0
    if overlap_rate < 0.40:
        return 0.8
    return 0.5


def _fmt_money(value: float) -> str:
    return f"¥{int(round(value)):,}"


def _fmt_pct(value: float) -> str:
    return f"{value * 100:.1f}%"


# ── 主函数 ────────────────────────────────────────────────────────────────

def suggest_position(code: str, risk_level: str = "moderate") -> Dict[str, Any]:
    """
    对单只基金给出一次买入的仓位建议。
    返回结构（每一步都透明暴露给 UI）：
    {
      code, name, category, risk_level,
      suggested_amount,       # 最终建议（取整到 100）
      range_min, range_max,
      steps:    [{label, detail, value}, ...],   # 给用户看的拆解
      warnings: [str, ...],
      inputs:   {total_value, current_cat_pct, target_cat_pct, timing_level, overlap_rate},
    }
    """
    if risk_level not in _BATCH_RATIO:
        risk_level = "moderate"
    batch_ratio = _BATCH_RATIO[risk_level]

    conn = get_connection()
    try:
        name = _get_fund_name(conn, code)
        category = _get_category(conn, code)
        snapshot = _get_holdings_snapshot(conn)
        targets = _get_target_allocation(conn)
        overlap_rate = _compute_overlap_rate(conn, code)
    finally:
        conn.close()

    timing = get_timing_signal(code)
    timing_level = timing.get("level", "neutral")
    timing_fragments = timing.get("fragments") or []

    warnings: List[str] = []
    steps: List[Dict[str, Any]] = []

    total_value = float(snapshot["total_value"] or 0.0)
    cat_value = float(snapshot["cat_values"].get(category, 0.0)) if category else 0.0
    current_cat_pct = (cat_value / total_value) if total_value > 0 else 0.0
    target_cat_pct = targets.get(category) if category else None

    # ── Step 1: 类别缺口 ──────────────────────────────────────────────────
    if not category:
        warnings.append(f"基金 {code} 无类别信息，无法计算缺口")
        return _empty_result(code, name, None, risk_level, warnings, timing_level)

    if total_value <= 0:
        # 空持仓：种子金额
        gap_amount = _EMPTY_PORTFOLIO_SEED
        steps.append({
            "label": "初始试仓 (holdings 为空)",
            "detail": f"{category} 目标 "
                      f"{_fmt_pct(target_cat_pct) if target_cat_pct else '未配置'}，"
                      f"建议先小额 ¥{int(_EMPTY_PORTFOLIO_SEED)} 试仓",
            "value": round(gap_amount, 2),
        })
        warnings.append("当前无持仓，按种子金额建议")
    elif target_cat_pct is None:
        # target_allocation 无该类别：回退到 15% 单基 cap
        gap_amount = total_value * 0.15
        steps.append({
            "label": "类别未在目标配置中",
            "detail": f"target_allocation 没定义 {category}，回退到总资产 15% 作为单基上限",
            "value": round(gap_amount, 2),
        })
        warnings.append(f"请先在目标配置里定义 {category} 类别的目标比例")
    else:
        gap_pct = max(0.0, target_cat_pct - current_cat_pct)
        gap_amount = gap_pct * total_value
        cat_detail = (
            f"{category} 当前 {_fmt_pct(current_cat_pct)} "
            f"({_fmt_money(cat_value)}), 目标 {_fmt_pct(target_cat_pct)} "
            f"({_fmt_money(target_cat_pct * total_value)})"
        )
        if gap_amount <= 0:
            # 类别已超配 → 建议 0 + 红色警示
            steps.append({
                "label": "类别已超配",
                "detail": cat_detail + "，无需加仓",
                "value": 0.0,
            })
            warnings.append(
                f"{category} 当前 {_fmt_pct(current_cat_pct)} ≥ 目标 "
                f"{_fmt_pct(target_cat_pct)}，建议暂不加仓"
            )
            return {
                "code": code,
                "name": name,
                "category": category,
                "risk_level": risk_level,
                "suggested_amount": 0,
                "range_min": 0,
                "range_max": 0,
                "steps": steps,
                "warnings": warnings,
                "inputs": {
                    "total_value": round(total_value, 2),
                    "current_cat_pct": round(current_cat_pct, 4),
                    "target_cat_pct": round(target_cat_pct, 4),
                    "timing_level": timing_level,
                    "overlap_rate": overlap_rate,
                },
            }
        else:
            steps.append({
                "label": "类别缺口",
                "detail": cat_detail,
                "value": round(gap_amount, 2),
            })

    # ── Step 2: 分批系数 ──────────────────────────────────────────────────
    batch_amount = gap_amount * batch_ratio
    batch_label_cn = {
        "conservative": "保守 1/4",
        "moderate": "稳健 1/3",
        "aggressive": "激进 1/2",
    }[risk_level]
    steps.append({
        "label": f"× 分批系数 ({batch_label_cn})",
        "detail": "分批建仓降低择时风险",
        "value": round(batch_amount, 2),
    })

    # ── Step 3: Timing 折扣 ───────────────────────────────────────────────
    timing_factor = _TIMING_DISCOUNT.get(timing_level, 0.8)
    after_timing = batch_amount * timing_factor
    timing_detail = " / ".join(timing_fragments[:2]) if timing_fragments else timing_level
    steps.append({
        "label": f"× Timing 折扣 {timing_factor}",
        "detail": timing_detail,
        "value": round(after_timing, 2),
    })
    if timing_level == "unfavorable":
        warnings.append("市场 timing 信号不利（高估/均线偏高），建议延后或进一步减半")
    elif timing_level == "neutral":
        warnings.append("市场 timing 中性，已按 0.8 折扣")

    # ── Step 4: Overlap 折扣 ──────────────────────────────────────────────
    overlap_factor = _overlap_discount(overlap_rate)
    after_overlap = after_timing * overlap_factor
    if overlap_rate is None:
        overlap_detail = "持仓数据不足，未打折"
    else:
        overlap_detail = f"与现有持仓重叠率 {_fmt_pct(overlap_rate)}"
    steps.append({
        "label": f"× Overlap 折扣 {overlap_factor}",
        "detail": overlap_detail,
        "value": round(after_overlap, 2),
    })
    if overlap_rate is not None and overlap_rate >= 0.40:
        warnings.append(f"与现有持仓重叠率 {_fmt_pct(overlap_rate)}，组合集中度过高")

    # ── Step 5: 边界 + 取整 ───────────────────────────────────────────────
    single_cap = total_value * _SINGLE_FUND_CAP_PCT if total_value > 0 else gap_amount
    range_max = max(_MIN_AMOUNT, min(single_cap, gap_amount))
    range_min = _MIN_AMOUNT

    clamped = max(0.0, min(after_overlap, range_max))
    if 0 < clamped < _MIN_AMOUNT:
        clamped = _MIN_AMOUNT
    rounded = round(clamped / 100.0) * 100.0

    steps.append({
        "label": "边界约束 + 取整百元",
        "detail": f"范围 {_fmt_money(range_min)} ~ {_fmt_money(range_max)}，"
                  f"单基上限 {_fmt_pct(_SINGLE_FUND_CAP_PCT)} 总资产",
        "value": rounded,
    })

    return {
        "code": code,
        "name": name,
        "category": category,
        "risk_level": risk_level,
        "suggested_amount": int(rounded),
        "range_min": int(range_min),
        "range_max": int(round(range_max)),
        "steps": steps,
        "warnings": warnings,
        "inputs": {
            "total_value": round(total_value, 2),
            "current_cat_pct": round(current_cat_pct, 4),
            "target_cat_pct": round(target_cat_pct, 4) if target_cat_pct is not None else None,
            "timing_level": timing_level,
            "overlap_rate": overlap_rate,
        },
    }


def _empty_result(code, name, category, risk_level, warnings, timing_level) -> Dict[str, Any]:
    return {
        "code": code,
        "name": name,
        "category": category,
        "risk_level": risk_level,
        "suggested_amount": 0,
        "range_min": 0,
        "range_max": 0,
        "steps": [],
        "warnings": warnings,
        "inputs": {"timing_level": timing_level},
    }


# ── 自测 ──────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    import json
    import sys

    test_codes = sys.argv[1:] or ["021095"]
    for c in test_codes:
        print(f"\n{'=' * 60}\ncode = {c}\n{'=' * 60}")
        result = suggest_position(c, risk_level="moderate")
        print(f"name:             {result.get('name')}")
        print(f"category:         {result.get('category')}")
        print(f"suggested_amount: ¥{result.get('suggested_amount'):,}")
        print(f"range:            ¥{result.get('range_min'):,} ~ ¥{result.get('range_max'):,}")
        print("\nsteps:")
        for s in result.get("steps", []):
            print(f"  • {s['label']}: ¥{int(round(s['value'])):,}")
            if s.get("detail"):
                print(f"      └─ {s['detail']}")
        if result.get("warnings"):
            print("\nwarnings:")
            for w in result["warnings"]:
                print(f"  ⚠️  {w}")
        print("\ninputs:")
        print(json.dumps(result.get("inputs"), ensure_ascii=False, indent=2))
