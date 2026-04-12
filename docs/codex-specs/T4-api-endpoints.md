# T4 — API 端点：app.py 新增持仓相关路由

## 前置条件

T1、T2、T3 已完成。

## 需要修改的文件

**`app.py`**

### 第一步：新增 import

在文件顶部的 import 区域末尾追加：

```python
from backend.fetcher import refresh_holdings_for_all_funds
from backend.holdings_analysis import get_holdings_overlap, get_industry_breakdown, get_holdings_changes
from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.cron import CronTrigger
```

### 第二步：新增 startup 事件（APScheduler）

找到 FastAPI app 实例定义（`app = FastAPI(...)`），在它之后追加：

```python
@app.on_event("startup")
def start_scheduler():
    scheduler = BackgroundScheduler()
    # 每日 15:30 刷新净值
    scheduler.add_job(
        refresh_all_funds,
        CronTrigger(hour=15, minute=30, day_of_week="mon-fri"),
        id="refresh_nav",
        replace_existing=True,
    )
    # 每日 16:00 刷新持仓季报
    scheduler.add_job(
        refresh_holdings_for_all_funds,
        CronTrigger(hour=16, minute=0, day_of_week="mon-fri"),
        id="refresh_holdings",
        replace_existing=True,
    )
    scheduler.start()
```

### 第三步：新增 6 个 API 端点

在现有路由末尾追加（不改动现有路由）：

```python
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
def get_fund_industry(code: str):
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
    result = refresh_holdings_for_all_funds()
    return result
```

## 约束

- 不修改任何现有路由
- `refresh_all_funds` 在文件中已有 import，不要重复 import
- APScheduler 只注册一次，避免重复注册（`replace_existing=True` 已处理）

## requirements.txt 更新

在 `requirements.txt` 末尾追加：

```
apscheduler>=3.10.0
```

## 验收命令

```bash
cd /Users/zhangpeicheng/jijin

# 先安装新依赖
pip install apscheduler

# 启动服务后测试（在另一个终端运行 uvicorn app:app --reload）
# 测试端点
curl -s http://localhost:8000/api/holdings/006195/stocks | python -m json.tool | head -30
curl -s http://localhost:8000/api/holdings/overlap | python -m json.tool | head -20
```

期望：两个端点均返回合法 JSON（即使数据为空也应返回空列表结构）
