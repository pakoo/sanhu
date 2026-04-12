"""AI 持仓解读上下文生成模块"""
from __future__ import annotations

from datetime import datetime

from backend.portfolio import get_portfolio_summary
from backend.risk import risk_analysis
from backend.rebalance import get_rebalance_suggestions


def build_portfolio_context() -> str:
    """聚合持仓数据，生成可直接送给 Claude 的中文分析 prompt"""
    summary = get_portfolio_summary()
    holdings = summary["holdings"]
    total_value = summary["total_value"]

    # ── 市场估值信号 ──────────────────────────────
    valuation_text = ""
    try:
        from backend.indices import get_all_valuation_signals
        signals = get_all_valuation_signals()
        hs = signals.get("hs300", {})
        cs = signals.get("csi500", {})
        if hs.get("current_pe"):
            valuation_text = "\n## 市场估值信号\n"
            valuation_text += (
                f"- 沪深300：PE {hs['current_pe']}，"
                f"近10年百分位 {hs['percentile']}%，信号：{hs['signal']}\n"
            )
        if cs.get("current_pe"):
            valuation_text += (
                f"- 中证500：PE {cs['current_pe']}，"
                f"近10年百分位 {cs['percentile']}%，信号：{cs['signal']}\n"
            )
    except Exception:
        pass

    # ── 基金综合评分 ──────────────────────────────
    scores_text = ""
    try:
        from backend.scoring import get_latest_scores
        scores = get_latest_scores()
        if scores:
            scores_text = "\n## 基金综合评分（0-100分，越高越好）\n"
            for s in scores:
                scores_text += (
                    f"- {s['name']}：综合 {s['total_score']} 分"
                    f"（夏普{s['sharpe_score']}/回撤{s['drawdown_score']}"
                    f"/收益{s['return_score']}/波动{s['volatility_score']}）\n"
                )
    except Exception:
        pass

    # ── 风险预警 ──────────────────────────────────
    alerts_text = ""
    try:
        risk = risk_analysis(holdings, total_value)
        if risk["alerts"]:
            alerts_text = "\n## 当前风险预警\n"
            level_map = {"high": "高", "medium": "中", "low": "低"}
            for a in risk["alerts"][:6]:
                lv = level_map.get(a["level"], a["level"])
                alerts_text += f"- [{lv}] {a['message']}\n"
    except Exception:
        pass

    # ── 调仓建议 ──────────────────────────────────
    rebalance_text = ""
    try:
        rb = get_rebalance_suggestions(holdings, total_value)
        comparison = rb.get("comparison", {})
        suggestions = rb.get("suggestions", [])
        cat_names = {"bond": "债券", "equity": "A股权益", "mixed": "混合", "qdii": "QDII"}
        if comparison:
            rebalance_text = "\n## 当前配置 vs 目标配置\n"
            for cat, info in comparison.items():
                name = cat_names.get(cat, cat)
                curr = info.get("current_pct", 0)
                tgt = info.get("target_pct", 0)
                diff = info.get("diff", 0)
                rebalance_text += f"- {name}：当前 {curr:.1f}%，目标 {tgt:.1f}%，差距 {diff:+.1f}%\n"
        if suggestions:
            rebalance_text += "\n近期调仓建议：\n"
            for s in suggestions[:4]:
                action = s.get("action", "")
                name = s.get("name", "")
                amount = s.get("amount", 0)
                rebalance_text += f"- {action} {name} {amount:+.0f} 元\n"
    except Exception:
        pass

    # ── 组装正文 ──────────────────────────────────
    now = datetime.now().strftime("%Y-%m-%d %H:%M")
    lines = [
        f"# 基金持仓分析报告（{now}）",
        "",
        "## 持仓总览",
        f"- 总市值：{total_value:,.0f} 元",
        f"- 持有盈亏：{summary['total_profit']:+,.0f} 元（{summary['total_profit_rate']:+.2f}%）",
        f"- 昨日收益：{summary['daily_profit']:+,.0f} 元",
        "",
        "## 资产配置比例",
    ]
    cat_names = {"bond": "债券型", "equity": "A股权益", "mixed": "混合型", "qdii": "QDII/海外"}
    for cat, pct in summary.get("allocation", {}).items():
        lines.append(f"- {cat_names.get(cat, cat)}：{pct:.1f}%")

    lines += ["", "## 各基金持仓明细"]
    for h in holdings:
        ret_7d = f"{h['ret_7d']:+.2f}%" if h.get("ret_7d") is not None else "--"
        ret_1m = f"{h['ret_1m']:+.2f}%" if h.get("ret_1m") is not None else "--"
        lines.append(
            f"- {h['name']}（{h['code']}）："
            f"市值 {h['current_value']:,.0f} 元，"
            f"持有收益率 {h['profit_rate']:+.2f}%，"
            f"近7天 {ret_7d}，近1月 {ret_1m}"
        )

    context = "\n".join(lines)
    context += valuation_text
    context += alerts_text
    context += scores_text
    context += rebalance_text
    context += (
        "\n\n---\n"
        "## 分析任务\n"
        "请基于以上持仓快照，分析当前投资组合：\n"
        "1. 主要风险点（集中度、市场估值、个基回撤等）\n"
        "2. 近期市场环境对持仓的影响\n"
        "3. 具体可执行的操作建议（买入/卖出/持有，附理由）\n"
        "4. 如何调整配置以更好地达到「逐步提高权益比例」的目标\n\n"
        "**提示（仅适用于 Claude Code）**：以上数据为生成时快照。"
        "如需获取最新实时数据，可直接调用本地 API（服务运行于 http://localhost:8000）：\n"
        "- `GET /api/portfolio` — 最新持仓与盈亏\n"
        "- `GET /api/risk/analysis` — 风险预警与波动率\n"
        "- `GET /api/market/valuation` — 沪深300/中证500 PE百分位\n"
        "- `GET /api/funds/scores` — 各基金综合评分\n"
        "- `GET /api/rebalance/suggestions` — 当前调仓建议\n\n"
        "请用简洁的中文回答，重点放在可操作性上。"
    )
    return context


def get_ai_context() -> dict:
    """API handler"""
    context = build_portfolio_context()
    return {
        "context": context,
        "generated_at": datetime.now().strftime("%Y-%m-%d %H:%M"),
    }
