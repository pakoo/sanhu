-- sanhu-go 数据库 schema（由 data/sanhu.db 真实结构生成，与代码 SQL 对齐）
-- 重新生成原因：原 001_init.sql 列名与代码不符，fresh 安装会 no such column

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
, company TEXT, scope TEXT, inception_date TEXT, aum REAL, manager_since TEXT);
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
CREATE TABLE IF NOT EXISTS fund_holdings (
            code        TEXT NOT NULL,
            stock_code  TEXT NOT NULL,
            stock_name  TEXT NOT NULL,
            weight      REAL,
            industry    TEXT,
            report_date TEXT NOT NULL,
            PRIMARY KEY (code, stock_code, report_date)
        );
CREATE TABLE IF NOT EXISTS fund_industry (
            code        TEXT NOT NULL,
            industry    TEXT NOT NULL,
            weight      REAL,
            report_date TEXT NOT NULL,
            PRIMARY KEY (code, industry, report_date)
        );
CREATE TABLE IF NOT EXISTS index_pe_history (
            index_code  TEXT NOT NULL,
            date        TEXT NOT NULL,
            pe          REAL,
            pb          REAL,
            PRIMARY KEY (index_code, date)
        );
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
        );
CREATE TABLE IF NOT EXISTS watchlist (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        code TEXT NOT NULL UNIQUE,
        added_at TEXT NOT NULL DEFAULT (datetime('now')),
        notes TEXT DEFAULT '',
        FOREIGN KEY (code) REFERENCES funds(code)
    );
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
        );
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
        );
CREATE TABLE IF NOT EXISTS selector_cache (
            session_id TEXT PRIMARY KEY,
            answers_json TEXT,
            gaps_json TEXT,
            candidates_json TEXT,
            created_at TEXT
        );
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
        , is_virtual INTEGER NOT NULL DEFAULT 0, virtual_entry_nav REAL);
CREATE TABLE IF NOT EXISTS user_settings (
            key   TEXT PRIMARY KEY,
            value TEXT NOT NULL
        );
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
        );
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
        );
CREATE TABLE IF NOT EXISTS simulation_snapshots (
            sim_id         INTEGER NOT NULL,
            date           TEXT NOT NULL,
            cash           REAL NOT NULL,
            total_value    REAL NOT NULL,
            holdings_json  TEXT,
            PRIMARY KEY (sim_id, date),
            FOREIGN KEY (sim_id) REFERENCES simulations(id)
        );
CREATE INDEX IF NOT EXISTS idx_nav_code_date ON nav_history(code, date);
CREATE INDEX IF NOT EXISTS idx_transactions_code ON transactions(code, date);
CREATE INDEX IF NOT EXISTS idx_decisions_status ON decisions(status);
CREATE INDEX IF NOT EXISTS idx_decisions_code ON decisions(code);
CREATE INDEX IF NOT EXISTS idx_simulations_status ON simulations(status);
CREATE INDEX IF NOT EXISTS idx_simulations_mode ON simulations(mode);
CREATE INDEX IF NOT EXISTS idx_sim_trades_sim ON simulation_trades(sim_id, trade_date);
CREATE INDEX IF NOT EXISTS idx_nav_history_code_date ON nav_history (code, date);
CREATE INDEX IF NOT EXISTS idx_transactions_code_date ON transactions (code, date);
CREATE INDEX IF NOT EXISTS idx_simulation_trades_sim_id_trade_date ON simulation_trades (sim_id, trade_date);
