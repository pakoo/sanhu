"""同类基金排名 - akshare 数据源

通过 akshare 拉取全市场开放式基金按类型的历史收益排名，
对每个周期计算降序排名和百分位，写入 fund_peer_ranks 表。
"""
from __future__ import annotations

from datetime import datetime

from backend.database import get_connection

# 内部 category → akshare symbol 值
# "index" 并不是本地 funds.category 里的合法值，但 akshare 把指数基金单独成池，
# 本地指数基金（如 006600 人保沪深300A）通常被归到 mixed/equity，
# 需要单独拉一次让它们能按真正的指数基金同行比较。
AK_SYMBOL_MAP = {
    "equity": "股票型",
    "mixed":  "混合型",
    "bond":   "债券型",
    "qdii":   "QDII",
    "index":  "指数型",
}

# (字段后缀, akshare 列名)
_PERIODS = [
    ("1m", "近1月"),
    ("3m", "近3月"),
    ("6m", "近6月"),
    ("1y", "近1年"),
    ("3y", "近3年"),
]


def refresh_peer_ranks() -> dict:
    """拉取 4 个类别的全市场排名，INSERT OR REPLACE 写入 fund_peer_ranks

    Returns:
        {"status": "ok" | "partial", "counts": {...}, "errors": [...]}
    """
    try:
        import akshare as ak  # lazy import
    except ImportError:
        return {"status": "error", "error": "akshare 未安装"}

    conn = get_connection()
    counts: dict[str, int] = {}
    errors: list[str] = []

    for category, symbol in AK_SYMBOL_MAP.items():
        try:
            df = ak.fund_open_fund_rank_em(symbol=symbol)
        except Exception as e:
            errors.append(f"{symbol}: {e}")
            continue
        if df is None or df.empty:
            errors.append(f"{symbol}: 空数据")
            continue

        written = _write_rank_rows(conn, category, df)
        counts[category] = written

    conn.commit()
    conn.close()
    return {
        "status": "partial" if errors else "ok",
        "counts": counts,
        "errors": errors,
    }


def _write_rank_rows(conn, category: str, df) -> int:
    """对每只基金计算各周期排名与百分位，批量 INSERT OR REPLACE

    排名口径：每个周期独立按收益降序（NaN 剔除），分母为该周期的有效样本数。
    这样极大值 NaN 的基金不会被错误地算入分位。
    """
    total_all = len(df)

    rank_maps: dict[str, dict[str, tuple[int, int]]] = {}
    for key, col in _PERIODS:
        if col not in df.columns:
            continue
        sub = df[["基金代码", col]].dropna()
        sub = sub.sort_values(col, ascending=False).reset_index(drop=True)
        n = len(sub)
        rank_maps[key] = {
            str(row["基金代码"]): (i + 1, n)
            for i, row in sub.iterrows()
        }

    now = datetime.now().isoformat(timespec="seconds")
    rows = []
    for _, row in df.iterrows():
        code = str(row["基金代码"]).zfill(6)
        data: dict = {
            "code": code,
            "category": category,
            "peer_total": total_all,
            "updated_at": now,
        }
        for key, _col in _PERIODS:
            rk = rank_maps.get(key, {}).get(code)
            if rk:
                rank, n = rk
                data[f"rank_{key}"] = rank
                data[f"pct_{key}"] = round(rank / n * 100, 2)
            else:
                data[f"rank_{key}"] = None
                data[f"pct_{key}"] = None
        rows.append(data)

    conn.executemany(
        """
        INSERT OR REPLACE INTO fund_peer_ranks
          (code, category, peer_total,
           rank_1m, pct_1m, rank_3m, pct_3m,
           rank_6m, pct_6m, rank_1y, pct_1y,
           rank_3y, pct_3y, updated_at)
        VALUES
          (:code, :category, :peer_total,
           :rank_1m, :pct_1m, :rank_3m, :pct_3m,
           :rank_6m, :pct_6m, :rank_1y, :pct_1y,
           :rank_3y, :pct_3y, :updated_at)
        """,
        rows,
    )
    return len(rows)


def get_peer_rank(code: str) -> dict | None:
    """查询单只基金的同类排名快照"""
    conn = get_connection()
    row = conn.execute(
        "SELECT * FROM fund_peer_ranks WHERE code=?", (code,)
    ).fetchone()
    conn.close()
    return dict(row) if row else None


if __name__ == "__main__":
    print(refresh_peer_ranks())
