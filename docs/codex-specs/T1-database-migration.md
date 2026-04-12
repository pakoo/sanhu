# T1 — 数据库迁移：新增 fund_holdings / fund_industry 表

## 任务说明

在 `backend/database.py` 中新增两张表的建表语句，向后兼容，不删除或修改任何现有表。

## 需要修改的文件

**`backend/database.py`**

找到 `init_db()` 函数，在函数体末尾（所有现有 `CREATE TABLE IF NOT EXISTS` 之后）追加：

```python
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
```

## 约束

- 只改 `init_db()` 函数，不修改其他函数
- 使用 `CREATE TABLE IF NOT EXISTS`，不是 `CREATE TABLE`
- 不改 `get_connection()`

## 验收命令

```bash
cd /Users/zhangpeicheng/jijin
python -c "
from backend.database import init_db, get_connection
init_db()
conn = get_connection()
tables = [r['name'] for r in conn.execute(\"SELECT name FROM sqlite_master WHERE type='table'\").fetchall()]
assert 'fund_holdings' in tables, 'fund_holdings 表不存在'
assert 'fund_industry' in tables, 'fund_industry 表不存在'
print('T1 PASS:', tables)
conn.close()
"
```

期望输出：`T1 PASS: [...]`（列表中包含 fund_holdings 和 fund_industry）
