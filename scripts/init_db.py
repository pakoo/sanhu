#!/usr/bin/env python3
"""
init_db.py — 新用户初始化脚本

用法（在项目根目录运行）：
    /usr/bin/python3 scripts/init_db.py

做了什么：
  1. 创建 data/ 目录（如果不存在）
  2. 调用 backend/database.py 的 init_db() 建表 + 迁移
  3. 加载 data/seed.sql（包含市场数据：基金信息、净值历史、PE历史等）
  4. 打印各表行数确认

执行完后可以直接启动服务器：
    /usr/bin/python3 app.py
"""
import sqlite3
import os
import sys

# ── 路径配置 ───────────────────────────────────────────────────────────────
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

DB_PATH = os.path.join(ROOT, 'data', 'jijin.db')
SEED_PATH = os.path.join(ROOT, 'data', 'seed.sql')

USER_TABLES = [
    'holdings', 'transactions', 'target_allocation', 'watchlist',
    'decisions', 'simulations', 'simulation_trades', 'simulation_snapshots',
    'user_settings',
]
MARKET_TABLES = [
    'funds', 'fund_profile', 'fund_peer_ranks',
    'nav_history', 'index_pe_history', 'fund_holdings', 'fund_industry',
]


def main():
    # ── Step 1: 建表 ──────────────────────────────────────────────────────
    print('[1/3] 初始化数据库表结构...')
    os.makedirs(os.path.join(ROOT, 'data'), exist_ok=True)

    # 如果已有数据库，询问是否覆盖（仅覆盖市场数据，不动用户数据）
    if os.path.exists(DB_PATH):
        c = sqlite3.connect(DB_PATH)
        tables = [r[0] for r in c.execute(
            "SELECT name FROM sqlite_master WHERE type='table'"
        ).fetchall()]
        c.close()
        if tables:
            print(f'  已检测到现有数据库: {DB_PATH}')
            answer = input('  是否重新加载市场数据（seed.sql）？用户数据不受影响 [y/N]: ').strip().lower()
            if answer != 'y':
                print('  已取消。')
                sys.exit(0)

    from backend.database import init_db
    init_db()
    print('  表结构初始化完成')

    # ── Step 2: 加载种子数据 ───────────────────────────────────────────────
    print(f'\n[2/3] 加载市场数据种子 ({SEED_PATH})...')
    if not os.path.exists(SEED_PATH):
        print(f'  [跳过] seed.sql 不存在，市场数据将在首次使用时自动从网络获取（约需数分钟）')
    else:
        size_kb = os.path.getsize(SEED_PATH) // 1024
        print(f'  seed.sql 大小: {size_kb} KB')
        conn = sqlite3.connect(DB_PATH)
        conn.execute("PRAGMA foreign_keys=OFF")  # seed 期间关闭外键检查
        with open(SEED_PATH, 'r', encoding='utf-8') as f:
            sql = f.read()
        conn.executescript(sql)
        conn.execute("PRAGMA foreign_keys=ON")
        conn.close()
        print('  市场数据加载完成')

    # ── Step 3: 验证 ─────────────────────────────────────────────────────
    print('\n[3/3] 数据验证...')
    conn = sqlite3.connect(DB_PATH)
    print('  市场数据（来自种子）:')
    for t in MARKET_TABLES:
        try:
            n = conn.execute(f'SELECT COUNT(*) FROM {t}').fetchone()[0]
            status = '✓' if n > 0 else '○'
            print(f'    {status} {t}: {n} 行')
        except sqlite3.OperationalError:
            print(f'    ✗ {t}: 表不存在')
    print('  用户数据（空，等待录入）:')
    for t in USER_TABLES:
        try:
            n = conn.execute(f'SELECT COUNT(*) FROM {t}').fetchone()[0]
            print(f'    ○ {t}: {n} 行')
        except sqlite3.OperationalError:
            print(f'    ✗ {t}: 表不存在')
    conn.close()

    print('\n初始化完成！运行以下命令启动服务：')
    print('  /usr/bin/python3 app.py')


if __name__ == '__main__':
    main()
