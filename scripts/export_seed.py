#!/usr/bin/env python3
"""
export_seed.py — 把当前数据库里的市场数据导出为 data/seed.sql

用法（在项目根目录运行）：
    /usr/bin/python3 scripts/export_seed.py

什么时候重新导出：
  - 市场数据大批量更新后（如新增大量 fund_profile / nav_history）
  - 正式发布新版本前

不包含的表（用户数据）：
  holdings, transactions, target_allocation, watchlist,
  decisions, simulations, simulation_trades, simulation_snapshots, user_settings
"""
import sqlite3
import os
import sys
from datetime import datetime

# ── 路径配置 ───────────────────────────────────────────────────────────────
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB_PATH = os.path.join(ROOT, 'data', 'jijin.db')
OUT_PATH = os.path.join(ROOT, 'data', 'seed.sql')

# funds 必须在 nav_history 前（外键依赖）
SEED_TABLES = [
    'funds',
    'fund_profile',
    'fund_peer_ranks',
    'nav_history',
    'index_pe_history',
    'fund_holdings',
    'fund_industry',
]


def esc(v):
    if v is None:
        return 'NULL'
    if isinstance(v, str):
        return "'" + v.replace("'", "''") + "'"
    return str(v)


def export():
    if not os.path.exists(DB_PATH):
        print(f'[error] 数据库不存在: {DB_PATH}')
        sys.exit(1)

    c = sqlite3.connect(DB_PATH)
    lines = []
    lines.append('-- jijin market data seed')
    lines.append(f'-- generated: {datetime.now().strftime("%Y-%m-%d %H:%M")}')
    lines.append('-- tables: ' + ', '.join(SEED_TABLES))
    lines.append('-- DO NOT EDIT: regenerate via scripts/export_seed.py')
    lines.append('')
    lines.append('BEGIN TRANSACTION;')
    lines.append('')

    total_rows = 0
    for t in SEED_TABLES:
        try:
            rows = c.execute(f'SELECT * FROM {t}').fetchall()
        except sqlite3.OperationalError:
            lines.append(f'-- {t}: table not found (skip)')
            lines.append('')
            continue

        if not rows:
            lines.append(f'-- {t}: 0 rows (skip)')
            lines.append('')
            continue

        cols = [d[0] for d in c.execute(f'SELECT * FROM {t} LIMIT 0').description]
        col_str = ', '.join(cols)
        lines.append(f'-- {t}: {len(rows)} rows')
        lines.append(f'DELETE FROM {t};')

        for i in range(0, len(rows), 500):
            batch = rows[i:i + 500]
            vals = ['(' + ', '.join(esc(v) for v in row) + ')' for row in batch]
            lines.append(f'INSERT INTO {t} ({col_str}) VALUES')
            lines.append(',\n'.join(vals) + ';')

        lines.append('')
        total_rows += len(rows)
        print(f'  {t}: {len(rows)} rows')

    lines.append('COMMIT;')

    with open(OUT_PATH, 'w', encoding='utf-8') as f:
        f.write('\n'.join(lines))

    size_kb = os.path.getsize(OUT_PATH) // 1024
    print(f'\n导出完成: {OUT_PATH}')
    print(f'文件大小: {size_kb} KB  |  总行数: {total_rows}')
    c.close()


if __name__ == '__main__':
    export()
