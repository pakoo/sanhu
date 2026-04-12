"""数据库模块 - SQLite 建表与连接管理"""
import sqlite3
import os
from config import DB_PATH, DATA_DIR

SCHEMA_SQL = """
CREATE TABLE IF NOT EXISTS funds (
    code TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    fund_type TEXT,
    category TEXT,
    risk_level TEXT,
    fee_rate REAL,
    manager TEXT,
    benchmark TEXT,
    updated_at TEXT
);

CREATE TABLE IF NOT EXISTS nav_history (
    code TEXT NOT NULL,
    date TEXT NOT NULL,
    nav REAL NOT NULL,
    acc_nav REAL,
    daily_return REAL,
    PRIMARY KEY (code, date),
    FOREIGN KEY (code) REFERENCES funds(code)
);

CREATE TABLE IF NOT EXISTS holdings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    code TEXT NOT NULL,
    shares REAL NOT NULL,
    cost_amount REAL NOT NULL,
    buy_date TEXT,
    notes TEXT,
    FOREIGN KEY (code) REFERENCES funds(code)
);

CREATE TABLE IF NOT EXISTS transactions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    code TEXT NOT NULL,
    type TEXT NOT NULL,
    date TEXT NOT NULL,
    amount REAL NOT NULL,
    nav_at_trade REAL,
    shares REAL,
    fee REAL DEFAULT 0,
    notes TEXT,
    FOREIGN KEY (code) REFERENCES funds(code)
);

CREATE TABLE IF NOT EXISTS target_allocation (
    category TEXT PRIMARY KEY,
    target_pct REAL NOT NULL,
    min_pct REAL,
    max_pct REAL
);

CREATE TABLE IF NOT EXISTS backtest_results (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    strategy_name TEXT NOT NULL,
    params_json TEXT NOT NULL,
    result_json TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_nav_code_date ON nav_history(code, date);
CREATE INDEX IF NOT EXISTS idx_transactions_code ON transactions(code, date);
"""


def get_connection() -> sqlite3.Connection:
    """获取数据库连接"""
    os.makedirs(DATA_DIR, exist_ok=True)
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("PRAGMA foreign_keys=ON")
    return conn


def init_db():
    """初始化数据库表结构"""
    conn = get_connection()
    conn.executescript(SCHEMA_SQL)
    conn.execute("""
        CREATE TABLE IF NOT EXISTS fund_profile (
            code TEXT PRIMARY KEY,
            name TEXT,
            category TEXT,
            sub_style TEXT,
            theme_tags TEXT,
            region_tag TEXT,
            aum REAL,
            inception_date TEXT,
            manager TEXT,
            manager_since TEXT,
            fee_rate REAL,
            top10_concentration REAL,
            top_industry TEXT,
            top_industry_weight REAL,
            profile_source TEXT,
            updated_at TEXT
        )
    """)
    conn.execute("""
        CREATE TABLE IF NOT EXISTS selector_cache (
            session_id TEXT PRIMARY KEY,
            answers_json TEXT,
            gaps_json TEXT,
            candidates_json TEXT,
            created_at TEXT
        )
    """)
    conn.execute("""
        CREATE TABLE IF NOT EXISTS decisions (
            id                  INTEGER PRIMARY KEY AUTOINCREMENT,
            created_at          TEXT NOT NULL,
            decision_type       TEXT NOT NULL,
            code                TEXT NOT NULL,
            target_amount       REAL,
            target_nav_max      REAL,
            target_tp_pct       REAL,
            target_sl_pct       REAL,
            rationale_json      TEXT,
            source_session_id   TEXT,
            transaction_id      INTEGER,
            status              TEXT NOT NULL DEFAULT 'pending',
            executed_at         TEXT,
            closed_at           TEXT,
            closing_pnl_pct     REAL,
            notes               TEXT
        )
    """)
    conn.execute("CREATE INDEX IF NOT EXISTS idx_decisions_status ON decisions(status)")
    conn.execute("CREATE INDEX IF NOT EXISTS idx_decisions_code ON decisions(code)")
    conn.execute("""
        CREATE TABLE IF NOT EXISTS fund_holdings (
            code        TEXT NOT NULL,
            stock_code  TEXT NOT NULL,
            stock_name  TEXT NOT NULL,
            weight      REAL,
            industry    TEXT,
            report_date TEXT NOT NULL,
            PRIMARY KEY (code, stock_code, report_date)
        )
    """)
    conn.execute("""
        CREATE TABLE IF NOT EXISTS fund_industry (
            code        TEXT NOT NULL,
            industry    TEXT NOT NULL,
            weight      REAL,
            report_date TEXT NOT NULL,
            PRIMARY KEY (code, industry, report_date)
        )
    """)
    conn.execute("""
        CREATE TABLE IF NOT EXISTS index_pe_history (
            index_code  TEXT NOT NULL,
            date        TEXT NOT NULL,
            pe          REAL,
            pb          REAL,
            PRIMARY KEY (index_code, date)
        )
    """)
    conn.execute("""
        CREATE TABLE IF NOT EXISTS fund_scores (
            code                  TEXT NOT NULL,
            date                  TEXT NOT NULL,
            total_score           REAL,
            sharpe_score          REAL,
            drawdown_score        REAL,
            return_score          REAL,
            volatility_score      REAL,
            sharpe_raw            REAL,
            max_drawdown_raw      REAL,
            annualized_return_raw REAL,
            volatility_raw        REAL,
            PRIMARY KEY (code, date)
        )
    """)
    conn.execute("""CREATE TABLE IF NOT EXISTS watchlist (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        code TEXT NOT NULL UNIQUE,
        added_at TEXT NOT NULL DEFAULT (datetime('now')),
        notes TEXT DEFAULT '',
        FOREIGN KEY (code) REFERENCES funds(code)
    )""")
    conn.execute("""
        CREATE TABLE IF NOT EXISTS user_settings (
            key   TEXT PRIMARY KEY,
            value TEXT NOT NULL
        )
    """)
    conn.execute(
        "INSERT OR IGNORE INTO user_settings(key, value) VALUES ('risk_level', 'moderate')"
    )
    conn.execute("""
        CREATE TABLE IF NOT EXISTS fund_peer_ranks (
            code        TEXT PRIMARY KEY,
            category    TEXT NOT NULL,
            peer_total  INTEGER,
            rank_1m     INTEGER, pct_1m REAL,
            rank_3m     INTEGER, pct_3m REAL,
            rank_6m     INTEGER, pct_6m REAL,
            rank_1y     INTEGER, pct_1y REAL,
            rank_3y     INTEGER, pct_3y REAL,
            updated_at  TEXT
        )
    """)
    # ─── v2.3 simulation framework tables ─────────────────────────────
    conn.execute("""
        CREATE TABLE IF NOT EXISTS simulations (
            id              INTEGER PRIMARY KEY AUTOINCREMENT,
            name            TEXT NOT NULL,
            strategy_name   TEXT NOT NULL,
            params_json     TEXT,
            fund_pool_json  TEXT,
            initial_capital REAL NOT NULL,
            start_date      TEXT NOT NULL,
            end_date        TEXT,
            mode            TEXT NOT NULL DEFAULT 'backtest',
            status          TEXT NOT NULL DEFAULT 'pending',
            current_date    TEXT,
            created_at      TEXT NOT NULL,
            updated_at      TEXT,
            notes           TEXT
        )
    """)
    conn.execute("CREATE INDEX IF NOT EXISTS idx_simulations_status ON simulations(status)")
    conn.execute("CREATE INDEX IF NOT EXISTS idx_simulations_mode ON simulations(mode)")
    conn.execute("""
        CREATE TABLE IF NOT EXISTS simulation_trades (
            id          INTEGER PRIMARY KEY AUTOINCREMENT,
            sim_id      INTEGER NOT NULL,
            trade_date  TEXT NOT NULL,
            code        TEXT NOT NULL,
            action      TEXT NOT NULL,
            shares      REAL NOT NULL,
            price       REAL NOT NULL,
            amount      REAL NOT NULL,
            reason      TEXT,
            FOREIGN KEY (sim_id) REFERENCES simulations(id)
        )
    """)
    conn.execute("CREATE INDEX IF NOT EXISTS idx_sim_trades_sim ON simulation_trades(sim_id, trade_date)")
    conn.execute("""
        CREATE TABLE IF NOT EXISTS simulation_snapshots (
            sim_id         INTEGER NOT NULL,
            date           TEXT NOT NULL,
            cash           REAL NOT NULL,
            total_value    REAL NOT NULL,
            holdings_json  TEXT,
            PRIMARY KEY (sim_id, date),
            FOREIGN KEY (sim_id) REFERENCES simulations(id)
        )
    """)
    conn.commit()
    _migrate(conn)
    conn.close()


# 新增列的轻量级迁移（SQLite 不支持 IF NOT EXISTS ALTER）
_COLUMN_MIGRATIONS = [
    ("funds",     "company",           "TEXT"),
    ("funds",     "scope",             "TEXT"),
    ("funds",     "inception_date",    "TEXT"),
    ("funds",     "aum",               "REAL"),
    ("funds",     "manager_since",     "TEXT"),
    # v2.3 Layer 1 单笔虚拟决策
    ("decisions", "is_virtual",        "INTEGER NOT NULL DEFAULT 0"),
    ("decisions", "virtual_entry_nav", "REAL"),
]


def _migrate(conn: sqlite3.Connection):
    """在已有表上增量补列"""
    for table, col, coltype in _COLUMN_MIGRATIONS:
        existing = {r[1] for r in conn.execute(f"PRAGMA table_info({table})").fetchall()}
        if col not in existing:
            try:
                conn.execute(f"ALTER TABLE {table} ADD COLUMN {col} {coltype}")
            except sqlite3.OperationalError as e:
                print(f"[migrate] {table}.{col} 失败: {e}")
    conn.commit()


def execute_query(sql: str, params: tuple = (), fetchone: bool = False):
    """执行查询并返回结果"""
    conn = get_connection()
    try:
        cursor = conn.execute(sql, params)
        if fetchone:
            result = cursor.fetchone()
        else:
            result = cursor.fetchall()
        conn.commit()
        return result
    finally:
        conn.close()


def execute_many(sql: str, params_list: list):
    """批量执行"""
    conn = get_connection()
    try:
        conn.executemany(sql, params_list)
        conn.commit()
    finally:
        conn.close()


# ── user_settings helpers ────────────────────────────────────────────────

def get_setting(key: str, default=None):
    """读取一条用户设置。找不到或表不存在时返回 default。"""
    conn = get_connection()
    try:
        row = conn.execute(
            "SELECT value FROM user_settings WHERE key=?", (key,)
        ).fetchone()
        return row["value"] if row else default
    except sqlite3.OperationalError:
        return default
    finally:
        conn.close()


def set_setting(key: str, value: str):
    """写入一条用户设置（upsert）。"""
    conn = get_connection()
    try:
        conn.execute(
            "INSERT INTO user_settings(key, value) VALUES(?, ?) "
            "ON CONFLICT(key) DO UPDATE SET value=excluded.value",
            (key, str(value)),
        )
        conn.commit()
    finally:
        conn.close()


def get_all_settings() -> dict:
    conn = get_connection()
    try:
        rows = conn.execute("SELECT key, value FROM user_settings").fetchall()
        return {row["key"]: row["value"] for row in rows}
    except sqlite3.OperationalError:
        return {}
    finally:
        conn.close()


if __name__ == "__main__":
    init_db()
    print(f"数据库已初始化: {DB_PATH}")
