"""
AI 入口 prompt 渲染模块（v2.3 Track A）

职责：读 prompts/ 目录下的模板，用工具数据库产出的结构化数据填占位符，
返回渲染后的 Markdown 字符串给前端一键复制。

哲学红线（对应 feedback_ai_philosophy.md v2.3 细化）：
- 所有 prompt 必须以工具独有的结构化数据为主体（> 500 字）
- 模板头部的 version + last_verified_date 会被提取并回传给 UI，
  用于"可能已过期"告警
- 工具自己不调用 LLM，只渲染 prompt 字符串
"""
from __future__ import annotations

import os
import re
from datetime import date, datetime
from typing import Any, Dict, List, Optional

from backend.database import get_connection
from backend.indices import get_all_valuation_signals
from backend.selector import find_peer_funds_by_theme, _parse_theme_tags
from backend.timing import get_timing_signal

PROMPTS_DIR = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "prompts")


# ─── 模板加载 + 元数据提取 ──────────────────────────────────────────

def _load_template(name: str) -> Dict[str, Any]:
    """
    加载 prompts/{name}.md，提取头部 HTML 注释里的 version/last_verified_date，
    返回 {version, last_verified_date, body}
    """
    path = os.path.join(PROMPTS_DIR, f"{name}.md")
    if not os.path.exists(path):
        raise FileNotFoundError(f"prompt template not found: {path}")
    with open(path, "r", encoding="utf-8") as f:
        raw = f.read()

    version = "unknown"
    last_verified = "unknown"
    m = re.match(r"<!--(.*?)-->\s*", raw, re.DOTALL)
    if m:
        header = m.group(1)
        v_match = re.search(r"version:\s*([^\s]+)", header)
        d_match = re.search(r"last_verified_date:\s*([^\s]+)", header)
        if v_match:
            version = v_match.group(1).strip()
        if d_match:
            last_verified = d_match.group(1).strip()
        body = raw[m.end():]
    else:
        body = raw

    return {
        "version": version,
        "last_verified_date": last_verified,
        "body": body,
    }


def _is_template_stale(last_verified: str, max_days: int = 90) -> bool:
    try:
        d = datetime.strptime(last_verified, "%Y-%m-%d").date()
        return (date.today() - d).days > max_days
    except (ValueError, TypeError):
        return True  # 解析失败视为过期


# ─── 数据段生成器 ──────────────────────────────────────────────────

def _format_pct(value: Any, digits: int = 2) -> str:
    if value is None:
        return "—"
    try:
        return f"{float(value) * 100:.{digits}f}%"
    except (TypeError, ValueError):
        return "—"


def _format_num(value: Any, digits: int = 2) -> str:
    if value is None:
        return "—"
    try:
        return f"{float(value):.{digits}f}"
    except (TypeError, ValueError):
        return "—"


def _build_holdings_section(code: str) -> Dict[str, Any]:
    conn = get_connection()
    try:
        latest = conn.execute(
            "SELECT MAX(report_date) AS d FROM fund_holdings WHERE code=?", (code,)
        ).fetchone()
        report_date = latest["d"] if latest and latest["d"] else ""
        if not report_date:
            return {
                "report_date": "（数据缺失）",
                "table": "| 股票代码 | 名称 | 权重 | 行业 |\n|---|---|---|---|\n| — | **该基金持股数据缺失** | — | — |",
                "count": 0,
            }
        rows = conn.execute(
            "SELECT stock_code, stock_name, weight, industry FROM fund_holdings "
            "WHERE code=? AND report_date=? ORDER BY weight DESC",
            (code, report_date),
        ).fetchall()
    finally:
        conn.close()

    if not rows:
        return {
            "report_date": report_date,
            "table": "| 股票代码 | 名称 | 权重 | 行业 |\n|---|---|---|---|\n| — | **该基金持股数据缺失** | — | — |",
            "count": 0,
        }

    lines = ["| 股票代码 | 名称 | 权重 | 行业 |", "|---|---|---|---|"]
    for r in rows:
        industry = r["industry"] or "（未分类）"
        lines.append(
            f"| {r['stock_code']} | {r['stock_name']} | {_format_num(r['weight'], 2)}% | {industry} |"
        )
    return {"report_date": report_date, "table": "\n".join(lines), "count": len(rows)}


def _build_peers_section(seed_code: str, limit: int = 8) -> Dict[str, Any]:
    peer_result = find_peer_funds_by_theme(seed_code, limit=limit)
    peers = peer_result.get("peers") or []

    if not peers:
        return {
            "matched_on": peer_result.get("matched_on") or "（无）",
            "matched_tags": ", ".join(peer_result.get("matched_tags") or []) or "（无）",
            "table": "| 代码 | 名称 | 类别 | 同类 pct_1y | 综合分 | 近 1 月 | 近 3 月 |\n|---|---|---|---|---|---|---|\n| — | **Peer 数据缺失或无匹配** | — | — | — | — | — |",
            "count": 0,
            "inferred_theme": peer_result.get("seed", {}).get("inferred_theme"),
            "note": peer_result.get("note"),
        }

    lines = [
        "| 代码 | 名称 | 类别 | 同类 pct_1y | 综合分 | 近 1 月 | 近 3 月 |",
        "|---|---|---|---|---|---|---|",
    ]
    for p in peers:
        lines.append(
            "| {code} | {name} | {cat} | {pct1y} | {score} | {r1m} | {r3m} |".format(
                code=p.get("code") or "—",
                name=(p.get("name") or "—")[:24],
                cat=p.get("category") or "—",
                pct1y=_format_pct(p.get("pct_1y"), 1),
                score=_format_num(p.get("total_score"), 1),
                r1m=_format_pct(p.get("ret_1m"), 2),
                r3m=_format_pct(p.get("ret_3m"), 2),
            )
        )
    return {
        "matched_on": peer_result.get("matched_on") or "—",
        "matched_tags": ", ".join(peer_result.get("matched_tags") or []) or "—",
        "table": "\n".join(lines),
        "count": len(peers),
        "inferred_theme": peer_result.get("seed", {}).get("inferred_theme"),
        "note": peer_result.get("note"),
    }


def _build_valuation_section() -> Dict[str, Any]:
    try:
        signals = get_all_valuation_signals() or {}
    except Exception as exc:
        return {
            "hs300_pe": "—",
            "hs300_pe_pct": "—",
            "hs300_signal": f"（获取失败：{exc}）",
            "csi500_pe": "—",
            "csi500_pe_pct": "—",
            "csi500_signal": "—",
        }
    hs = signals.get("hs300") or {}
    cs = signals.get("csi500") or {}
    return {
        "hs300_pe": _format_num(hs.get("current_pe"), 2),
        "hs300_pe_pct": _format_num(hs.get("percentile"), 1),
        "hs300_signal": hs.get("signal") or "—",
        "csi500_pe": _format_num(cs.get("current_pe"), 2),
        "csi500_pe_pct": _format_num(cs.get("percentile"), 1),
        "csi500_signal": cs.get("signal") or "—",
    }


def _build_timing_section(code: str) -> Dict[str, Any]:
    try:
        t = get_timing_signal(code) or {}
    except Exception as exc:
        return {
            "level": f"（获取失败：{exc}）",
            "market": "—",
            "quality": "—",
            "price": "—",
        }
    market = t.get("market_signal") or {}
    quality = t.get("quality_signal") or {}
    price = t.get("price_signal") or {}
    fragments = t.get("fragments") or []
    frag_str = "; ".join(fragments) if fragments else "—"
    return {
        "level": f"{t.get('level', '—')}（composite={t.get('composite_score', '—')}；{frag_str}）",
        "market": f"{market.get('level', '—')} / PE 百分位 {_format_num(market.get('pe_pct'), 1)}%",
        "quality": f"{quality.get('level', '—')} / 总分 {quality.get('total_score', '—')}",
        "price": f"{price.get('level', '—')} / 现价偏离 20 日均线 {_format_num(price.get('deviation_pct'), 2)}%",
    }


# ─── 主入口：赛道分析 prompt ───────────────────────────────────────

def render_theme_analysis_prompt(seed_code: str) -> Dict[str, Any]:
    """
    渲染赛道分析 prompt。返回结构：
        {
            "prompt": str,
            "version": str,
            "last_verified_date": str,
            "stale": bool,
            "char_count": int,
            "data_sections": { ... },  # 每段数据的元信息（用于监控）
        }
    """
    tpl = _load_template("theme_analysis")
    body = tpl["body"]

    # Seed 基本信息
    conn = get_connection()
    try:
        row = conn.execute(
            """
            SELECT fp.code, COALESCE(fp.name, f.name, fp.code) AS name,
                   COALESCE(fp.category, f.category) AS category,
                   fp.theme_tags
            FROM fund_profile fp
            LEFT JOIN funds f ON f.code = fp.code
            WHERE fp.code = ?
            """,
            (seed_code,),
        ).fetchone()
    finally:
        conn.close()

    if row:
        seed_name = row["name"] or seed_code
        seed_category = row["category"] or "—"
        seed_theme_tags_list = _parse_theme_tags(row["theme_tags"])
    else:
        seed_name = seed_code
        seed_category = "—"
        seed_theme_tags_list = []

    seed_theme_tags_str = ", ".join(seed_theme_tags_list) if seed_theme_tags_list else "（该基金无主题标签，已降级用持股聚合推断）"

    holdings = _build_holdings_section(seed_code)
    peers = _build_peers_section(seed_code, limit=8)
    valuation = _build_valuation_section()
    timing = _build_timing_section(seed_code)

    inferred_theme = peers.get("inferred_theme") or "—"

    replacements: Dict[str, str] = {
        "{seed_code}": seed_code,
        "{seed_name}": seed_name,
        "{seed_category}": seed_category,
        "{seed_theme_tags}": seed_theme_tags_str,
        "{inferred_theme}": inferred_theme or "—",
        "{as_of_date}": date.today().isoformat(),
        "{holdings_report_date}": holdings["report_date"],
        "{holdings_table}": holdings["table"],
        "{matched_on}": peers["matched_on"],
        "{matched_tags}": peers["matched_tags"],
        "{peers_table}": peers["table"],
        "{hs300_pe}": valuation["hs300_pe"],
        "{hs300_pe_pct}": valuation["hs300_pe_pct"],
        "{hs300_signal}": valuation["hs300_signal"],
        "{csi500_pe}": valuation["csi500_pe"],
        "{csi500_pe_pct}": valuation["csi500_pe_pct"],
        "{csi500_signal}": valuation["csi500_signal"],
        "{seed_timing_level}": timing["level"],
        "{seed_market_signal}": timing["market"],
        "{seed_quality_signal}": timing["quality"],
        "{seed_price_signal}": timing["price"],
    }

    rendered = body
    for k, v in replacements.items():
        rendered = rendered.replace(k, str(v))

    stale = _is_template_stale(tpl["last_verified_date"])

    # 写入点击日志（埋点 —— 对应 D 节红线 3）
    _log_ai_entry_click("theme_analysis", seed_code)

    return {
        "prompt": rendered,
        "version": tpl["version"],
        "last_verified_date": tpl["last_verified_date"],
        "stale": stale,
        "char_count": len(rendered),
        "data_sections": {
            "holdings_count": holdings["count"],
            "peers_count": peers["count"],
            "peers_note": peers.get("note"),
            "holdings_report_date": holdings["report_date"],
        },
    }


# ─── AI 入口点击埋点 ───────────────────────────────────────────────

_AI_ENTRY_LOG = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "logs", "ai_entry_clicks.log"
)


def _log_ai_entry_click(template_name: str, payload: str = "") -> None:
    """极简 append 埋点，不做分析。红线 3 要求上线 1 周后主动回看。"""
    try:
        os.makedirs(os.path.dirname(_AI_ENTRY_LOG), exist_ok=True)
        with open(_AI_ENTRY_LOG, "a", encoding="utf-8") as f:
            f.write(f"{datetime.now().isoformat()}\t{template_name}\t{payload}\n")
    except Exception:
        pass
