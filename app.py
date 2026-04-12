"""基金投资决策助手 - FastAPI 入口"""
from __future__ import annotations
import json
import os
import subprocess
import sys
from typing import Optional

from fastapi import FastAPI, UploadFile, File, Form, BackgroundTasks
from fastapi.staticfiles import StaticFiles
from fastapi.responses import FileResponse, Response
from contextlib import asynccontextmanager

from backend.database import init_db
from backend.portfolio import get_portfolio_summary, get_holding_detail, record_transaction, get_nav_history
from backend.fetcher import fetch_realtime_estimate, search_fund, refresh_all_funds, fetch_nav_history as fetch_nav
from backend.risk import risk_analysis, correlation_matrix
from backend.rebalance import get_rebalance_suggestions, set_target_allocation, get_target_allocation, gradual_transition_plan
from backend.watchlist import get_watchlist, add_to_watchlist, remove_from_watchlist
from backend.backtest import dca_backtest, take_profit_backtest, portfolio_backtest
from backend.models import (
    TransactionRequest, TargetAllocationRequest,
    DCABacktestRequest, TakeProfitBacktestRequest, PortfolioBacktestRequest,
    ImportCommitRequest, WatchlistAddRequest,
    DecisionCreateRequest, LinkTransactionRequest, SimulationCreateRequest,
)
from backend.importer import (
    ocr_image, parse_holdings_screenshot, parse_transaction_screenshot,
    fuzzy_match_fund, get_all_funds_from_db,
    bulk_replace_holdings, bulk_import_transactions,
)
from backend.fetcher import refresh_holdings_for_all_funds
from backend.holdings_analysis import get_holdings_overlap, get_industry_breakdown, get_holdings_changes
from backend.database import get_connection
from backend.indices import get_all_valuation_signals, refresh_index_pe
from backend.scoring import get_latest_scores, calculate_all_scores
from backend.ai_context import get_ai_context
from backend.selector import QUESTIONS, diagnose_portfolio_gaps, generate_recommendation, export_prompt_for_claude, find_peer_funds_by_theme
from backend.decisions import (
    create_decision, link_transaction, list_decisions,
    cancel_decision, check_tp_sl_triggers, get_decision_performance,
    export_decision_review,
)
from backend.timing import get_timing_signal
from backend.simulation import (
    advance_all_forward_simulations,
    advance_forward_simulation,
    create_simulation,
    delete_simulation,
    get_simulation_detail,
    list_simulations,
    list_strategies,
    run_simulation,
)
from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.cron import CronTrigger

BASE_DIR = os.path.dirname(__file__)


def _bg_fetch_watchlist_data(code: str) -> None:
    """关注列表后台数据补全：持股 + 行业 + 评分"""
    try:
        from backend.fetcher import fetch_fund_holdings
        fetch_fund_holdings(code)
    except Exception as e:
        print(f"[bg_watchlist] {code} 持股抓取失败: {e}")


@asynccontextmanager
async def lifespan(app: FastAPI):
    init_db()
    scheduler = BackgroundScheduler()
    scheduler.add_job(
        refresh_all_funds,
        CronTrigger(hour=15, minute=30, day_of_week="mon-fri"),
        id="refresh_nav",
        replace_existing=True,
    )
    scheduler.add_job(
        refresh_holdings_for_all_funds,
        CronTrigger(hour=16, minute=0, day_of_week="mon-fri"),
        id="refresh_holdings",
        replace_existing=True,
    )
    scheduler.add_job(
        refresh_index_pe,
        CronTrigger(hour=9, minute=0, day_of_week="mon-fri"),
        id="refresh_index_pe",
        replace_existing=True,
    )
    scheduler.add_job(
        calculate_all_scores,
        CronTrigger(hour=15, minute=35, day_of_week="mon-fri"),
        id="calculate_scores",
        replace_existing=True,
    )
    from backend.decisions import check_tp_sl_triggers as _check_tp_sl
    scheduler.add_job(
        _check_tp_sl,
        CronTrigger(hour=15, minute=40, day_of_week="mon-fri"),
        id="check_tp_sl",
        replace_existing=True,
    )
    from backend.peer_ranks import refresh_peer_ranks
    scheduler.add_job(
        refresh_peer_ranks,
        CronTrigger(hour=8, minute=0, day_of_week="mon"),
        id="refresh_peer_ranks",
        replace_existing=True,
    )
    from backend.watchlist import refresh_watchlist_missing_holdings as _refresh_wl_holdings
    scheduler.add_job(
        _refresh_wl_holdings,
        CronTrigger(hour=16, minute=30, day_of_week="mon-fri"),
        id="refresh_watchlist_holdings",
        replace_existing=True,
    )
    # v2.3 Batch 6: 每晚 16:00 推进所有 forward 模拟
    scheduler.add_job(
        advance_all_forward_simulations,
        CronTrigger(hour=16, minute=0, day_of_week="mon-fri"),
        id="advance_forward_sims",
        replace_existing=True,
    )
    scheduler.start()
    yield
    scheduler.shutdown()


app = FastAPI(title="基金投资决策助手", version="1.0.0", lifespan=lifespan)

# 静态文件
app.mount("/static", StaticFiles(directory="static"), name="static")


# === 首页 ===
@app.get("/")
async def index():
    return FileResponse("static/index.html")


# === 持仓 API ===
@app.get("/api/portfolio")
async def api_portfolio():
    return get_portfolio_summary()


@app.get("/api/portfolio/{code}")
async def api_portfolio_detail(code: str):
    detail = get_holding_detail(code)
    if not detail:
        return {"error": "未找到该基金持仓"}
    return detail


@app.post("/api/portfolio/transaction")
async def api_transaction(req: TransactionRequest):
    return record_transaction(
        code=req.code, tx_type=req.type, date=req.date,
        amount=req.amount, nav=req.nav, fee=req.fee, notes=req.notes
    )


# === 基金数据 API ===
@app.get("/api/funds/search")
async def api_search(q: str = ""):
    if not q:
        return []
    return search_fund(q)


@app.get("/api/funds/{code}/nav")
async def api_nav(code: str, days: int = 365):
    return get_nav_history(code, days)


@app.get("/api/funds/{code}/realtime")
async def api_realtime(code: str):
    result = fetch_realtime_estimate(code)
    return result or {"error": "暂无实时数据"}


@app.post("/api/funds/refresh")
async def api_refresh():
    return refresh_all_funds()


# === 风险分析 API ===
@app.get("/api/risk/analysis")
async def api_risk():
    summary = get_portfolio_summary()
    return risk_analysis(summary["holdings"], summary["total_value"])


@app.get("/api/risk/correlation")
async def api_correlation():
    summary = get_portfolio_summary()
    codes = [h["code"] for h in summary["holdings"]]
    return correlation_matrix(codes)


# === 调仓建议 API ===
@app.get("/api/rebalance/suggestions")
async def api_rebalance():
    summary = get_portfolio_summary()
    return get_rebalance_suggestions(summary["holdings"], summary["total_value"])


@app.get("/api/rebalance/target")
async def api_get_target():
    return get_target_allocation()


@app.post("/api/rebalance/target")
async def api_set_target(req: TargetAllocationRequest):
    return set_target_allocation(req.allocations)


@app.get("/api/rebalance/transition")
async def api_transition(months: int = 6, monthly_invest: float = 5000):
    summary = get_portfolio_summary()
    return gradual_transition_plan(summary["holdings"], summary["total_value"], months, monthly_invest)


# === 回测 API ===
@app.post("/api/backtest/dca")
async def api_backtest_dca(req: DCABacktestRequest):
    return dca_backtest(
        code=req.code, amount=req.amount, frequency=req.frequency,
        start_date=req.start_date, end_date=req.end_date
    )


@app.post("/api/backtest/takeprofit")
async def api_backtest_tp(req: TakeProfitBacktestRequest):
    return take_profit_backtest(
        code=req.code, amount=req.amount, frequency=req.frequency,
        take_profit_pct=req.take_profit_pct, stop_loss_pct=req.stop_loss_pct,
        start_date=req.start_date, end_date=req.end_date
    )


@app.post("/api/backtest/portfolio")
async def api_backtest_portfolio(req: PortfolioBacktestRequest):
    return portfolio_backtest(
        allocations=req.allocations, total_amount=req.total_amount,
        rebalance_freq=req.rebalance_freq,
        start_date=req.start_date, end_date=req.end_date
    )


# ──────────────── 持仓透视 ────────────────

@app.get("/api/holdings/{code}/stocks")
def get_fund_stocks(code: str):
    """单基金最新持仓股票列表"""
    conn = get_connection()
    latest = conn.execute(
        "SELECT MAX(report_date) as d FROM fund_holdings WHERE code=?", (code,)
    ).fetchone()
    if not latest or not latest["d"]:
        conn.close()
        return {"code": code, "stocks": [], "report_date": ""}
    stocks = conn.execute(
        "SELECT stock_code, stock_name, weight, industry FROM fund_holdings WHERE code=? AND report_date=?",
        (code, latest["d"])
    ).fetchall()
    conn.close()
    return {
        "code": code,
        "stocks": [dict(s) for s in stocks],
        "report_date": latest["d"],
    }


@app.get("/api/holdings/{code}/industry")
def get_fund_industry_route(code: str):
    """单基金最新行业分布"""
    conn = get_connection()
    latest = conn.execute(
        "SELECT MAX(report_date) as d FROM fund_industry WHERE code=?", (code,)
    ).fetchone()
    if not latest or not latest["d"]:
        conn.close()
        return {"code": code, "industries": [], "report_date": ""}
    industries = conn.execute(
        "SELECT industry, weight FROM fund_industry WHERE code=? AND report_date=?",
        (code, latest["d"])
    ).fetchall()
    conn.close()
    return {
        "code": code,
        "industries": [dict(i) for i in industries],
        "report_date": latest["d"],
    }


@app.get("/api/holdings/overlap")
def holdings_overlap():
    """跨基金持仓重叠分析"""
    return get_holdings_overlap()


@app.get("/api/holdings/industry-total")
def holdings_industry_total():
    """穿透后汇总行业分布"""
    return get_industry_breakdown()


@app.get("/api/holdings/{code}/changes")
def holdings_changes(code: str):
    """单基金持仓变化对比（最近两期）"""
    return get_holdings_changes(code)


@app.post("/api/holdings/refresh")
def refresh_holdings():
    """手动触发所有基金持仓数据抓取"""
    return refresh_holdings_for_all_funds()


# ──────────────── v2.0 API ────────────────

@app.get("/api/market/valuation")
def api_market_valuation():
    """市场估值信号（PE百分位）"""
    return get_all_valuation_signals()


@app.post("/api/market/valuation/refresh")
def api_market_valuation_refresh():
    """手动触发指数 PE 数据刷新"""
    return refresh_index_pe()


@app.get("/api/funds/scores")
def api_fund_scores():
    """所有持仓基金综合评分"""
    return get_latest_scores()


@app.post("/api/funds/scores/refresh")
def api_fund_scores_refresh():
    """手动触发评分计算"""
    return calculate_all_scores()


@app.post("/api/peer-ranks/refresh")
def api_peer_ranks_refresh():
    """手动触发同类基金排名刷新（akshare 全市场开放式基金榜）"""
    from backend.peer_ranks import refresh_peer_ranks
    return refresh_peer_ranks()


@app.get("/api/ai/context")
def api_ai_context():
    """生成 AI 持仓解读 prompt context"""
    return get_ai_context()


@app.post("/api/import/parse")
async def api_import_parse(file: UploadFile = File(...), import_type: str = Form(...)):
    """解析支付宝截图，返回 OCR 识别结果供前端核对"""
    image_bytes = await file.read()
    if len(image_bytes) > 10 * 1024 * 1024:
        return {"import_type": import_type, "rows": [], "error": "文件过大，请上传 10MB 以内的图片"}

    if not (image_bytes[:2] == b'\xff\xd8' or image_bytes[:8] == b'\x89PNG\r\n\x1a\n'):
        return {"import_type": import_type, "rows": [], "error": "请上传 JPG 或 PNG 格式的图片"}

    try:
        lines = ocr_image(image_bytes)
    except Exception as e:
        return {"import_type": import_type, "rows": [], "error": f"OCR 解析失败：{e}"}

    if not lines:
        return {"import_type": import_type, "rows": [], "error": "未能识别截图中的文字，请确保图片清晰"}

    if import_type == "holdings":
        parsed = parse_holdings_screenshot(lines)
    else:
        parsed = parse_transaction_screenshot(lines)

    if not parsed:
        return {
            "import_type": import_type,
            "rows": [],
            "error": "未能从截图中识别到基金数据，请确认是支付宝持仓或交易记录截图",
        }

    all_funds = get_all_funds_from_db()
    for row in parsed:
        row["matches"] = fuzzy_match_fund(row.get("raw_name", ""), all_funds)

    return {"import_type": import_type, "rows": parsed, "error": None}


@app.post("/api/import/commit")
async def api_import_commit(req: ImportCommitRequest):
    """提交核对后的数据，写入数据库"""
    if req.import_type == "holdings":
        result = bulk_replace_holdings(req.rows)
    else:
        result = bulk_import_transactions(req.rows)
    return result


@app.get("/api/watchlist")
def api_get_watchlist():
    return get_watchlist()


@app.post("/api/watchlist/add")
def api_add_watchlist(req: WatchlistAddRequest, background_tasks: BackgroundTasks):
    result = add_to_watchlist(req.code)
    background_tasks.add_task(_bg_fetch_watchlist_data, req.code)
    return result


@app.delete("/api/watchlist/{code}")
def api_remove_watchlist(code: str):
    return remove_from_watchlist(code)


def _safe_json_loads(raw: str, fallback):
    try:
        return json.loads(raw) if raw else fallback
    except (TypeError, json.JSONDecodeError):
        return fallback


def _run_build_fund_profile():
    subprocess.Popen(
        [sys.executable, "-m", "scripts.build_fund_profile"],
        cwd=BASE_DIR,
    )


@app.get("/api/selector/questions")
def api_selector_questions():
    return {"status": "ok", "data": QUESTIONS}


@app.post("/api/selector/diagnose")
def api_selector_diagnose():
    return {"status": "ok", "data": diagnose_portfolio_gaps()}


@app.post("/api/selector/recommend")
def api_selector_recommend(payload: Optional[dict] = None):
    answers = payload.get("answers") if isinstance(payload, dict) else None
    required_keys = ("q1", "q2", "q3", "q4")
    if (
        not isinstance(answers, dict)
        or any(not isinstance(answers.get(key), str) or not answers.get(key).strip() for key in required_keys)
    ):
        return {"status": "error", "msg": "缺少答案: q1,q2,q3,q4 均为必填"}
    return {"status": "ok", "data": generate_recommendation(answers)}


@app.get("/api/selector/session/{session_id}")
def api_selector_session(session_id: str):
    conn = get_connection()
    try:
        row = conn.execute(
            """
            SELECT session_id, answers_json, gaps_json, candidates_json, created_at
            FROM selector_cache
            WHERE session_id=?
            """,
            (session_id,),
        ).fetchone()
    finally:
        conn.close()

    if not row:
        return {"status": "error", "msg": "会话不存在"}

    return {
        "status": "ok",
        "data": {
            "session_id": row["session_id"],
            "answers_json": _safe_json_loads(row["answers_json"], {}),
            "gaps_json": _safe_json_loads(row["gaps_json"], []),
            "candidates_json": _safe_json_loads(row["candidates_json"], {}),
            "created_at": row["created_at"],
        },
    }


@app.get("/api/selector/export/{session_id}")
def api_selector_export(session_id: str):
    markdown_str = export_prompt_for_claude(session_id)
    status_code = 404 if markdown_str.startswith("错误：") else 200
    return Response(
        content=markdown_str,
        media_type="text/plain; charset=utf-8",
        status_code=status_code,
    )


@app.post("/api/selector/adopt")
def api_selector_adopt(payload: Optional[dict] = None):
    code = payload.get("code") if isinstance(payload, dict) else None
    if not isinstance(code, str) or not code.strip():
        return {"status": "error", "msg": "code 为必填字段"}
    return {"status": "ok", "data": add_to_watchlist(code.strip())}


@app.post("/api/selector/refresh-profile")
def api_selector_refresh_profile(background_tasks: BackgroundTasks):
    background_tasks.add_task(_run_build_fund_profile)
    return {"status": "ok", "msg": "后台重建 fund_profile 任务已启动，请稍后刷新"}


# ──────────────── v2.1 择时 + 决策日志 ────────────────

@app.get("/api/timing/{code}")
def api_timing(code: str):
    return get_timing_signal(code)


@app.get("/api/position-sizing/{code}")
def api_position_sizing(code: str):
    from backend.position_sizing import suggest_position
    from backend.database import get_setting
    try:
        risk = get_setting("risk_level", "moderate") or "moderate"
        data = suggest_position(code, risk_level=risk)
        return {"status": "ok", "data": data}
    except Exception as e:
        return {"status": "error", "msg": str(e)}


@app.get("/api/settings")
def api_get_settings():
    from backend.database import get_all_settings
    return {"status": "ok", "data": get_all_settings()}


@app.patch("/api/settings")
def api_patch_settings(payload: dict):
    from backend.database import set_setting, get_all_settings
    allowed = {"risk_level"}
    if not isinstance(payload, dict):
        return {"status": "error", "msg": "payload must be dict"}
    if "risk_level" in payload and payload["risk_level"] not in ("conservative", "moderate", "aggressive"):
        return {"status": "error", "msg": "risk_level must be conservative/moderate/aggressive"}
    for k, v in payload.items():
        if k in allowed:
            set_setting(k, str(v))
    return {"status": "ok", "data": get_all_settings()}


@app.post("/api/decisions")
def api_decisions_create(req: DecisionCreateRequest):
    # bool 字段 is_virtual 即便是 False 也必须传给 create_decision，
    # 因此这里不使用 exclude_none（保留 False 值）
    payload = req.dict()
    try:
        return {"status": "ok", "data": create_decision(payload)}
    except ValueError as exc:
        from fastapi import HTTPException
        raise HTTPException(status_code=400, detail=str(exc))


@app.get("/api/decisions")
def api_decisions_list(
    status: Optional[str] = None,
    limit: int = 50,
    kind: str = "all",
):
    return {
        "status": "ok",
        "data": list_decisions(status_filter=status, limit=limit, kind=kind),
    }


@app.post("/api/decisions/check-triggers")
def api_decisions_check_triggers():
    return {"status": "ok", "data": check_tp_sl_triggers()}


@app.get("/api/decisions/{decision_id}")
def api_decisions_get(decision_id: int):
    rows = list_decisions(limit=1000)
    row = next((r for r in rows if r["id"] == decision_id), None)
    if not row:
        return {"status": "error", "msg": "决策不存在"}
    perf = get_decision_performance(decision_id)
    return {"status": "ok", "data": {**row, "performance": perf}}


@app.patch("/api/decisions/{decision_id}/link-tx")
def api_decisions_link(decision_id: int, req: LinkTransactionRequest):
    try:
        return {"status": "ok", "data": link_transaction(decision_id, req.transaction_id)}
    except ValueError as exc:
        from fastapi import HTTPException
        raise HTTPException(status_code=400, detail=str(exc))


@app.get("/api/decisions/{decision_id}/export")
def api_decisions_export(decision_id: int):
    md = export_decision_review(decision_id)
    return Response(content=md, media_type="text/plain; charset=utf-8")


@app.delete("/api/decisions/{decision_id}")
def api_decisions_cancel(decision_id: int):
    return {"status": "ok", "data": cancel_decision(decision_id)}


# ─── v2.3 Track A: 主题反查 peer 基金 ────────────────────────────────
@app.get("/api/funds/{code}/peers")
def api_fund_peers(code: str, limit: int = 10):
    """给定 seed 基金，返回同主题的 peer 基金列表"""
    return {"status": "ok", "data": find_peer_funds_by_theme(code, limit=limit)}


@app.get("/api/funds/{code}/ai-prompts/theme-analysis")
def api_fund_ai_theme_prompt(code: str):
    """渲染赛道分析 AI 入口 prompt"""
    from backend.ai_prompts import render_theme_analysis_prompt
    try:
        return {"status": "ok", "data": render_theme_analysis_prompt(code)}
    except FileNotFoundError as exc:
        from fastapi import HTTPException
        raise HTTPException(status_code=500, detail=f"prompt 模板缺失: {exc}")


# ─── v2.3 Batch 5: 策略模拟框架 ────────────────────────────────────
@app.get("/api/simulations/strategies")
def api_simulation_strategies():
    return {"status": "ok", "data": list_strategies()}


@app.post("/api/simulations")
def api_simulation_create(req: SimulationCreateRequest):
    from fastapi import HTTPException
    try:
        return {"status": "ok", "data": create_simulation(req.dict())}
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc))


@app.get("/api/simulations")
def api_simulation_list():
    return {"status": "ok", "data": list_simulations()}


@app.get("/api/simulations/{sim_id}")
def api_simulation_detail(sim_id: int):
    detail = get_simulation_detail(sim_id)
    if not detail:
        return {"status": "error", "msg": "simulation 不存在"}
    return {"status": "ok", "data": detail}


@app.post("/api/simulations/{sim_id}/run")
def api_simulation_run(sim_id: int):
    from fastapi import HTTPException

    sim = get_simulation_detail(sim_id)
    if not sim:
        raise HTTPException(status_code=404, detail="simulation 不存在")

    try:
        return {"status": "ok", "data": run_simulation(sim_id, mode=sim["mode"])}
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc))


@app.delete("/api/simulations/{sim_id}")
def api_simulation_delete(sim_id: int):
    from fastapi import HTTPException
    try:
        return {"status": "ok", "data": delete_simulation(sim_id)}
    except ValueError as exc:
        raise HTTPException(status_code=404, detail=str(exc))


@app.post("/api/simulations/{sim_id}/advance")
def api_simulation_advance(sim_id: int):
    """Forward mode 专用：单次推进一条 forward 模拟（到今天或 end_date，取较小）"""
    from fastapi import HTTPException
    try:
        return {"status": "ok", "data": advance_forward_simulation(sim_id)}
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc))
