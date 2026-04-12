"""关注列表模块"""
from __future__ import annotations
from backend.database import get_connection


def get_watchlist() -> list[dict]:
    conn = get_connection()
    rows = conn.execute("SELECT code, added_at FROM watchlist ORDER BY added_at DESC").fetchall()
    if not rows:
        conn.close()
        return []

    result = []
    for row in rows:
        code = row["code"]

        fund = conn.execute(
            "SELECT name, category FROM funds WHERE code=?", (code,)
        ).fetchone()
        name = fund["name"] if fund else code
        category = fund["category"] if fund else ""

        holding = conn.execute(
            "SELECT shares FROM holdings WHERE code=?", (code,)
        ).fetchone()
        is_holding = holding is not None

        nav_rows = conn.execute(
            "SELECT date, nav, acc_nav, daily_return FROM nav_history "
            "WHERE code=? ORDER BY date DESC LIMIT 2", (code,)
        ).fetchall()

        latest_nav = None
        daily_return = None
        current_value = 0.0

        if nav_rows:
            latest_nav = nav_rows[0]["nav"]
            daily_return = nav_rows[0]["daily_return"]
            if is_holding and latest_nav:
                current_value = holding["shares"] * latest_nav

        nav_7d = conn.execute(
            "SELECT acc_nav FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 1 OFFSET 7",
            (code,)
        ).fetchone()
        ret_7d = None
        if nav_rows and nav_7d and nav_7d["acc_nav"] and nav_rows[0]["acc_nav"]:
            try:
                ret_7d = round((nav_rows[0]["acc_nav"] / nav_7d["acc_nav"] - 1) * 100, 2)
            except Exception:
                pass

        nav_1m = conn.execute(
            "SELECT acc_nav FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 1 OFFSET 21",
            (code,)
        ).fetchone()
        ret_1m = None
        if nav_rows and nav_1m and nav_1m["acc_nav"] and nav_rows[0]["acc_nav"]:
            try:
                ret_1m = round((nav_rows[0]["acc_nav"] / nav_1m["acc_nav"] - 1) * 100, 2)
            except Exception:
                pass

        spark_rows = conn.execute(
            "SELECT nav FROM nav_history WHERE code=? ORDER BY date DESC LIMIT 30",
            (code,)
        ).fetchall()
        sparkline = list(reversed([r["nav"] for r in spark_rows if r["nav"] is not None]))

        score_row = conn.execute(
            "SELECT total_score FROM fund_scores "
            "WHERE code=? ORDER BY date DESC LIMIT 1", (code,)
        ).fetchone()
        total_score = round(score_row["total_score"], 1) if score_row and score_row["total_score"] is not None else None

        result.append({
            "code": code,
            "name": name,
            "category": category,
            "added_at": row["added_at"],
            "is_holding": is_holding,
            "current_value": round(current_value, 2),
            "latest_nav": round(latest_nav, 4) if latest_nav else None,
            "daily_return": round(daily_return, 4) if daily_return is not None else None,
            "ret_7d": ret_7d,
            "ret_1m": ret_1m,
            "sparkline": sparkline,
            "total_score": total_score,
        })

    conn.close()
    return result


def add_to_watchlist(code: str) -> dict:
    try:
        from backend.fetcher import fetch_fund_detail, fetch_nav_history
        fetch_fund_detail(code)
        fetch_nav_history(code)
    except Exception:
        pass

    conn = get_connection()
    conn.execute(
        "INSERT OR IGNORE INTO watchlist (code, added_at) VALUES (?, datetime('now'))",
        (code,)
    )
    conn.commit()
    conn.close()
    return {"status": "ok", "code": code}


def remove_from_watchlist(code: str) -> dict:
    conn = get_connection()
    conn.execute("DELETE FROM watchlist WHERE code=?", (code,))
    conn.commit()
    conn.close()
    return {"status": "ok", "code": code}


def bulk_add_to_watchlist(codes: list[str]) -> int:
    if not codes:
        return 0
    conn = get_connection()
    count = 0
    for code in codes:
        try:
            conn.execute(
                "INSERT OR IGNORE INTO watchlist (code, added_at) VALUES (?, datetime('now'))",
                (code,)
            )
            count += 1
        except Exception:
            pass
    conn.commit()
    conn.close()
    return count


def refresh_watchlist_missing_holdings() -> dict:
    """每日补抓：watchlist 里持股数据为空或超过 30 天未更新的基金"""
    from backend.fetcher import fetch_fund_holdings
    from backend.database import get_connection
    import datetime

    conn = get_connection()
    wl_codes = [r["code"] for r in conn.execute("SELECT code FROM watchlist").fetchall()]
    conn.close()

    skipped = []
    updated = []
    failed = []

    for code in wl_codes:
        conn = get_connection()
        row = conn.execute(
            "SELECT MAX(report_date) as d FROM fund_holdings WHERE code=?", (code,)
        ).fetchone()
        conn.close()

        last_date = row["d"] if row else None
        needs_refresh = (last_date is None) or (
            datetime.date.fromisoformat(last_date)
            < datetime.date.today() - datetime.timedelta(days=30)
        )

        if not needs_refresh:
            skipped.append(code)
            continue

        try:
            result = fetch_fund_holdings(code)
            updated.append(code)
            print(f"[watchlist_refresh] {code} 持股已更新，{len(result)} 条")
        except Exception as e:
            failed.append(code)
            print(f"[watchlist_refresh] {code} 失败: {e}")

    return {"updated": updated, "skipped": skipped, "failed": failed}
