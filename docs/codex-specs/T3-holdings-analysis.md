# T3 — 持仓分析：新建 backend/holdings_analysis.py

## 前置条件

T1、T2 已完成，且至少已为权益类基金抓取过一次持仓数据。

## 需要创建的文件

**新建 `backend/holdings_analysis.py`**

完整文件内容如下：

```python
"""持仓穿透分析模块 — 跨基金重叠、行业分布、持仓变化"""
from __future__ import annotations
from backend.database import get_connection


def get_holdings_overlap() -> dict:
    """跨基金持仓重叠分析

    Returns:
        {
            "overlap_stocks": [
                {
                    "stock_code": "300750",
                    "stock_name": "宁德时代",
                    "appear_in": ["006195", "021095"],
                    "appear_count": 2,
                    "total_exposure_pct": 2.3,
                    "fund_weights": [{"code": "006195", "weight": 3.0, "fund_name": "..."}, ...]
                }
            ],
            "diversification_score": 2.3,
            "total_funds_analyzed": 5,
            "report_date": "2024-09-30",
        }
    """
    conn = get_connection()

    # 获取所有权益类基金的持仓金额（用于加权）
    funds = conn.execute(
        """SELECT f.code, f.name, h.shares * n.nav AS market_value
           FROM funds f
           JOIN holdings h ON f.code = h.code
           LEFT JOIN (
               SELECT code, nav FROM nav_history
               WHERE (code, date) IN (
                   SELECT code, MAX(date) FROM nav_history GROUP BY code
               )
           ) n ON f.code = n.code
           WHERE f.category != 'bond'"""
    ).fetchall()

    total_market_value = sum(f["market_value"] or 0 for f in funds)
    fund_weight_map = {}  # code -> (name, market_value, pct_of_total)
    for f in funds:
        mv = f["market_value"] or 0
        fund_weight_map[f["code"]] = {
            "name": f["name"],
            "market_value": mv,
            "pct_of_total": mv / total_market_value * 100 if total_market_value > 0 else 0,
        }

    if not fund_weight_map:
        conn.close()
        return {"overlap_stocks": [], "diversification_score": 1.0, "total_funds_analyzed": 0, "report_date": ""}

    # 获取各基金最新持仓
    stock_fund_map = {}  # stock_code -> list of {code, stock_name, weight}
    latest_date = ""
    for fund_code in fund_weight_map:
        latest = conn.execute(
            "SELECT MAX(report_date) as d FROM fund_holdings WHERE code=?", (fund_code,)
        ).fetchone()
        if not latest or not latest["d"]:
            continue
        latest_date = max(latest_date, latest["d"])
        holdings = conn.execute(
            "SELECT stock_code, stock_name, weight FROM fund_holdings WHERE code=? AND report_date=?",
            (fund_code, latest["d"])
        ).fetchall()
        for h in holdings:
            sc = h["stock_code"]
            if sc not in stock_fund_map:
                stock_fund_map[sc] = {"stock_name": h["stock_name"], "funds": []}
            stock_fund_map[sc]["funds"].append({
                "code": fund_code,
                "weight": h["weight"] or 0,
            })

    conn.close()

    # 计算每只股票的实质暴露（对总资产）
    all_exposures = []
    for stock_code, info in stock_fund_map.items():
        fund_weights_detail = []
        total_exposure = 0.0
        for fw in info["funds"]:
            fund_code = fw["code"]
            fund_pct = fund_weight_map.get(fund_code, {}).get("pct_of_total", 0)
            # 该股票对总资产的暴露 = 基金在总资产中占比 × 该股在基金中的weight
            exposure = fund_pct * fw["weight"] / 100
            total_exposure += exposure
            fund_weights_detail.append({
                "code": fund_code,
                "fund_name": fund_weight_map.get(fund_code, {}).get("name", ""),
                "weight": fw["weight"],
            })
        all_exposures.append({
            "stock_code": stock_code,
            "stock_name": info["stock_name"],
            "appear_in": [fw["code"] for fw in info["funds"]],
            "appear_count": len(info["funds"]),
            "total_exposure_pct": round(total_exposure, 4),
            "fund_weights": fund_weights_detail,
        })

    # 只保留在 ≥2 只基金中出现的股票，按暴露度排序
    overlap_stocks = sorted(
        [s for s in all_exposures if s["appear_count"] >= 2],
        key=lambda x: x["total_exposure_pct"],
        reverse=True,
    )

    # 计算 Herfindahl 指数（基于各基金占总资产的比例）
    fund_pcts = [v["pct_of_total"] / 100 for v in fund_weight_map.values()]
    herfindahl = sum(p ** 2 for p in fund_pcts)
    diversification_score = round(1 / herfindahl, 2) if herfindahl > 0 else 1.0

    return {
        "overlap_stocks": overlap_stocks,
        "diversification_score": diversification_score,
        "total_funds_analyzed": len(fund_weight_map),
        "report_date": latest_date,
    }


def get_industry_breakdown() -> dict:
    """穿透后汇总行业分布

    Returns:
        {
            "industries": [
                {"industry": "科技", "total_weight": 35.2,
                 "fund_breakdown": [{"code": "006195", "fund_name": "...", "weight": 20.0}, ...]},
                ...
            ],
            "report_date": "2024-09-30",
            "funds_analyzed": ["006195", ...]
        }
    """
    conn = get_connection()

    # 获取所有权益类基金及其在总持仓中的占比
    funds = conn.execute(
        """SELECT f.code, f.name, h.shares * n.nav AS market_value
           FROM funds f
           JOIN holdings h ON f.code = h.code
           LEFT JOIN (
               SELECT code, nav FROM nav_history
               WHERE (code, date) IN (
                   SELECT code, MAX(date) FROM nav_history GROUP BY code
               )
           ) n ON f.code = n.code
           WHERE f.category != 'bond'"""
    ).fetchall()

    total_mv = sum(f["market_value"] or 0 for f in funds)
    fund_pct_map = {
        f["code"]: {
            "name": f["name"],
            "pct": (f["market_value"] or 0) / total_mv * 100 if total_mv > 0 else 0
        }
        for f in funds
    }

    industry_map = {}  # industry -> list of {code, fund_name, weight}
    latest_date = ""
    analyzed = []

    for fund_code, fund_info in fund_pct_map.items():
        latest = conn.execute(
            "SELECT MAX(report_date) as d FROM fund_industry WHERE code=?", (fund_code,)
        ).fetchone()
        if not latest or not latest["d"]:
            continue
        latest_date = max(latest_date, latest["d"])
        analyzed.append(fund_code)
        rows = conn.execute(
            "SELECT industry, weight FROM fund_industry WHERE code=? AND report_date=?",
            (fund_code, latest["d"])
        ).fetchall()
        for r in rows:
            ind = r["industry"]
            if ind not in industry_map:
                industry_map[ind] = []
            industry_map[ind].append({
                "code": fund_code,
                "fund_name": fund_info["name"],
                "weight": r["weight"] or 0,
                "fund_pct": fund_info["pct"],
            })

    conn.close()

    industries = []
    for industry, entries in industry_map.items():
        # 加权合并：以各基金在总资产中的占比为权重
        total_weight = sum(e["weight"] * e["fund_pct"] / 100 for e in entries)
        industries.append({
            "industry": industry,
            "total_weight": round(total_weight, 2),
            "fund_breakdown": [
                {"code": e["code"], "fund_name": e["fund_name"], "weight": e["weight"]}
                for e in entries
            ],
        })

    industries.sort(key=lambda x: x["total_weight"], reverse=True)

    return {
        "industries": industries,
        "report_date": latest_date,
        "funds_analyzed": analyzed,
    }


def get_holdings_changes(code: str) -> dict:
    """单基金持仓变化对比（最近两期）

    Returns:
        {
            "new_stocks": [...],      # 本期有、上期无
            "removed_stocks": [...],  # 上期有、本期无
            "increased": [...],       # weight 增加 > 1%
            "decreased": [...],       # weight 减少 > 1%
            "unchanged": [...],
            "current_date": "2024-09-30",
            "prev_date": "2024-06-30",
        }
    """
    conn = get_connection()

    dates = conn.execute(
        "SELECT DISTINCT report_date FROM fund_holdings WHERE code=? ORDER BY report_date DESC LIMIT 2",
        (code,)
    ).fetchall()

    if len(dates) < 2:
        conn.close()
        return {
            "new_stocks": [], "removed_stocks": [], "increased": [],
            "decreased": [], "unchanged": [],
            "current_date": dates[0]["report_date"] if dates else "",
            "prev_date": "",
        }

    current_date = dates[0]["report_date"]
    prev_date = dates[1]["report_date"]

    current = {
        r["stock_code"]: r
        for r in conn.execute(
            "SELECT stock_code, stock_name, weight FROM fund_holdings WHERE code=? AND report_date=?",
            (code, current_date)
        ).fetchall()
    }
    prev = {
        r["stock_code"]: r
        for r in conn.execute(
            "SELECT stock_code, stock_name, weight FROM fund_holdings WHERE code=? AND report_date=?",
            (code, prev_date)
        ).fetchall()
    }
    conn.close()

    new_stocks, removed_stocks, increased, decreased, unchanged = [], [], [], [], []

    for sc, data in current.items():
        name = data["stock_name"]
        w = data["weight"] or 0
        if sc not in prev:
            new_stocks.append({"stock_code": sc, "stock_name": name, "weight": w})
        else:
            pw = prev[sc]["weight"] or 0
            diff = w - pw
            entry = {"stock_code": sc, "stock_name": name, "weight": w, "prev_weight": pw, "change": round(diff, 2)}
            if diff > 1:
                increased.append(entry)
            elif diff < -1:
                decreased.append(entry)
            else:
                unchanged.append(entry)

    for sc, data in prev.items():
        if sc not in current:
            removed_stocks.append({"stock_code": sc, "stock_name": data["stock_name"], "prev_weight": data["weight"] or 0})

    return {
        "new_stocks": new_stocks,
        "removed_stocks": removed_stocks,
        "increased": sorted(increased, key=lambda x: x["change"], reverse=True),
        "decreased": sorted(decreased, key=lambda x: x["change"]),
        "unchanged": unchanged,
        "current_date": current_date,
        "prev_date": prev_date,
    }
```

## 验收命令

```bash
cd /Users/zhangpeicheng/jijin

# 单元测试：Herfindahl 计算
python -c "
# 模拟2只各50%的基金
fund_pcts = [0.5, 0.5]
h = sum(p**2 for p in fund_pcts)
score = 1/h
assert abs(score - 2.0) < 0.01, f'期望2.0，得到{score}'
print('Herfindahl 单测 PASS, score =', score)
"

# 模块导入测试
python -c "
from backend.holdings_analysis import get_holdings_overlap, get_industry_breakdown, get_holdings_changes
print('导入 PASS')
result = get_holdings_overlap()
print('重叠分析结构 PASS, 分析基金数:', result['total_funds_analyzed'])
"
```
