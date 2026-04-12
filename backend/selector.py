from __future__ import annotations

import contextlib
import io
import json
import sqlite3
import uuid
from datetime import datetime
from typing import Any, Dict, List, Optional, Set

from backend.database import get_connection
from backend.fetcher import fetch_fund_holdings
from backend.holdings_analysis import get_holdings_overlap, get_industry_breakdown
from backend.scoring import calculate_all_scores

QUESTIONS = [
    {
        "id": "q1",
        "text": "这次加仓主要想补什么？",
        "options": [
            {"value": "a", "label": "让工具判断（按缺口诊断）"},
            {"value": "b", "label": "新能源或科技成长"},
            {"value": "c", "label": "消费或医药防御"},
            {"value": "d", "label": "出海资产（港股/美股）"},
        ],
    },
    {
        "id": "q2",
        "text": "能接受的最大单日跌幅？",
        "options": [
            {"value": "a", "label": "1% 左右"},
            {"value": "b", "label": "2-3%"},
            {"value": "c", "label": "3-5%"},
            {"value": "d", "label": "5% 以上"},
        ],
    },
    {
        "id": "q3",
        "text": "这笔钱多久不会动？",
        "options": [
            {"value": "a", "label": "1 年内"},
            {"value": "b", "label": "1-3 年"},
            {"value": "c", "label": "3-5 年"},
            {"value": "d", "label": "5 年以上"},
        ],
    },
    {
        "id": "q4",
        "text": "对重复持仓的容忍度？",
        "options": [
            {"value": "a", "label": "完全不要和我现有基金重仓同一只股票"},
            {"value": "b", "label": "重叠 30% 以内可以"},
            {"value": "c", "label": "无所谓，只要基金好"},
        ],
    },
]

_NEUTRAL_INDUSTRIES = ["科技", "消费", "医药", "金融", "新能源", "军工", "红利", "出海", "宽基", "其他"]
_INDUSTRY_KEYWORDS = {
    "科技": [
        "科技",
        "半导体",
        "电子",
        "通信",
        "计算机",
        "软件",
        "互联网",
        "元件",
        "光模块",
        "传媒",
        "广告营销",
        "消费电子",
    ],
    "消费": [
        "消费",
        "食品",
        "饮料",
        "家电",
        "家居",
        "零售",
        "商贸",
        "酒店",
        "旅游",
        "餐饮",
        "服饰",
        "农业",
        "养殖",
        "乳业",
        "白色家电",
    ],
    "医药": ["医药", "医疗", "生物", "制药", "疫苗", "器械", "中药", "创新药", "健康"],
    "金融": ["金融", "银行", "证券", "保险", "多元金融", "地产", "房地产"],
    "新能源": ["新能源", "电池", "光伏", "风电", "储能", "锂", "汽车零部件", "新能源车"],
    "军工": ["军工", "航空", "航天", "兵器", "船舶", "国防"],
    "红利": ["红利", "煤炭", "石油", "天然气", "公用事业", "电力", "运营商", "高股息", "港口"],
    "出海": ["港股", "美股", "全球", "海外", "出海"],
    "宽基": ["沪深300", "中证500", "中证1000", "创业板", "科创50", "宽基", "指数"],
}


def _safe_float(value: Any, default: float = 0.0) -> float:
    try:
        if value is None:
            return default
        return float(value)
    except (TypeError, ValueError):
        return default


def _round_float(value: Optional[float], digits: int = 4) -> Optional[float]:
    if value is None:
        return None
    return round(float(value), digits)


def _format_value(value: Any, digits: int = 1) -> str:
    if value is None:
        return "N/A"
    number = _safe_float(value, default=float("nan"))
    if number != number:
        return "N/A"
    if digits == 0:
        return str(int(round(number)))
    text = f"{number:.{digits}f}"
    return text.rstrip("0").rstrip(".")


def _bucket_industry(industry_name: Optional[str]) -> str:
    if not industry_name:
        return "其他"
    name = str(industry_name)
    for bucket, keywords in _INDUSTRY_KEYWORDS.items():
        for keyword in keywords:
            if keyword in name:
                return bucket
    return "其他"


def _manager_tenure_years(manager_since: Optional[str]) -> Optional[float]:
    if not manager_since:
        return None
    try:
        start = datetime.fromisoformat(manager_since).date()
    except ValueError:
        try:
            start = datetime.strptime(manager_since, "%Y-%m-%d").date()
        except ValueError:
            return None
    days = (datetime.now().date() - start).days
    return round(days / 365.25, 1) if days >= 0 else None


def _load_profile_rank_rows(conn: sqlite3.Connection, codes: List[str]) -> Dict[str, Dict[str, Any]]:
    if not codes:
        return {}
    placeholders = ",".join("?" for _ in codes)
    rows = conn.execute(
        f"""
        SELECT
            c.code AS code,
            COALESCE(fp.name, f.name, c.code) AS name,
            COALESCE(fp.category, f.category) AS category,
            fp.theme_tags,
            fp.region_tag,
            COALESCE(fp.aum, f.aum) AS aum,
            COALESCE(fp.inception_date, f.inception_date) AS inception_date,
            COALESCE(fp.manager_since, f.manager_since) AS manager_since,
            fp.top_industry,
            fp.top_industry_weight,
            fp.profile_source,
            pr.pct_1y,
            pr.rank_1y,
            pr.peer_total,
            fs.total_score,
            fs.sharpe_raw
        FROM (
            SELECT code FROM fund_profile WHERE code IN ({placeholders})
            UNION
            SELECT code FROM funds WHERE code IN ({placeholders})
        ) c
        LEFT JOIN fund_profile fp ON fp.code = c.code
        LEFT JOIN funds f ON f.code = c.code
        LEFT JOIN fund_peer_ranks pr ON pr.code = c.code
        LEFT JOIN (
            SELECT s1.*
            FROM fund_scores s1
            JOIN (
                SELECT code, MAX(date) AS max_date
                FROM fund_scores
                WHERE code IN ({placeholders})
                GROUP BY code
            ) latest
            ON latest.code = s1.code AND latest.max_date = s1.date
        ) fs ON fs.code = c.code
        """,
        tuple(codes + codes + codes),
    ).fetchall()
    return {row["code"]: dict(row) for row in rows}


def _get_user_current_stock_set(conn: sqlite3.Connection) -> Set[str]:
    rows = conn.execute(
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
        JOIN funds f ON f.code = h.code
        WHERE f.category IN ('equity', 'mixed', 'qdii')
        """
    ).fetchall()
    return {str(row["stock_code"]) for row in rows if row["stock_code"]}


def _get_candidate_holdings_map(conn: sqlite3.Connection, codes: List[str]) -> Dict[str, Set[str]]:
    if not codes:
        return {}
    placeholders = ",".join("?" for _ in codes)
    rows = conn.execute(
        f"""
        SELECT fh.code, fh.stock_code
        FROM fund_holdings fh
        JOIN (
            SELECT code, MAX(report_date) AS report_date
            FROM fund_holdings
            WHERE code IN ({placeholders})
            GROUP BY code
        ) latest
          ON latest.code = fh.code AND latest.report_date = fh.report_date
        WHERE fh.code IN ({placeholders})
        """,
        tuple(codes + codes),
    ).fetchall()
    holdings_map: Dict[str, Set[str]] = {}
    for row in rows:
        code = str(row["code"])
        holdings_map.setdefault(code, set()).add(str(row["stock_code"]))
    return holdings_map


def _get_portfolio_snapshot() -> Dict[str, Any]:
    conn = get_connection()
    try:
        rows = conn.execute(
            """
            SELECT
                f.category,
                SUM(COALESCE(h.shares * n.nav, h.cost_amount, 0)) AS market_value
            FROM holdings h
            JOIN funds f ON f.code = h.code
            LEFT JOIN (
                SELECT nh1.code, nh1.nav
                FROM nav_history nh1
                JOIN (
                    SELECT code, MAX(date) AS max_date
                    FROM nav_history
                    GROUP BY code
                ) latest
                  ON latest.code = nh1.code AND latest.max_date = nh1.date
            ) n ON n.code = h.code
            GROUP BY f.category
            """
        ).fetchall()
    finally:
        conn.close()

    bucket_values = {"bond": 0.0, "equity": 0.0, "qdii": 0.0}
    total_value = 0.0
    for row in rows:
        category = row["category"]
        value = _safe_float(row["market_value"])
        total_value += value
        if category == "bond":
            bucket_values["bond"] += value
        elif category == "qdii":
            bucket_values["qdii"] += value
        else:
            bucket_values["equity"] += value

    allocation = {
        key: round(value / total_value * 100, 2) if total_value > 0 else 0.0
        for key, value in bucket_values.items()
    }
    return {"total_value": round(total_value, 2), "allocation": allocation}


def _upsert_profile_from_holdings(conn: sqlite3.Connection, code: str) -> Dict[str, Any]:
    latest = conn.execute(
        "SELECT MAX(report_date) AS report_date FROM fund_holdings WHERE code=?",
        (code,),
    ).fetchone()
    if not latest or not latest["report_date"]:
        return {}

    report_date = latest["report_date"]
    industry_row = conn.execute(
        """
        SELECT industry, SUM(COALESCE(weight, 0)) AS total_weight
        FROM fund_holdings
        WHERE code=? AND report_date=? AND industry IS NOT NULL AND TRIM(industry) <> ''
        GROUP BY industry
        ORDER BY total_weight DESC, industry ASC
        LIMIT 1
        """,
        (code, report_date),
    ).fetchone()

    current = conn.execute(
        "SELECT name, top_industry, top_industry_weight FROM fund_profile WHERE code=?",
        (code,),
    ).fetchone()
    top_industry = industry_row["industry"] if industry_row else (current["top_industry"] if current else None)
    top_industry_weight = (
        _safe_float(industry_row["total_weight"])
        if industry_row
        else (current["top_industry_weight"] if current else None)
    )
    if current:
        conn.execute(
            """
            UPDATE fund_profile
            SET top_industry=?,
                top_industry_weight=?,
                profile_source='pierced',
                updated_at=?
            WHERE code=?
            """,
            (top_industry, top_industry_weight, datetime.now().isoformat(), code),
        )
    else:
        fund_row = conn.execute("SELECT name FROM funds WHERE code=?", (code,)).fetchone()
        conn.execute(
            """
            INSERT INTO fund_profile
            (code, name, top_industry, top_industry_weight, profile_source, updated_at)
            VALUES (?, ?, ?, ?, 'pierced', ?)
            """,
            (
                code,
                str((fund_row["name"] if fund_row and fund_row["name"] else code)),
                top_industry,
                top_industry_weight,
                datetime.now().isoformat(),
            ),
        )
    conn.commit()
    refreshed = conn.execute(
        """
        SELECT top_industry, top_industry_weight, profile_source
        FROM fund_profile
        WHERE code=?
        """,
        (code,),
    ).fetchone()
    return dict(refreshed) if refreshed else {}


def _update_candidate_top_industry_fragment(candidate: Dict[str, Any], top_industry: Optional[str], top_industry_weight: Any) -> None:
    if not top_industry:
        return
    fragments = candidate.get("reason_fragments")
    if not isinstance(fragments, list):
        fragments = []
        candidate["reason_fragments"] = fragments

    fragment = f"top_industry {top_industry} ({_format_value(top_industry_weight)}%)"
    for index, item in enumerate(fragments):
        if str(item).startswith("top_industry "):
            fragments[index] = fragment
            break
    else:
        fragments.append(fragment)

    candidate["profile_source"] = "pierced"


def _lazy_pierce_candidate_holdings(candidates: List[Dict[str, Any]]) -> None:
    if not candidates:
        return

    conn = get_connection()
    try:
        for candidate in candidates:
            code = str(candidate.get("code") or "").strip()
            if not code:
                continue

            count_row = conn.execute(
                "SELECT COUNT(*) AS cnt FROM fund_holdings WHERE code=?",
                (code,),
            ).fetchone()
            if int(count_row["cnt"]) > 0:
                continue

            try:
                with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                    fetch_fund_holdings(code, save=True)
            except Exception:
                continue

            profile_row = conn.execute(
                """
                SELECT top_industry, top_industry_weight, profile_source
                FROM fund_profile
                WHERE code=?
                """,
                (code,),
            ).fetchone()
            profile_data = dict(profile_row) if profile_row else {}

            if not profile_data.get("top_industry") or profile_data.get("profile_source") != "pierced":
                try:
                    profile_data = _upsert_profile_from_holdings(conn, code)
                except Exception:
                    continue

            if profile_data.get("top_industry"):
                _update_candidate_top_industry_fragment(
                    candidate,
                    profile_data.get("top_industry"),
                    profile_data.get("top_industry_weight"),
                )
    finally:
        conn.close()


def _normalize_cached_candidates_payload(raw_payload: str) -> Dict[str, Any]:
    try:
        parsed = json.loads(raw_payload)
    except (TypeError, json.JSONDecodeError):
        return {"candidates": [], "portfolio_snapshot": None}

    if isinstance(parsed, dict):
        candidates = parsed.get("candidates") or []
        portfolio_snapshot = parsed.get("portfolio_snapshot")
        return {
            "candidates": candidates if isinstance(candidates, list) else [],
            "portfolio_snapshot": portfolio_snapshot if isinstance(portfolio_snapshot, dict) else None,
        }

    if isinstance(parsed, list):
        return {"candidates": parsed, "portfolio_snapshot": None}

    return {"candidates": [], "portfolio_snapshot": None}


def _get_answer_label(question: Dict[str, Any], selected_value: Any) -> str:
    selected = str(selected_value or "")
    for option in question.get("options") or []:
        if option.get("value") == selected:
            return str(option.get("label") or selected or "未作答")
    return selected or "未作答"


def diagnose_portfolio_gaps() -> Dict[str, Any]:
    industry_breakdown = get_industry_breakdown() or {}
    industries = industry_breakdown.get("industries") or []
    if not industries:
        return {"gaps": [], "portfolio_summary": {}, "note": "暂无权益基金持仓穿透数据"}

    overlap_data = get_holdings_overlap() or {}
    diversification_score = _safe_float(overlap_data.get("diversification_score"), 1.0)
    overlap_stocks = overlap_data.get("overlap_stocks") or []

    bucket_weights: Dict[str, float] = {name: 0.0 for name in _NEUTRAL_INDUSTRIES}
    for item in industries:
        bucket = _bucket_industry(item.get("industry"))
        bucket_weights[bucket] = bucket_weights.get(bucket, 0.0) + _safe_float(item.get("total_weight"))

    neutral_weight = 10.0
    gaps: List[Dict[str, Any]] = []
    for bucket in _NEUTRAL_INDUSTRIES:
        current_weight = round(bucket_weights.get(bucket, 0.0), 2)
        if current_weight < neutral_weight - 5.0:
            priority = min(1.0, max(0.0, (neutral_weight - current_weight) / neutral_weight))
            gaps.append(
                {
                    "type": "industry",
                    "name": bucket,
                    "current_pct": current_weight,
                    "suggest_add_pct": round(max(0.0, neutral_weight - current_weight), 2),
                    "priority": round(priority, 4),
                }
            )

    conn = get_connection()
    try:
        qdii_row = conn.execute(
            """
            SELECT COUNT(*) AS cnt
            FROM holdings h
            JOIN funds f ON f.code = h.code
            WHERE f.category = 'qdii'
            """
        ).fetchone()
    finally:
        conn.close()

    if int(qdii_row["cnt"]) == 0:
        gaps.append(
            {
                "type": "region",
                "name": "出海资产",
                "current_pct": 0,
                "suggest_add_pct": 10,
                "priority": 0.7,
            }
        )

    max_overlap = max((_safe_float(item.get("total_exposure_pct")) for item in overlap_stocks), default=0.0)
    if diversification_score < 2.5 or max_overlap > 6.0:
        gaps.append(
            {
                "type": "concentration",
                "name": "分散化",
                "current_pct": diversification_score,
                "suggest_add_pct": 0,
                "priority": 0.8,
            }
        )

    gaps.sort(key=lambda item: item.get("priority", 0.0), reverse=True)
    top_overlap_stock = overlap_stocks[0]["stock_name"] if overlap_stocks else None
    return {
        "gaps": gaps,
        "portfolio_summary": {
            "diversification_score": diversification_score,
            "top_overlap_stock": top_overlap_stock,
        },
    }


def build_candidate_pool(filters: Dict[str, Any]) -> List[str]:
    filters = dict(filters or {})
    categories = list(filters.get("categories") or ["equity", "mixed"])
    theme_tags = list(filters.get("theme_tags") or [])
    region_tags = list(filters.get("region_tags") or [])
    min_aum = _safe_float(filters.get("min_aum"), 2.0)
    max_aum = _safe_float(filters.get("max_aum"), 500.0)
    min_inception_years = int(filters.get("min_inception_years") or 2)
    max_pct_1y = _safe_float(filters.get("max_pct_1y"), 50.0)

    clauses = ["1=1"]
    params: List[Any] = []

    if categories:
        placeholders = ",".join("?" for _ in categories)
        clauses.append(f"COALESCE(fp.category, f.category) IN ({placeholders})")
        params.extend(categories)

    clauses.append("(COALESCE(fp.aum, f.aum) IS NULL OR COALESCE(fp.aum, f.aum) >= ?)")
    params.append(min_aum)
    clauses.append("(COALESCE(fp.aum, f.aum) IS NULL OR COALESCE(fp.aum, f.aum) <= ?)")
    params.append(max_aum)

    if min_inception_years > 0:
        clauses.append(
            "("
            "COALESCE(fp.inception_date, f.inception_date) IS NULL "
            "OR COALESCE(fp.inception_date, f.inception_date) < date('now', ?)"
            ")"
        )
        params.append(f"-{min_inception_years} years")

    clauses.append("(pr.pct_1y IS NULL OR pr.pct_1y <= ?)")
    params.append(max_pct_1y)

    if theme_tags:
        like_clauses = []
        for tag in theme_tags:
            like_clauses.append("fp.theme_tags LIKE ?")
            params.append(f"%{tag}%")
        clauses.append("(" + " OR ".join(like_clauses) + ")")

    if region_tags:
        placeholders = ",".join("?" for _ in region_tags)
        clauses.append(f"fp.region_tag IN ({placeholders})")
        params.extend(region_tags)

    order_sql = "CASE WHEN pr.pct_1y IS NULL THEN 1 ELSE 0 END, pr.pct_1y ASC, fp.code ASC"
    sql = f"""
        SELECT fp.code
        FROM fund_profile fp
        LEFT JOIN funds f ON f.code = fp.code
        LEFT JOIN fund_peer_ranks pr ON pr.code = fp.code
        WHERE {' AND '.join(clauses)}
        ORDER BY {order_sql}
        LIMIT 300
    """

    conn = get_connection()
    try:
        rows = conn.execute(sql, tuple(params)).fetchall()
    finally:
        conn.close()
    return [str(row["code"]) for row in rows if row["code"]]


def score_candidates_for_gap(gap: Dict[str, Any], pool: List[str]) -> List[Dict[str, Any]]:
    if not pool:
        return []

    score_rows = calculate_all_scores(codes=pool, fallback_to_peer_rank=True)
    score_lookup: Dict[str, Dict[str, Any]] = {row["code"]: row for row in score_rows}

    conn = get_connection()
    try:
        profile_lookup = _load_profile_rank_rows(conn, pool)
        current_stock_set = _get_user_current_stock_set(conn)
        candidate_holdings_map = _get_candidate_holdings_map(conn, pool)
    finally:
        conn.close()

    ranked: List[Dict[str, Any]] = []
    gap_type = gap.get("type")
    gap_name = str(gap.get("name") or "")

    for code in pool:
        profile = profile_lookup.get(code, {})
        pct_1y = profile.get("pct_1y")
        theme_tags = str(profile.get("theme_tags") or "")
        region_tag = str(profile.get("region_tag") or "")
        profile_source = str(profile.get("profile_source") or "")

        if gap_type == "industry":
            if profile.get("top_industry") == gap_name and profile_source == "pierced":
                gap_fit = 1.0
            elif gap_name and gap_name in theme_tags:
                gap_fit = 0.7
            else:
                gap_fit = 0.3
        elif gap_type == "region":
            gap_fit = 1.0 if region_tag in ("港股", "美股", "全球") else 0.2
        elif gap_type == "concentration":
            if pct_1y is not None:
                gap_fit = max(0.0, (50.0 - _safe_float(pct_1y)) / 50.0)
            else:
                gap_fit = 0.5
        else:
            gap_fit = 0.3

        quality_row = score_lookup.get(code, {})
        total_score = quality_row.get("total_score")
        quality_score = _safe_float(total_score, 50.0) / 100.0

        if pct_1y is not None:
            peer_rank_score = (100.0 - _safe_float(pct_1y)) / 100.0
        else:
            peer_rank_score = 0.5

        composite = 0.45 * gap_fit + 0.35 * quality_score + 0.20 * peer_rank_score

        candidate_stocks = candidate_holdings_map.get(code)
        if candidate_stocks:
            overlap_rate = len(candidate_stocks & current_stock_set) / float(len(candidate_stocks))
        else:
            overlap_rate = None

        top_industry = profile.get("top_industry")
        top_industry_weight = profile.get("top_industry_weight")
        if top_industry:
            top_industry_fragment = f"top_industry {top_industry} ({_format_value(top_industry_weight)}%)"
        else:
            top_industry_fragment = "top_industry N/A"

        if pct_1y is not None:
            rank_fragment = f"1年同类 Top {_format_value(pct_1y)}%"
        else:
            rank_fragment = "1年同类 Top N/A"

        sharpe_fragment = f"夏普 {_format_value(quality_row.get('sharpe_raw'))}"
        aum_fragment = f"规模 {_format_value(profile.get('aum'))}亿"
        manager_years = _manager_tenure_years(profile.get("manager_since"))
        manager_fragment = f"经理任职 {_format_value(manager_years)}年" if manager_years is not None else "经理任职 N/A"

        ranked.append(
            {
                "code": code,
                "name": str(profile.get("name") or code),
                "composite": round(composite, 4),
                "gap_fit": round(gap_fit, 4),
                "quality_score": round(quality_score, 4),
                "peer_rank_score": round(peer_rank_score, 4),
                "overlap_rate": _round_float(overlap_rate, 4),
                "reason_fragments": [
                    top_industry_fragment,
                    rank_fragment,
                    sharpe_fragment,
                    aum_fragment,
                    manager_fragment,
                ],
                "profile_source": profile_source,
            }
        )

    ranked.sort(key=lambda item: item["composite"], reverse=True)
    return ranked[:10]


def generate_recommendation(user_answers: Dict[str, str]) -> Dict[str, Any]:
    diagnosis = diagnose_portfolio_gaps()
    session_id = str(uuid.uuid4())
    gaps = [dict(item) for item in diagnosis.get("gaps") or []]

    if not gaps:
        return {
            "session_id": session_id,
            "gaps": [],
            "candidates": [],
            "note": "暂无持仓穿透数据，无法诊断缺口",
        }

    q1 = user_answers.get("q1", "a")
    q2 = user_answers.get("q2", "b")
    q3 = user_answers.get("q3", "b")
    q4 = user_answers.get("q4", "b")

    boost_targets = {
        "b": {"names": {"新能源", "科技"}, "type": None},
        "c": {"names": {"消费", "医药"}, "type": None},
        "d": {"names": set(), "type": "region"},
    }
    if q1 in boost_targets:
        rule = boost_targets[q1]
        for gap in gaps:
            should_boost = False
            if rule["type"] and gap.get("type") == rule["type"]:
                should_boost = True
            if rule["names"] and gap.get("name") in rule["names"]:
                should_boost = True
            if should_boost:
                gap["priority"] = round(min(1.0, _safe_float(gap.get("priority")) + 0.5), 4)

    gaps.sort(key=lambda item: item.get("priority", 0.0), reverse=True)

    filters: Dict[str, Any] = {
        "theme_tags": [],
        "region_tags": [],
        "min_aum": 2.0,
        "max_aum": 500.0,
        "q3": q3,
    }

    q2_mapping = {
        "a": {"categories": ["bond", "mixed"], "max_daily_drawdown_hint": 1.0},
        "b": {"categories": ["equity", "mixed", "index"], "max_daily_drawdown_hint": 3.0},
        "c": {"categories": ["equity", "mixed", "index"], "max_daily_drawdown_hint": 5.0},
        "d": {"categories": ["equity", "qdii"], "max_daily_drawdown_hint": 8.0},
    }
    filters.update(q2_mapping.get(q2, q2_mapping["b"]))

    q3_mapping = {
        "a": {"min_inception_years": 1, "max_pct_1y": 50.0},
        "b": {"min_inception_years": 2, "max_pct_1y": 50.0},
        "c": {"min_inception_years": 2, "max_pct_1y": 40.0},
        "d": {"min_inception_years": 3, "max_pct_1y": 40.0},
    }
    filters.update(q3_mapping.get(q3, q3_mapping["b"]))

    overlap_threshold_mapping = {"a": 0.20, "b": 0.50, "c": 1.01}
    overlap_threshold = overlap_threshold_mapping.get(q4, 0.50)

    top_gaps = gaps[:2]
    theme_tags = list(filters.get("theme_tags") or [])
    region_tags = list(filters.get("region_tags") or [])
    for gap in top_gaps:
        if gap.get("type") == "industry" and gap.get("name") not in ("其他", "分散化"):
            theme_tags.append(str(gap.get("name")))
        elif gap.get("type") == "region":
            for tag in ("港股", "美股", "全球"):
                if tag not in region_tags:
                    region_tags.append(tag)
    filters["theme_tags"] = list(dict.fromkeys(theme_tags))
    filters["region_tags"] = list(dict.fromkeys(region_tags))

    pool = build_candidate_pool(filters)

    merged: Dict[str, Dict[str, Any]] = {}
    for gap in top_gaps:
        for candidate in score_candidates_for_gap(gap, pool):
            current = merged.get(candidate["code"])
            if current is None or candidate["composite"] > current["composite"]:
                merged[candidate["code"]] = candidate

    candidates = sorted(merged.values(), key=lambda item: item["composite"], reverse=True)
    candidates = candidates[:6]
    candidates = [
        candidate
        for candidate in candidates
        if candidate["overlap_rate"] is None or candidate["overlap_rate"] <= overlap_threshold
    ]

    warnings: List[str] = []
    if q1 in ("b", "c", "d") and q2 == "a":
        warnings.append("你的主题偏好偏进攻，但风险承受设置偏保守，推荐结果已尽量在两者之间折中。")

    portfolio_snapshot = _get_portfolio_snapshot()
    _lazy_pierce_candidate_holdings(candidates)

    conn = get_connection()
    try:
        try:
            conn.execute(
                """
                INSERT OR REPLACE INTO selector_cache
                (session_id, answers_json, gaps_json, candidates_json, created_at)
                VALUES (?, ?, ?, ?, ?)
                """,
                (
                    session_id,
                    json.dumps(user_answers, ensure_ascii=False),
                    json.dumps(gaps, ensure_ascii=False),
                    json.dumps(
                        {
                            "candidates": candidates,
                            "portfolio_snapshot": portfolio_snapshot,
                        },
                        ensure_ascii=False,
                    ),
                    datetime.now().isoformat(),
                ),
            )
            conn.commit()
        except sqlite3.OperationalError:
            conn.rollback()
    finally:
        conn.close()

    return {
        "session_id": session_id,
        "gaps": gaps,
        "candidates": candidates,
        "portfolio_snapshot": portfolio_snapshot,
        "warnings": warnings,
    }


def export_prompt_for_claude(session_id: str) -> str:
    conn = get_connection()
    try:
        row = conn.execute(
            """
            SELECT answers_json, gaps_json, candidates_json
            FROM selector_cache
            WHERE session_id=?
            """,
            (session_id,),
        ).fetchone()
    finally:
        conn.close()

    if not row:
        return f"错误：找不到会话 {session_id}"

    try:
        answers = json.loads(row["answers_json"] or "{}")
    except (TypeError, json.JSONDecodeError):
        answers = {}
    try:
        gaps = json.loads(row["gaps_json"] or "[]")
    except (TypeError, json.JSONDecodeError):
        gaps = []

    cached_payload = _normalize_cached_candidates_payload(row["candidates_json"])
    candidates = cached_payload["candidates"]
    portfolio_snapshot = cached_payload["portfolio_snapshot"] or _get_portfolio_snapshot()

    overlap_data = get_holdings_overlap() or {}
    overlap_stocks = overlap_data.get("overlap_stocks") or []
    top_overlap_names = [str(item.get("stock_name") or "").strip() for item in overlap_stocks]
    top_overlap_names = [name for name in top_overlap_names if name][:3]
    overlap_text = "、".join(top_overlap_names) if top_overlap_names else "暂无数据"

    answer_lines: List[str] = []
    for index, question in enumerate(QUESTIONS, start=1):
        qid = question["id"]
        answer_lines.append(
            f"Q{index}（{question['text']}）：{_get_answer_label(question, answers.get(qid))}"
        )

    gap_lines: List[str] = []
    for gap in (gaps or [])[:3]:
        gap_lines.append(
            "- {name}：当前 {current_pct:.1f}%，建议补充至 {suggest_add_pct:.1f}%（优先级 {priority:.2f}）".format(
                name=str(gap.get("name") or "未知"),
                current_pct=_safe_float(gap.get("current_pct")),
                suggest_add_pct=_safe_float(gap.get("suggest_add_pct")),
                priority=_safe_float(gap.get("priority")),
            )
        )
    if not gap_lines:
        gap_lines.append("- 暂无数据")

    candidate_codes = [str(item.get("code") or "").strip() for item in candidates if item.get("code")]
    profile_lookup: Dict[str, Dict[str, Any]] = {}
    if candidate_codes:
        conn = get_connection()
        try:
            profile_lookup = _load_profile_rank_rows(conn, candidate_codes)
        finally:
            conn.close()

    candidate_rows: List[str] = []
    for candidate in candidates:
        code = str(candidate.get("code") or "")
        profile = profile_lookup.get(code, {})
        manager_years = _manager_tenure_years(profile.get("manager_since"))
        pct_1y_text = "N/A" if profile.get("pct_1y") is None else f"{_safe_float(profile.get('pct_1y')):.2f}%"
        aum_text = "N/A" if profile.get("aum") is None else f"{_safe_float(profile.get('aum')):.2f}亿"
        manager_text = "N/A" if manager_years is None else f"{manager_years:.1f}年"
        candidate_rows.append(
            "| {code} | {name} | {composite:.2f} | {gap_fit:.2f} | {pct_1y} | {aum} | {manager} |".format(
                code=code or "-",
                name=str(candidate.get("name") or code or "-"),
                composite=_safe_float(candidate.get("composite")),
                gap_fit=_safe_float(candidate.get("gap_fit")),
                pct_1y=pct_1y_text,
                aum=aum_text,
                manager=manager_text,
            )
        )

    allocation = portfolio_snapshot.get("allocation") if isinstance(portfolio_snapshot, dict) else {}
    total_value = _safe_float(
        portfolio_snapshot.get("total_value") if isinstance(portfolio_snapshot, dict) else None
    )
    current_date = datetime.now().strftime("%Y-%m-%d")

    sections = [
        "# 我的基金持仓决策咨询",
        "",
        "## 持仓快照",
        f"- 总资产：{total_value:.2f} 元",
        "- 配置：债基 {bond:.2f}%，权益基金 {equity:.2f}%，QDII {qdii:.2f}%".format(
            bond=_safe_float((allocation or {}).get("bond")),
            equity=_safe_float((allocation or {}).get("equity")),
            qdii=_safe_float((allocation or {}).get("qdii")),
        ),
        f"- 前十大持仓重叠股票：{overlap_text}",
        "",
        "## 系统诊断的缺口（Top 3）",
        *gap_lines,
        "",
        "## 我的问诊回答",
        *answer_lines,
        "",
        "## 系统筛出的候选基金",
        "| 基金代码 | 基金名称 | 综合分 | 缺口适配 | 同类百分位 | 规模 | 经理任职 |",
        "|---|---|---|---|---|---|---|",
        *(candidate_rows or ["| 暂无数据 | 暂无数据 | 0.00 | 0.00 | N/A | N/A | N/A |"]),
        "",
        "---",
        f"*数据来源：基金季报+天天基金API，截至 {current_date}。以上为系统分析，不构成投资建议。*",
        "",
        "## 我的问题",
        "（请在此处填写你想问 AI 的问题）",
    ]
    return "\n".join(sections)


# ─── v2.3 Track A: 主题反查 ────────────────────────────────────────────

def _parse_theme_tags(raw: Any) -> List[str]:
    """fund_profile.theme_tags 存的是 JSON 数组字符串，解析成 list"""
    if not raw:
        return []
    if isinstance(raw, list):
        return [str(t).strip() for t in raw if str(t).strip()]
    try:
        parsed = json.loads(raw)
        if isinstance(parsed, list):
            return [str(t).strip() for t in parsed if str(t).strip()]
    except (TypeError, json.JSONDecodeError):
        pass
    # 不是 JSON？当作逗号分隔字符串降级
    return [s.strip() for s in str(raw).split(",") if s.strip()]


def _infer_theme_from_holdings(conn: sqlite3.Connection, code: str) -> Optional[str]:
    """
    当 fund_profile 没有 theme_tags 和 top_industry 时，用 fund_holdings 的 industry
    字段聚合，按 _bucket_industry 桶化，返回权重最高的主题桶名（比如"科技"）。
    依赖 _INDUSTRY_KEYWORDS 做模糊匹配。
    """
    rows = conn.execute(
        "SELECT industry, weight FROM fund_holdings "
        "WHERE code=? AND industry IS NOT NULL AND weight IS NOT NULL",
        (code,),
    ).fetchall()
    if not rows:
        return None
    bucket_weights: Dict[str, float] = {}
    for r in rows:
        bucket = _bucket_industry(r["industry"])
        if bucket == "其他":
            continue
        bucket_weights[bucket] = bucket_weights.get(bucket, 0.0) + float(r["weight"] or 0)
    if not bucket_weights:
        return None
    # 按权重降序，取第一个
    return max(bucket_weights.items(), key=lambda kv: kv[1])[0]


def _nav_return_from_offset(conn: sqlite3.Connection, code: str, offset_days: int) -> Optional[float]:
    """用 nav_history 里 OFFSET=offset_days 的 acc_nav 估算近 N 天收益率"""
    latest = conn.execute(
        "SELECT acc_nav FROM nav_history WHERE code=? AND acc_nav IS NOT NULL "
        "ORDER BY date DESC LIMIT 1",
        (code,),
    ).fetchone()
    if not latest or latest["acc_nav"] in (None, 0):
        return None
    past = conn.execute(
        "SELECT acc_nav FROM nav_history WHERE code=? AND acc_nav IS NOT NULL "
        "ORDER BY date DESC LIMIT 1 OFFSET ?",
        (code, offset_days),
    ).fetchone()
    if not past or past["acc_nav"] in (None, 0):
        return None
    try:
        return round(float(latest["acc_nav"]) / float(past["acc_nav"]) - 1, 4)
    except (TypeError, ValueError, ZeroDivisionError):
        return None


def find_peer_funds_by_theme(seed_code: str, limit: int = 10) -> Dict[str, Any]:
    """
    给定 seed 基金，返回同主题的 peer 基金列表（用 fund_profile.theme_tags 匹配）。
    数据不够时返回 note 字段而不是 500。

    Returns:
        {
            "seed": {"code", "name", "theme_tags": [...], "top_industry": ...},
            "matched_on": "theme_tags" | "top_industry" | None,
            "matched_tags": [...],
            "peers": [...],
            "note": Optional[str],
        }
    """
    seed_code = str(seed_code or "").strip()
    if not seed_code:
        return {"seed": None, "matched_on": None, "matched_tags": [], "peers": [], "note": "seed code 为空"}

    conn = get_connection()
    try:
        seed_row = conn.execute(
            """
            SELECT fp.code, COALESCE(fp.name, f.name, fp.code) AS name,
                   fp.theme_tags, fp.top_industry, COALESCE(fp.category, f.category) AS category
            FROM fund_profile fp
            LEFT JOIN funds f ON f.code = fp.code
            WHERE fp.code = ?
            """,
            (seed_code,),
        ).fetchone()

        if not seed_row:
            # 没有 profile，降级到 funds 表基本信息
            fallback = conn.execute(
                "SELECT code, name, category FROM funds WHERE code=?", (seed_code,)
            ).fetchone()
            seed_info = {
                "code": seed_code,
                "name": fallback["name"] if fallback else seed_code,
                "category": fallback["category"] if fallback else None,
                "theme_tags": [],
                "top_industry": None,
            }
            return {
                "seed": seed_info,
                "matched_on": None,
                "matched_tags": [],
                "peers": [],
                "note": "该基金无 fund_profile 记录，无法反查主题 peer",
            }

        seed_tags = _parse_theme_tags(seed_row["theme_tags"])
        seed_top_industry = seed_row["top_industry"]
        seed_info = {
            "code": seed_row["code"],
            "name": seed_row["name"],
            "category": seed_row["category"],
            "theme_tags": seed_tags,
            "top_industry": seed_top_industry,
        }

        # 选匹配维度：优先 theme_tags，然后 top_industry，最后从 fund_holdings 聚合推断
        matched_on: Optional[str] = None
        matched_tags: List[str] = []
        like_clauses: List[str] = []
        like_params: List[str] = []
        fallback_note: Optional[str] = None

        if seed_tags:
            matched_on = "theme_tags"
            matched_tags = seed_tags
            for tag in seed_tags:
                like_clauses.append("fp.theme_tags LIKE ?")
                like_params.append(f"%{tag}%")
        elif seed_top_industry:
            matched_on = "top_industry"
            matched_tags = [seed_top_industry]
            like_clauses.append("(fp.top_industry = ? OR fp.theme_tags LIKE ?)")
            like_params.append(seed_top_industry)
            like_params.append(f"%{seed_top_industry}%")
        else:
            # 最后一层降级：从持股聚合推断主题桶
            inferred = _infer_theme_from_holdings(conn, seed_code)
            if inferred:
                matched_on = "inferred_from_holdings"
                matched_tags = [inferred]
                like_clauses.append("fp.theme_tags LIKE ?")
                like_params.append(f"%{inferred}%")
                seed_info["inferred_theme"] = inferred
                fallback_note = (
                    f"该基金未打主题标签，已从前十大持股聚合推断主题为「{inferred}」，"
                    "结果仅供参考（推断可能不精确）"
                )
            else:
                return {
                    "seed": seed_info,
                    "matched_on": None,
                    "matched_tags": [],
                    "peers": [],
                    "note": "该基金未打主题标签，也无可用持股数据来推断主题，无法反查 peer",
                }

        # 排除 seed 本身和当前持仓
        holdings_codes = [
            str(r["code"]) for r in conn.execute("SELECT code FROM holdings").fetchall()
        ]
        exclude_set = {seed_code, *holdings_codes}
        exclude_placeholders = ",".join("?" for _ in exclude_set) if exclude_set else ""
        exclude_sql = f"AND fp.code NOT IN ({exclude_placeholders})" if exclude_placeholders else ""

        sql = f"""
            SELECT fp.code
            FROM fund_profile fp
            LEFT JOIN fund_peer_ranks pr ON pr.code = fp.code
            WHERE ({' OR '.join(like_clauses)})
              {exclude_sql}
            ORDER BY
                CASE WHEN pr.pct_1y IS NULL THEN 1 ELSE 0 END,
                pr.pct_1y ASC,
                fp.code ASC
            LIMIT ?
        """
        params = tuple(like_params + list(exclude_set) + [max(1, int(limit) * 3)])
        pool_codes = [str(r["code"]) for r in conn.execute(sql, params).fetchall()]

        if not pool_codes:
            return {
                "seed": seed_info,
                "matched_on": matched_on,
                "matched_tags": matched_tags,
                "peers": [],
                "note": f"按主题 {matched_tags} 未找到同 tag 的其它基金",
            }

        profile_rows = _load_profile_rank_rows(conn, pool_codes)
        try:
            score_rows = calculate_all_scores(codes=pool_codes, fallback_to_peer_rank=True)
            score_lookup = {r["code"]: r for r in score_rows}
        except Exception:
            score_lookup = {}

        peers: List[Dict[str, Any]] = []
        for code in pool_codes:
            prof = profile_rows.get(code, {})
            if not prof:
                continue
            latest_nav_row = conn.execute(
                "SELECT nav FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 1", (code,)
            ).fetchone()
            latest_nav = float(latest_nav_row["nav"]) if latest_nav_row and latest_nav_row["nav"] else None

            score_snap = score_lookup.get(code, {})
            total_score = score_snap.get("total_score") if isinstance(score_snap, dict) else None

            peers.append({
                "code": code,
                "name": prof.get("name"),
                "category": prof.get("category"),
                "theme_tags": _parse_theme_tags(prof.get("theme_tags")),
                "pct_1y": prof.get("pct_1y"),
                "total_score": round(float(total_score), 1) if total_score is not None else None,
                "latest_nav": round(latest_nav, 4) if latest_nav is not None else None,
                "ret_1m": _nav_return_from_offset(conn, code, 21),
                "ret_3m": _nav_return_from_offset(conn, code, 63),
                "top_industry": prof.get("top_industry"),
            })

        # 最终按 pct_1y 升序 + total_score 降序排序，取 limit
        def _sort_key(p: Dict[str, Any]):
            pct = p.get("pct_1y")
            score = p.get("total_score") or 0
            return (
                1 if pct is None else 0,
                pct if pct is not None else 1.0,
                -score,
            )
        peers.sort(key=_sort_key)
        peers = peers[: max(1, int(limit))]

        return {
            "seed": seed_info,
            "matched_on": matched_on,
            "matched_tags": matched_tags,
            "peers": peers,
            "note": fallback_note,
        }
    finally:
        conn.close()
