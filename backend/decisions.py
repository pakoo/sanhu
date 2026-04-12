from __future__ import annotations

import json
import sqlite3
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional

from config import DB_PATH

VALID_DECISION_TYPES = {"buy", "sell", "rebalance", "hold"}
EXECUTED_STATUSES = {"executed", "triggered_tp", "triggered_sl"}
DECISION_TYPE_CN = {
    "buy": "买入",
    "sell": "卖出",
    "rebalance": "调仓",
    "hold": "持仓观察",
}


def _connect() -> sqlite3.Connection:
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row
    return conn


def _utc_now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def _safe_json_loads(raw: Any, default: Any) -> Any:
    if raw in (None, ""):
        return default
    try:
        return json.loads(raw)
    except (TypeError, json.JSONDecodeError):
        return default


def _fetch_selector_reason_fragments(conn: sqlite3.Connection, session_id: str, code: str) -> Optional[Any]:
    row = conn.execute(
        "SELECT candidates_json FROM selector_cache WHERE session_id=?",
        (session_id,),
    ).fetchone()
    if not row:
        return None

    payload = _safe_json_loads(row["candidates_json"], {})
    candidates = payload.get("candidates") if isinstance(payload, dict) else None
    if not isinstance(candidates, list):
        return None

    for candidate in candidates:
        if isinstance(candidate, dict) and str(candidate.get("code") or "") == code:
            return candidate.get("reason_fragments")
    return None


def _serialize_rationale(rationale: Dict[str, Any]) -> str:
    return json.dumps(rationale, ensure_ascii=False)


def _parse_rationale(raw: Any) -> Dict[str, Any]:
    parsed = _safe_json_loads(raw, {})
    return parsed if isinstance(parsed, dict) else {}


def _get_latest_nav(conn: sqlite3.Connection, code: str) -> Optional[float]:
    row = conn.execute(
        "SELECT nav FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 1",
        (code,),
    ).fetchone()
    if not row or row["nav"] is None:
        return None
    return float(row["nav"])


def _get_latest_nav_date(conn: sqlite3.Connection, code: str) -> Optional[str]:
    row = conn.execute(
        "SELECT date FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 1",
        (code,),
    ).fetchone()
    return row["date"] if row and row["date"] else None


def _get_nav_series(conn: sqlite3.Connection, code: str, start_date: str) -> List[sqlite3.Row]:
    return conn.execute(
        """
        SELECT date, nav
        FROM nav_history
        WHERE code=? AND date>=?
        ORDER BY date ASC
        """,
        (code, start_date),
    ).fetchall()


def _build_curve(rows: List[sqlite3.Row], baseline_nav: Optional[float]) -> List[dict]:
    if baseline_nav is None or baseline_nav <= 0:
        return []

    curve: List[dict] = []
    for row in rows:
        nav = row["nav"]
        if nav is None:
            pnl_pct = None
        else:
            pnl_pct = float(nav) / baseline_nav - 1
        curve.append(
            {
                "date": row["date"],
                "nav": float(nav) if nav is not None else None,
                "pnl_pct": pnl_pct,
            }
        )
    return curve


def _format_pct(value: Optional[float]) -> str:
    if value is None:
        return "未设置"
    return f"{value * 100:.0f}%"


def _format_reason_lines(rationale: Dict[str, Any]) -> List[str]:
    lines: List[str] = []

    reason_fragments = rationale.get("reason_fragments")
    if isinstance(reason_fragments, list) and reason_fragments:
        lines.append("- 候选理由：")
        for item in reason_fragments:
            lines.append(f"- {item}")
    elif reason_fragments is not None:
        lines.append(f"- 候选理由：{reason_fragments}")

    timing_signal = rationale.get("timing_signal")
    if isinstance(timing_signal, dict) and timing_signal:
        lines.append(f"- 时点信号：{json.dumps(timing_signal, ensure_ascii=False)}")
    elif timing_signal is not None:
        lines.append(f"- 时点信号：{timing_signal}")

    score_snapshot = rationale.get("score_snapshot")
    if isinstance(score_snapshot, dict) and score_snapshot:
        lines.append(f"- 评分快照：{json.dumps(score_snapshot, ensure_ascii=False)}")
    elif score_snapshot is not None:
        lines.append(f"- 评分快照：{score_snapshot}")

    if not lines:
        lines.append("- 无可用判断依据")
    return lines


def create_decision(payload: dict) -> dict:
    code = str(payload.get("code") or "").strip()
    decision_type = str(payload.get("decision_type") or "").strip()
    if not code:
        raise ValueError("code is required")
    if decision_type not in VALID_DECISION_TYPES:
        raise ValueError("decision_type must be one of buy/sell/rebalance/hold")

    rationale: Dict[str, Any] = {}

    try:
        from backend.indices import get_all_valuation_signals

        rationale["timing_signal"] = get_all_valuation_signals()
    except Exception as exc:
        rationale["timing_signal"] = str(exc)

    try:
        import backend.scoring as scoring

        score_snapshot = None
        get_fund_score = getattr(scoring, "get_fund_score", None)
        if callable(get_fund_score):
            score_snapshot = get_fund_score(code)
        else:
            get_latest_scores = getattr(scoring, "get_latest_scores", None)
            if callable(get_latest_scores):
                for item in get_latest_scores():
                    if str(item.get("code") or "") == code:
                        score_snapshot = item
                        break
        rationale["score_snapshot"] = score_snapshot
    except Exception as exc:
        rationale["score_snapshot"] = str(exc)

    source_session_id = payload.get("source_session_id")
    if source_session_id:
        try:
            conn = _connect()
            try:
                rationale["reason_fragments"] = _fetch_selector_reason_fragments(
                    conn, str(source_session_id), code
                )
            finally:
                conn.close()
        except Exception as exc:
            rationale["reason_fragments"] = str(exc)

    is_virtual = bool(payload.get("is_virtual"))
    virtual_entry_nav: Optional[float] = None
    initial_status = "pending"
    executed_at: Optional[str] = None

    created_at = _utc_now_iso()
    conn = _connect()
    try:
        if is_virtual:
            # Layer 1 单笔虚拟决策：快照当前净值，直接置为 executed
            latest_nav = _get_latest_nav(conn, code)
            if latest_nav is None or latest_nav <= 0:
                raise ValueError(
                    f"虚拟决策创建失败：基金 {code} 暂无净值数据，"
                    "请先在关注列表/持仓里触发一次数据抓取"
                )
            virtual_entry_nav = float(latest_nav)
            initial_status = "executed"
            executed_at = created_at

        cursor = conn.execute(
            """
            INSERT INTO decisions (
                created_at, decision_type, code, target_amount, target_nav_max,
                target_tp_pct, target_sl_pct, rationale_json, source_session_id,
                status, notes, is_virtual, virtual_entry_nav, executed_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                created_at,
                decision_type,
                code,
                payload.get("target_amount"),
                payload.get("target_nav_max"),
                payload.get("target_tp_pct"),
                payload.get("target_sl_pct"),
                _serialize_rationale(rationale),
                source_session_id,
                initial_status,
                payload.get("notes"),
                1 if is_virtual else 0,
                virtual_entry_nav,
                executed_at,
            ),
        )
        conn.commit()
        row_id = cursor.lastrowid
    finally:
        conn.close()

    return {
        "id": row_id,
        "status": initial_status,
        "code": code,
        "created_at": created_at,
        "is_virtual": is_virtual,
        "virtual_entry_nav": virtual_entry_nav,
    }


def link_transaction(decision_id: int, transaction_id: int) -> dict:
    conn = _connect()
    try:
        row = conn.execute(
            "SELECT is_virtual FROM decisions WHERE id=?",
            (decision_id,),
        ).fetchone()
        if not row:
            raise ValueError(f"decision {decision_id} not found")
        if row["is_virtual"]:
            raise ValueError(
                "虚拟决策不能关联真实交易。如需转为真实持仓，请新建一条真实决策。"
            )

        conn.execute(
            """
            UPDATE decisions
            SET status='executed', executed_at=?, transaction_id=?
            WHERE id=?
            """,
            (_utc_now_iso(), transaction_id, decision_id),
        )
        conn.commit()
    finally:
        conn.close()
    return {"id": decision_id, "status": "executed"}


def list_decisions(
    status_filter: Optional[str] = None,
    limit: int = 50,
    kind: str = "all",
) -> List[dict]:
    limit_value = max(1, int(limit))
    statuses = [item.strip() for item in (status_filter or "").split(",") if item.strip()]

    conn = _connect()
    try:
        sql = "SELECT * FROM decisions"
        clauses: List[str] = []
        params: List[Any] = []
        if statuses:
            placeholders = ",".join("?" for _ in statuses)
            clauses.append(f"status IN ({placeholders})")
            params.extend(statuses)
        kind_norm = (kind or "all").lower()
        if kind_norm == "real":
            clauses.append("COALESCE(is_virtual, 0) = 0")
        elif kind_norm == "virtual":
            clauses.append("COALESCE(is_virtual, 0) = 1")
        if clauses:
            sql += " WHERE " + " AND ".join(clauses)
        sql += " ORDER BY created_at DESC LIMIT ?"
        params.append(limit_value)

        rows = conn.execute(sql, tuple(params)).fetchall()
        results: List[dict] = []
        for row in rows:
            item = dict(row)

            # 1. current P&L
            latest_nav = _get_latest_nav(conn, item["code"])
            current_pnl_pct = None
            entry_nav_for_pnl: Optional[float] = None
            if item.get("is_virtual"):
                vnav = item.get("virtual_entry_nav")
                if vnav not in (None, 0):
                    entry_nav_for_pnl = float(vnav)
            elif item.get("transaction_id"):
                tx_row = conn.execute(
                    "SELECT nav_at_trade FROM transactions WHERE id=?",
                    (item["transaction_id"],),
                ).fetchone()
                if tx_row and tx_row["nav_at_trade"] not in (None, 0):
                    entry_nav_for_pnl = float(tx_row["nav_at_trade"])
            if latest_nav is not None and entry_nav_for_pnl:
                current_pnl_pct = latest_nav / entry_nav_for_pnl - 1
            item["current_pnl_pct"] = current_pnl_pct

            # 2. fund name (fund_profile primary, funds fallback)
            name_row = conn.execute(
                """
                SELECT COALESCE(
                    (SELECT name FROM fund_profile WHERE code=?),
                    (SELECT name FROM funds WHERE code=?)
                ) AS name
                """,
                (item["code"], item["code"]),
            ).fetchone()
            item["name"] = name_row["name"] if name_row and name_row["name"] else None

            # 3. parsed rationale + flat convenience fields
            rationale = _parse_rationale(item.get("rationale_json"))
            item["rationale"] = rationale
            fragments = rationale.get("reason_fragments")
            item["reason_fragments"] = fragments if isinstance(fragments, list) else []
            score_snap = rationale.get("score_snapshot")
            item["score_total_snapshot"] = (
                score_snap.get("total_score") if isinstance(score_snap, dict) else None
            )

            results.append(item)
        return results
    finally:
        conn.close()


def cancel_decision(decision_id: int) -> dict:
    conn = _connect()
    try:
        conn.execute(
            """
            UPDATE decisions
            SET status='cancelled', closed_at=?
            WHERE id=? AND status='pending'
            """,
            (_utc_now_iso(), decision_id),
        )
        conn.commit()
    finally:
        conn.close()
    return {"id": decision_id, "status": "cancelled"}


def check_tp_sl_triggers() -> List[dict]:
    conn = _connect()
    try:
        rows = conn.execute(
            "SELECT * FROM decisions WHERE status='executed'"
        ).fetchall()

        triggered: List[dict] = []
        for row in rows:
            entry_nav: Optional[float] = None
            if row["is_virtual"]:
                vnav = row["virtual_entry_nav"]
                if vnav in (None, 0):
                    continue
                entry_nav = float(vnav)
            else:
                tx_id = row["transaction_id"]
                if not tx_id:
                    continue
                tx_row = conn.execute(
                    "SELECT nav_at_trade FROM transactions WHERE id=?",
                    (tx_id,),
                ).fetchone()
                if not tx_row or tx_row["nav_at_trade"] in (None, 0):
                    continue
                entry_nav = float(tx_row["nav_at_trade"])

            latest_nav = _get_latest_nav(conn, row["code"])
            if latest_nav is None or entry_nav is None:
                continue

            pnl = latest_nav / entry_nav - 1
            new_status = None
            if row["target_tp_pct"] is not None and pnl >= float(row["target_tp_pct"]):
                new_status = "triggered_tp"
            elif row["target_sl_pct"] is not None and pnl <= float(row["target_sl_pct"]):
                new_status = "triggered_sl"

            if not new_status:
                continue

            closed_at = _utc_now_iso()
            conn.execute(
                """
                UPDATE decisions
                SET status=?, closed_at=?, closing_pnl_pct=?
                WHERE id=?
                """,
                (new_status, closed_at, pnl, row["id"]),
            )
            item = dict(row)
            item["status"] = new_status
            item["closed_at"] = closed_at
            item["closing_pnl_pct"] = pnl
            triggered.append(item)

        conn.commit()
        return triggered
    finally:
        conn.close()


def get_decision_performance(decision_id: int) -> dict:
    conn = _connect()
    try:
        row = conn.execute(
            "SELECT * FROM decisions WHERE id=?",
            (decision_id,),
        ).fetchone()
        if not row:
            return {"error": "decision not found"}

        decision = dict(row)
        if decision["status"] not in EXECUTED_STATUSES:
            return {"error": "not yet executed"}

        entry_nav = None
        is_virtual = bool(decision.get("is_virtual"))
        if is_virtual:
            vnav = decision.get("virtual_entry_nav")
            if vnav is not None:
                entry_nav = float(vnav)
        elif decision.get("transaction_id"):
            tx_row = conn.execute(
                "SELECT nav_at_trade FROM transactions WHERE id=?",
                (decision["transaction_id"],),
            ).fetchone()
            if tx_row and tx_row["nav_at_trade"] is not None:
                entry_nav = float(tx_row["nav_at_trade"])

        # 虚拟决策的 entry_date 应对齐到快照净值所在的交易日，
        # 而不是"创建决策的那一刻"，否则 nav_history 里没有 >= entry_date 的数据，
        # nav_curve 会一直是空直到下个交易日。
        executed_at = decision.get("executed_at") or decision.get("created_at") or ""
        entry_date = executed_at[:10]
        if is_virtual:
            latest_nav_date = _get_latest_nav_date(conn, decision["code"])
            if latest_nav_date:
                entry_date = latest_nav_date
        nav_rows = _get_nav_series(conn, decision["code"], entry_date) if entry_date else []
        nav_curve = _build_curve(nav_rows, entry_nav)

        benchmark_rows = _get_nav_series(conn, "000300", entry_date) if entry_date else []
        if not benchmark_rows:
            benchmark_rows = _get_nav_series(conn, "sh000300", entry_date) if entry_date else []

        benchmark_baseline = None
        if benchmark_rows and benchmark_rows[0]["nav"] not in (None, 0):
            benchmark_baseline = float(benchmark_rows[0]["nav"])
        hs300_curve = _build_curve(benchmark_rows, benchmark_baseline)

        nav_pnls = [item["pnl_pct"] for item in nav_curve if item["pnl_pct"] is not None]
        hs300_pnls = [item["pnl_pct"] for item in hs300_curve if item["pnl_pct"] is not None]

        return {
            "id": decision["id"],
            "code": decision["code"],
            "decision_type": decision["decision_type"],
            "status": decision["status"],
            "created_at": decision["created_at"],
            "executed_at": decision.get("executed_at"),
            "closed_at": decision.get("closed_at"),
            "transaction_id": decision.get("transaction_id"),
            "entry_date": entry_date,
            "entry_nav": entry_nav,
            "nav_curve": nav_curve,
            "hs300_curve": hs300_curve,
            "max_drawdown": min(nav_pnls) if nav_pnls else None,
            "max_gain": max(nav_pnls) if nav_pnls else None,
            "current_pnl_pct": nav_pnls[-1] if nav_pnls else None,
            "hs300_pnl_pct": hs300_pnls[-1] if hs300_pnls else None,
        }
    finally:
        conn.close()


def export_decision_review(decision_id: int) -> str:
    conn = _connect()
    try:
        row = conn.execute(
            "SELECT * FROM decisions WHERE id=?",
            (decision_id,),
        ).fetchone()
        if not row:
            return "# 决策复盘：未找到该决策"

        decision = dict(row)
        fund_row = conn.execute(
            "SELECT name FROM funds WHERE code=?",
            (decision["code"],),
        ).fetchone()
    finally:
        conn.close()

    fund_name = fund_row["name"] if fund_row and fund_row["name"] else decision["code"]
    decision_type_cn = DECISION_TYPE_CN.get(decision["decision_type"], decision["decision_type"])
    rationale = _parse_rationale(decision.get("rationale_json"))
    reason_lines = _format_reason_lines(rationale)

    perf = get_decision_performance(decision_id)
    status_lines: List[str] = []
    if perf.get("error") == "not yet executed":
        status_lines.append("尚未执行")
    elif perf.get("error"):
        status_lines.append(perf["error"])
    else:
        status_lines.append(f"- 执行时间：{decision.get('executed_at') or '未知'}")
        status_lines.append(
            f"- 建仓净值：{perf.get('entry_nav') if perf.get('entry_nav') is not None else '未知'}"
        )
        current_pnl = perf.get("current_pnl_pct")
        status_lines.append(
            f"- 当前收益：{current_pnl * 100:.2f}%"
            if current_pnl is not None
            else "- 当前收益：暂无数据"
        )

    return "\n".join(
        [
            f"# 决策复盘：{fund_name} {decision_type_cn} {str(decision.get('created_at') or '')[:10]}",
            "",
            "## 当时的判断依据",
            *reason_lines,
            "",
            "## 计划参数",
            "- 计划金额：{amount}，止盈：{tp}，止损：{sl}".format(
                amount=decision.get("target_amount") if decision.get("target_amount") is not None else "未设置",
                tp=_format_pct(decision.get("target_tp_pct")),
                sl=_format_pct(decision.get("target_sl_pct")),
            ),
            "",
            "## 实际执行 / 当前状态",
            *status_lines,
            "",
            "## 我的问题",
            "（请在此添加你想问 AI 的问题）",
        ]
    )
