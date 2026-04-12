"""策略模拟框架"""
from __future__ import annotations

import json
from dataclasses import dataclass, field
from datetime import date, datetime
from typing import Any, Callable, Dict, List, Optional, Protocol, Set

from backend.backtest import _get_nav_df
from backend.database import get_connection


@dataclass
class Trade:
    code: str
    action: str  # 'buy' or 'sell'
    shares: float
    price: float
    amount: float  # shares * price
    reason: str = ""


@dataclass
class SimulationState:
    cash: float
    holdings: Dict[str, float] = field(default_factory=dict)  # code -> shares

    def total_value(self, price_lookup: Dict[str, float]) -> float:
        equity = sum(shares * price_lookup.get(code, 0.0) for code, shares in self.holdings.items())
        return round(self.cash + equity, 2)


@dataclass
class MarketSnapshot:
    as_of_date: date
    nav_by_code: Dict[str, float]  # code -> nav on this date


class Strategy(Protocol):
    name: str

    def run(self, as_of_date, state, market, params, fund_pool) -> List[Trade]: ...


_STRATEGY_REGISTRY: Dict[str, Strategy] = {}


def register_strategy(strategy):
    _STRATEGY_REGISTRY[strategy.name] = strategy


def get_strategy(name):
    strategy = _STRATEGY_REGISTRY.get(name)
    if not strategy:
        raise ValueError(f"未知策略: {name}")
    return strategy


def list_strategies():
    return [
        {"name": s.name, "description": getattr(s, "description", "")}
        for s in _STRATEGY_REGISTRY.values()
    ]


class DcaMonthlyStrategy:
    name = "dca_monthly"
    description = "每月定投：每个月第一个交易日，对你选的每只基金各买入固定金额（由「每只基金每月买入金额」参数决定）"

    def fire_schedule(self, start_date, end_date, trading_dates) -> Set[date]:
        # 返回每个月的第一个交易日
        result = set()
        seen_months = set()
        for d in trading_dates:
            if start_date <= d <= end_date:
                key = (d.year, d.month)
                if key not in seen_months:
                    seen_months.add(key)
                    result.add(d)
        return result

    def should_fire_single_day(
        self,
        as_of_date: date,
        past_trades: List[dict],
    ) -> bool:
        """
        Forward 模式专用：在单日推进时判断今天是否应该触发策略。
        对于 dca_monthly：如果本月（as_of_date 所在的年月）在 past_trades 里没有任何买入记录，
        且今天是本月迄今的第一个交易日，则触发。因为 forward 模式是逐日推进的，
        只要"本月还没买过"就可以认为今天就是本月的第一个有效交易日。
        """
        target_key = (as_of_date.year, as_of_date.month)
        for t in past_trades:
            trade_date_str = str(t.get("trade_date") or "")
            if not trade_date_str:
                continue
            try:
                td = datetime.fromisoformat(trade_date_str).date()
            except ValueError:
                continue
            if (td.year, td.month) == target_key:
                return False
        return True

    def run(self, as_of_date, state, market, params, fund_pool):
        amount_per_fund = float(params.get("amount_per_fund", 500.0))
        trades = []
        for code in fund_pool:
            nav = market.nav_by_code.get(code)
            if nav is None or nav <= 0:
                continue
            if state.cash < amount_per_fund:
                continue
            shares = round(amount_per_fund / nav, 4)
            trades.append(
                Trade(
                    code=code,
                    action="buy",
                    shares=shares,
                    price=nav,
                    amount=amount_per_fund,
                    reason=f"DCA monthly buy @ nav {nav}",
                )
            )
        return trades


register_strategy(DcaMonthlyStrategy())


def _now_str() -> str:
    return datetime.now().strftime("%Y-%m-%d %H:%M:%S")


def _to_date(value: Any) -> Optional[date]:
    if value is None or value == "":
        return None
    if isinstance(value, date):
        return value
    return datetime.strptime(str(value), "%Y-%m-%d").date()


def _json_loads(raw: Optional[str], fallback):
    try:
        return json.loads(raw) if raw else fallback
    except (TypeError, json.JSONDecodeError):
        return fallback


def _row_to_dict(row) -> Optional[dict]:
    return dict(row) if row else None


def _fetch_simulation_row(sim_id: int) -> Optional[dict]:
    conn = get_connection()
    try:
        row = conn.execute("SELECT * FROM simulations WHERE id=?", (sim_id,)).fetchone()
        return _row_to_dict(row)
    finally:
        conn.close()


def _build_market_snapshot(as_of_date: date, fund_pool: List[str]) -> MarketSnapshot:
    conn = get_connection()
    try:
        placeholders = ",".join("?" for _ in fund_pool) or "''"
        rows = conn.execute(
            f"""
            SELECT code, nav
            FROM nav_history
            WHERE date=? AND code IN ({placeholders})
            """,
            (as_of_date.isoformat(), *fund_pool),
        ).fetchall()
    finally:
        conn.close()
    nav_by_code = {row["code"]: float(row["nav"]) for row in rows}
    return MarketSnapshot(as_of_date=as_of_date, nav_by_code=nav_by_code)


def _load_trade_rows(sim_id: int) -> List[dict]:
    conn = get_connection()
    try:
        rows = conn.execute(
            """
            SELECT id, sim_id, trade_date, code, action, shares, price, amount, reason
            FROM simulation_trades
            WHERE sim_id=?
            ORDER BY trade_date ASC, id ASC
            """,
            (sim_id,),
        ).fetchall()
        return [dict(row) for row in rows]
    finally:
        conn.close()


def _load_snapshot_rows(sim_id: int) -> List[dict]:
    conn = get_connection()
    try:
        rows = conn.execute(
            """
            SELECT sim_id, date, cash, total_value, holdings_json
            FROM simulation_snapshots
            WHERE sim_id=?
            ORDER BY date ASC
            """,
            (sim_id,),
        ).fetchall()
    finally:
        conn.close()

    snapshots = []
    for row in rows:
        item = dict(row)
        item["holdings_json"] = _json_loads(item.get("holdings_json"), {})
        snapshots.append(item)
    return snapshots


def _calculate_max_drawdown(snapshots: List[dict]) -> float:
    peak = None
    max_drawdown = 0.0
    for item in snapshots:
        total_value = float(item.get("total_value") or 0.0)
        if peak is None or total_value > peak:
            peak = total_value
        if peak and peak > 0:
            drawdown = (total_value / peak - 1.0) * 100
            max_drawdown = min(max_drawdown, drawdown)
    return round(max_drawdown, 2)


def _calculate_stats(initial_capital: float, trades: List[dict], snapshots: List[dict]) -> dict:
    final_value = round(float(snapshots[-1]["total_value"]), 2) if snapshots else round(initial_capital, 2)
    total_return_pct = round((final_value / initial_capital - 1.0) * 100, 2) if initial_capital else 0.0
    return {
        "final_value": final_value,
        "total_return_pct": total_return_pct,
        "max_drawdown": _calculate_max_drawdown(snapshots),
        "trade_count": len(trades),
    }


def create_simulation(payload) -> dict:
    strategy_name = (payload or {}).get("strategy_name", "")
    get_strategy(strategy_name)

    mode = (payload or {}).get("mode", "backtest")
    if mode not in {"backtest", "forward"}:
        raise ValueError("mode must be backtest or forward")

    now = _now_str()
    params = (payload or {}).get("params") or {}
    fund_pool = (payload or {}).get("fund_pool") or []
    notes = (payload or {}).get("notes")

    # forward 模式：start_date 默认今天；创建时就进入 running，current_date 初始化为 start_date - 1 天
    # （第一次 advance 会从 start_date 开始推进）
    start_date_str = payload.get("start_date") or date.today().isoformat()

    if mode == "forward":
        initial_status = "running"
        initial_current_date = None  # None 表示还没开始推进；第一次 advance 从 start_date 开始
    else:
        initial_status = "pending"
        initial_current_date = None

    conn = get_connection()
    try:
        cursor = conn.execute(
            """
            INSERT INTO simulations (
                name, strategy_name, params_json, fund_pool_json,
                initial_capital, start_date, end_date, mode,
                status, current_date, created_at, updated_at, notes
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                payload["name"],
                strategy_name,
                json.dumps(params, ensure_ascii=False),
                json.dumps(fund_pool, ensure_ascii=False),
                float(payload["initial_capital"]),
                start_date_str,
                payload.get("end_date"),
                mode,
                initial_status,
                initial_current_date,
                now,
                now,
                notes,
            ),
        )
        conn.commit()
        sim_id = cursor.lastrowid
    finally:
        conn.close()

    created = _fetch_simulation_row(sim_id)
    if not created:
        raise ValueError("simulation 创建失败")
    return created


def list_simulations() -> List[dict]:
    conn = get_connection()
    try:
        rows = conn.execute(
            """
            SELECT *
            FROM simulations
            ORDER BY created_at DESC, id DESC
            """
        ).fetchall()
        return [dict(row) for row in rows]
    finally:
        conn.close()


def get_simulation_detail(sim_id) -> Optional[dict]:
    sim = _fetch_simulation_row(int(sim_id))
    if not sim:
        return None

    trades = _load_trade_rows(int(sim_id))
    snapshots = _load_snapshot_rows(int(sim_id))
    stats = _calculate_stats(float(sim["initial_capital"]), trades, snapshots)

    warnings = []
    start = _to_date(sim.get("start_date"))
    end = _to_date(sim.get("end_date"))
    if start and end and (end - start).days < 90:
        warnings.append("⚠️ 模拟时长不足 90 天，PnL 结果无统计意义")

    return {
        **sim,
        "trades": trades,
        "snapshots": snapshots,
        "stats": stats,
        "warnings": warnings,
    }


def delete_simulation(sim_id) -> dict:
    sim = _fetch_simulation_row(int(sim_id))
    if not sim:
        raise ValueError("simulation 不存在")

    conn = get_connection()
    try:
        conn.execute("DELETE FROM simulation_trades WHERE sim_id=?", (sim_id,))
        conn.execute("DELETE FROM simulation_snapshots WHERE sim_id=?", (sim_id,))
        conn.execute("DELETE FROM simulations WHERE id=?", (sim_id,))
        conn.commit()
    finally:
        conn.close()

    return {"deleted": True, "id": int(sim_id)}


def run_one_day(sim_id, as_of_date, sim_row, state, strategy, should_fire_strategy) -> SimulationState:
    """
    1. 取当日各基金净值 → MarketSnapshot
    2. 如果 should_fire_strategy，调 strategy.run(...)，生成 trades
    3. 对每笔 trade 扣现金、加持仓，写 simulation_trades 表
    4. 写 simulation_snapshots 表（当日总市值）
    5. 返回更新后的 state
    """
    params = _json_loads(sim_row.get("params_json"), {})
    fund_pool = _json_loads(sim_row.get("fund_pool_json"), [])
    market = _build_market_snapshot(as_of_date, fund_pool)

    should_fire = should_fire_strategy(as_of_date) if callable(should_fire_strategy) else bool(should_fire_strategy)
    trades = strategy.run(as_of_date, state, market, params, fund_pool) if should_fire else []

    conn = get_connection()
    try:
        for trade in trades:
            if trade.action not in {"buy", "sell"}:
                continue

            executed_amount = round(float(trade.shares) * float(trade.price), 2)
            shares = round(float(trade.shares), 4)
            price = round(float(trade.price), 4)

            if trade.action == "buy":
                if executed_amount <= 0 or state.cash + 1e-8 < executed_amount:
                    continue
                state.cash = round(state.cash - executed_amount, 2)
                state.holdings[trade.code] = round(state.holdings.get(trade.code, 0.0) + shares, 4)
            else:
                held = state.holdings.get(trade.code, 0.0)
                if executed_amount <= 0 or held + 1e-8 < shares:
                    continue
                remaining = round(held - shares, 4)
                state.cash = round(state.cash + executed_amount, 2)
                if remaining <= 0:
                    state.holdings.pop(trade.code, None)
                else:
                    state.holdings[trade.code] = remaining

            conn.execute(
                """
                INSERT INTO simulation_trades (
                    sim_id, trade_date, code, action, shares, price, amount, reason
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    sim_id,
                    as_of_date.isoformat(),
                    trade.code,
                    trade.action,
                    shares,
                    price,
                    executed_amount,
                    trade.reason or "",
                ),
            )

        total_value = state.total_value(market.nav_by_code)
        conn.execute(
            """
            INSERT OR REPLACE INTO simulation_snapshots (
                sim_id, date, cash, total_value, holdings_json
            ) VALUES (?, ?, ?, ?, ?)
            """,
            (
                sim_id,
                as_of_date.isoformat(),
                round(state.cash, 2),
                total_value,
                json.dumps(state.holdings, ensure_ascii=False),
            ),
        )
        conn.execute(
            """
            UPDATE simulations
            SET current_date=?, updated_at=?
            WHERE id=?
            """,
            (as_of_date.isoformat(), _now_str(), sim_id),
        )
        conn.commit()
    finally:
        conn.close()

    return state


def run_simulation(sim_id, mode="backtest") -> Dict:
    """
    - 加载 simulation 行
    - 解析 params_json, fund_pool_json
    - 实例化 strategy
    - 从 start_date 到 end_date 拉出 fund_pool 所有基金的交集交易日
    - 计算策略触发日（strategy.fire_schedule）
    - 循环每个交易日调 run_one_day
    - 结束后更新 simulation.status='completed', current_date=end_date
    - 返回 {trades_count, final_value, total_return_pct, nav_curve}

    幂等：开始时 DELETE FROM simulation_trades WHERE sim_id=? + DELETE FROM simulation_snapshots WHERE sim_id=?
    """
    sim = _fetch_simulation_row(int(sim_id))
    if not sim:
        raise ValueError("simulation 不存在")

    # Forward 模式直接 dispatch 到 advance_forward_simulation
    if sim.get("mode") == "forward" or mode == "forward":
        result = advance_forward_simulation(int(sim_id))
        # 返回结构对齐：forward 用 advanced_days 代替 trades_count 概念
        detail = get_simulation_detail(int(sim_id))
        stats = detail["stats"] if detail else {}
        return {
            "mode": "forward",
            "advanced_days": result.get("advanced_days", 0),
            "trade_count": len(detail["trades"]) if detail else 0,
            "current_date": result.get("current_date"),
            "status": result.get("status"),
            "final_value": stats.get("final_value"),
            "total_return_pct": stats.get("total_return_pct"),
        }

    if sim.get("mode") != "backtest":
        raise ValueError("当前仅支持 backtest/forward mode")

    start_date = _to_date(sim["start_date"])
    end_date = _to_date(sim.get("end_date"))
    if not start_date or not end_date:
        raise ValueError("backtest 模式必须提供 start_date 和 end_date")

    fund_pool = _json_loads(sim.get("fund_pool_json"), [])
    if not fund_pool:
        raise ValueError("fund_pool 不能为空")

    strategy = get_strategy(sim["strategy_name"])
    nav_dates_by_code: Dict[str, Set[date]] = {}

    for code in fund_pool:
        df = _get_nav_df(code, start_date.isoformat(), end_date.isoformat())
        if df.empty:
            raise ValueError(f"基金 {code} 没有足够的净值数据")
        nav_dates_by_code[code] = {ts.date() for ts in df.index}

    common_dates = sorted(set.intersection(*nav_dates_by_code.values()))
    if not common_dates:
        raise ValueError("fund_pool 在给定区间没有共同交易日")

    fire_dates = strategy.fire_schedule(start_date, end_date, common_dates) if hasattr(strategy, "fire_schedule") else set(common_dates)
    state = SimulationState(cash=round(float(sim["initial_capital"]), 2))

    conn = get_connection()
    try:
        conn.execute(
            """
            UPDATE simulations
            SET status='running', current_date=NULL, updated_at=?
            WHERE id=?
            """,
            (_now_str(), sim_id),
        )
        conn.execute("DELETE FROM simulation_trades WHERE sim_id=?", (sim_id,))
        conn.execute("DELETE FROM simulation_snapshots WHERE sim_id=?", (sim_id,))
        conn.commit()
    finally:
        conn.close()

    try:
        for trading_date in common_dates:
            state = run_one_day(
                sim_id=sim_id,
                as_of_date=trading_date,
                sim_row=sim,
                state=state,
                strategy=strategy,
                should_fire_strategy=lambda current, dates=fire_dates: current in dates,
            )

        detail = get_simulation_detail(sim_id)
        if detail is None:
            raise ValueError("simulation 结果读取失败")

        stats = detail["stats"]
        nav_curve = [
            {
                "date": item["date"],
                "cash": item["cash"],
                "total_value": item["total_value"],
            }
            for item in detail["snapshots"]
        ]

        conn = get_connection()
        try:
            conn.execute(
                """
                UPDATE simulations
                SET status='completed', current_date=?, updated_at=?
                WHERE id=?
                """,
                (end_date.isoformat(), _now_str(), sim_id),
            )
            conn.commit()
        finally:
            conn.close()

        return {
            "trades_count": len(detail["trades"]),
            "final_value": stats["final_value"],
            "total_return_pct": stats["total_return_pct"],
            "nav_curve": nav_curve,
        }
    except Exception:
        conn = get_connection()
        try:
            conn.execute(
                """
                UPDATE simulations
                SET status='failed', updated_at=?
                WHERE id=?
                """,
                (_now_str(), sim_id),
            )
            conn.commit()
        finally:
            conn.close()
        raise


# ─── v2.3 Batch 6: Forward mode ────────────────────────────────────

def _rebuild_state_from_db(sim_id: int, initial_capital: float) -> SimulationState:
    """
    forward 模式每次 advance 时，从最新 snapshot 重建运行态。
    没有 snapshot 时回落到 initial_capital + 空持仓。
    """
    snapshots = _load_snapshot_rows(sim_id)
    if not snapshots:
        return SimulationState(cash=round(float(initial_capital), 2))
    last = snapshots[-1]
    holdings_raw = last.get("holdings_json") or {}
    if not isinstance(holdings_raw, dict):
        holdings_raw = _json_loads(holdings_raw, {})
    return SimulationState(
        cash=round(float(last.get("cash") or 0.0), 2),
        holdings={str(k): float(v) for k, v in holdings_raw.items()},
    )


def advance_forward_simulation(
    sim_id: int,
    target_date: Optional[date] = None,
) -> Dict[str, Any]:
    """
    推进一条 forward 模拟到 target_date（默认 today）。
    - 从 current_date + 1 开始，跑到 target_date
    - 只在有 nav 的交易日调 run_one_day
    - 对 dca_monthly 这类策略，使用 strategy.should_fire_single_day 判断当日是否触发
    - 达到 end_date 后把 status 置为 completed

    返回：{"advanced_days", "trade_count", "current_date", "status"}
    """
    sim = _fetch_simulation_row(int(sim_id))
    if not sim:
        raise ValueError("simulation 不存在")
    if sim.get("mode") != "forward":
        raise ValueError("只能对 forward 模式的 simulation 调用 advance")
    if sim.get("status") == "completed":
        return {
            "advanced_days": 0,
            "trade_count": 0,
            "current_date": sim.get("current_date"),
            "status": "completed",
            "note": "已经完成，无需推进",
        }

    target = target_date or date.today()
    start_date = _to_date(sim.get("start_date")) or date.today()
    end_date = _to_date(sim.get("end_date"))  # 可选

    # 上限卡在 end_date 和 target 之间取较小的
    if end_date and target > end_date:
        target = end_date

    # 起点：current_date + 1，第一次推进时 current_date 为 None，从 start_date 起
    current = _to_date(sim.get("current_date"))
    if current is None:
        next_date = start_date
    else:
        next_date = date.fromordinal(current.toordinal() + 1)

    if next_date > target:
        return {
            "advanced_days": 0,
            "trade_count": 0,
            "current_date": sim.get("current_date"),
            "status": sim.get("status"),
            "note": f"当前日期 {current} 已经 >= 目标日期 {target}",
        }

    fund_pool = _json_loads(sim.get("fund_pool_json"), [])
    if not fund_pool:
        raise ValueError("fund_pool 不能为空")

    strategy = get_strategy(sim["strategy_name"])
    state = _rebuild_state_from_db(int(sim_id), float(sim["initial_capital"]))

    advanced_days = 0
    trade_count_before = len(_load_trade_rows(int(sim_id)))

    # 日期迭代（每天一步；非交易日只推进 current_date，不触发策略）
    cursor_date = next_date
    while cursor_date <= target:
        market = _build_market_snapshot(cursor_date, fund_pool)
        if not market.nav_by_code:
            # 非交易日或没有 nav 数据：只推进 current_date，不调 run_one_day
            conn = get_connection()
            try:
                conn.execute(
                    "UPDATE simulations SET current_date=?, updated_at=? WHERE id=?",
                    (cursor_date.isoformat(), _now_str(), sim_id),
                )
                conn.commit()
            finally:
                conn.close()
        else:
            # 真正的交易日：问策略"今天要不要开火"
            past_trades = _load_trade_rows(int(sim_id))
            should_fire = False
            if hasattr(strategy, "should_fire_single_day"):
                should_fire = bool(strategy.should_fire_single_day(cursor_date, past_trades))

            state = run_one_day(
                sim_id=int(sim_id),
                as_of_date=cursor_date,
                sim_row=sim,
                state=state,
                strategy=strategy,
                should_fire_strategy=lambda d, fire=should_fire: fire,
            )
        advanced_days += 1
        cursor_date = date.fromordinal(cursor_date.toordinal() + 1)

    # end_date 达到则 completed
    final_current = _to_date(_fetch_simulation_row(int(sim_id)).get("current_date"))
    new_status = sim.get("status")
    if end_date and final_current and final_current >= end_date:
        conn = get_connection()
        try:
            conn.execute(
                "UPDATE simulations SET status='completed', updated_at=? WHERE id=?",
                (_now_str(), sim_id),
            )
            conn.commit()
        finally:
            conn.close()
        new_status = "completed"

    trade_count_after = len(_load_trade_rows(int(sim_id)))

    return {
        "advanced_days": advanced_days,
        "trade_count": trade_count_after - trade_count_before,
        "current_date": final_current.isoformat() if final_current else None,
        "status": new_status,
    }


def advance_all_forward_simulations() -> Dict[str, Any]:
    """
    APScheduler 每晚调用：遍历所有 running + forward 的 simulation，推进到今天。
    """
    conn = get_connection()
    try:
        rows = conn.execute(
            "SELECT id FROM simulations WHERE mode='forward' AND status='running'"
        ).fetchall()
        sim_ids = [int(r["id"]) for r in rows]
    finally:
        conn.close()

    results: List[Dict[str, Any]] = []
    for sid in sim_ids:
        try:
            r = advance_forward_simulation(sid)
            r["sim_id"] = sid
            results.append(r)
        except Exception as exc:
            results.append({"sim_id": sid, "error": str(exc)})
    return {"processed": len(sim_ids), "results": results}
