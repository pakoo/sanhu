# T2 — 持仓抓取：fetcher.py 新增三个函数

## 前置条件

T1 已完成（fund_holdings / fund_industry 表存在）。

## 需要修改的文件

**`backend/fetcher.py`**

在文件末尾（`refresh_all_funds()` 之后）追加以下三个函数。**不修改任何现有函数。**

---

### 函数 1：`fetch_fund_holdings`

```python
def fetch_fund_holdings(code: str, save: bool = True) -> list[dict]:
    """抓取基金季报前十大持仓股票

    债券基金（category='bond'）直接返回 []
    
    Returns:
        [{"stock_code": "600519", "stock_name": "贵州茅台", 
          "weight": 9.5, "industry": "食品饮料", "report_date": "2024-09-30"}, ...]
    """
    # 检查是否为债券基金，是则跳过
    conn = get_connection()
    row = conn.execute("SELECT category FROM funds WHERE code=?", (code,)).fetchone()
    conn.close()
    if row and row["category"] == "bond":
        return []

    url = f"https://fundf10.eastmoney.com/FundArchivesDatas.aspx?type=jjcc&code={code}&topline=10"
    try:
        resp = requests.get(url, headers=HEADERS, timeout=15)
        text = resp.text.strip()

        # 响应为 JSONP 格式：var apidata={ content:"...", arryList:[...], ... }
        # 需要提取 arryList 中的持仓数据
        # 先提取 report_date（从 content 中的季报日期）
        date_match = re.search(r'(\d{4}年\d{1,2}月\d{1,2}日)', text)
        report_date = ""
        if date_match:
            from datetime import datetime
            try:
                dt = datetime.strptime(date_match.group(1), "%Y年%m月%d日")
                report_date = dt.strftime("%Y-%m-%d")
            except ValueError:
                pass

        # 提取持仓数据：解析 HTML table（arryList 是 HTML 格式）
        # Eastmoney 返回的是 HTML 片段，从 <tr> 中提取
        rows = re.findall(r'<tr[^>]*>(.*?)</tr>', text, re.DOTALL)
        results = []
        for row in rows:
            cells = re.findall(r'<td[^>]*>(.*?)</td>', row, re.DOTALL)
            if len(cells) < 7:
                continue
            # 去除 HTML 标签
            def strip_html(s):
                return re.sub(r'<[^>]+>', '', s).strip()
            
            stock_code = strip_html(cells[1])
            stock_name = strip_html(cells[2])
            # weight 在第7列（cells[6]），格式 "9.52%"
            weight_str = strip_html(cells[6]).replace('%', '')
            
            if not stock_code or not stock_name:
                continue
            try:
                weight = float(weight_str) if weight_str else None
            except ValueError:
                weight = None
            
            results.append({
                "stock_code": stock_code,
                "stock_name": stock_name,
                "weight": weight,
                "industry": None,  # 行业信息从 fetch_fund_industry 获取
                "report_date": report_date,
            })

        if save and results and report_date:
            conn = get_connection()
            conn.executemany(
                """INSERT OR REPLACE INTO fund_holdings 
                   (code, stock_code, stock_name, weight, industry, report_date)
                   VALUES (?, ?, ?, ?, ?, ?)""",
                [(code, r["stock_code"], r["stock_name"], r["weight"], r["industry"], r["report_date"])
                 for r in results]
            )
            conn.commit()
            conn.close()

        return results

    except Exception as e:
        print(f"获取 {code} 持仓数据失败: {e}")
        return []
```

---

### 函数 2：`fetch_fund_industry`

```python
def fetch_fund_industry(code: str, save: bool = True) -> list[dict]:
    """抓取基金季报行业分布

    债券基金（category='bond'）直接返回 []

    Returns:
        [{"industry": "科技", "weight": 35.2, "report_date": "2024-09-30"}, ...]
    """
    conn = get_connection()
    row = conn.execute("SELECT category FROM funds WHERE code=?", (code,)).fetchone()
    conn.close()
    if row and row["category"] == "bond":
        return []

    url = f"https://fundf10.eastmoney.com/FundArchivesDatas.aspx?type=hycc&code={code}"
    try:
        resp = requests.get(url, headers=HEADERS, timeout=15)
        text = resp.text.strip()

        date_match = re.search(r'(\d{4}年\d{1,2}月\d{1,2}日)', text)
        report_date = ""
        if date_match:
            from datetime import datetime
            try:
                dt = datetime.strptime(date_match.group(1), "%Y年%m月%d日")
                report_date = dt.strftime("%Y-%m-%d")
            except ValueError:
                pass

        rows = re.findall(r'<tr[^>]*>(.*?)</tr>', text, re.DOTALL)
        results = []
        for row in rows:
            cells = re.findall(r'<td[^>]*>(.*?)</td>', row, re.DOTALL)
            if len(cells) < 3:
                continue
            def strip_html(s):
                return re.sub(r'<[^>]+>', '', s).strip()
            
            industry = strip_html(cells[0])
            weight_str = strip_html(cells[1]).replace('%', '')
            
            if not industry:
                continue
            try:
                weight = float(weight_str) if weight_str else None
            except ValueError:
                weight = None

            results.append({
                "industry": industry,
                "weight": weight,
                "report_date": report_date,
            })

        if save and results and report_date:
            conn = get_connection()
            conn.executemany(
                """INSERT OR REPLACE INTO fund_industry
                   (code, industry, weight, report_date)
                   VALUES (?, ?, ?, ?)""",
                [(code, r["industry"], r["weight"], r["report_date"])
                 for r in results]
            )
            conn.commit()
            conn.close()

        return results

    except Exception as e:
        print(f"获取 {code} 行业分布失败: {e}")
        return []
```

---

### 函数 3：`refresh_holdings_for_all_funds`

```python
def refresh_holdings_for_all_funds() -> dict:
    """批量刷新所有权益类基金的持仓和行业数据

    Returns:
        {"refreshed": [...], "skipped": [...], "errors": [...]}
    """
    conn = get_connection()
    funds = conn.execute("SELECT code, category FROM funds").fetchall()
    conn.close()

    refreshed = []
    skipped = []
    errors = []

    for fund in funds:
        code = fund["code"]
        category = fund["category"]
        
        if category == "bond":
            skipped.append(code)
            continue
        
        try:
            fetch_fund_holdings(code)
            time.sleep(REQUEST_DELAY)
            fetch_fund_industry(code)
            time.sleep(REQUEST_DELAY)
            refreshed.append(code)
        except Exception as e:
            print(f"刷新 {code} 持仓失败: {e}")
            errors.append(code)

    return {"refreshed": refreshed, "skipped": skipped, "errors": errors}
```

## 约束

- 开头加 `from __future__ import annotations`（文件已有，无需重复）
- 不修改任何现有函数
- `strip_html` 辅助函数在每个使用它的函数内部定义（避免作用域问题）

## 验收命令

```bash
cd /Users/zhangpeicheng/jijin

# 测试权益基金（国金量化）
python -c "
from backend.fetcher import fetch_fund_holdings, fetch_fund_industry
holdings = fetch_fund_holdings('006195', save=False)
print('持仓条数:', len(holdings))
if holdings:
    print('第一条:', holdings[0])
    total = sum(h['weight'] for h in holdings if h['weight'])
    print('weight 合计:', total, '（应 <= 100）')
    assert total <= 100, 'weight 超出 100%'
print('T2-holdings PASS')
"

# 测试债券基金（易方达中债）应跳过
python -c "
from backend.fetcher import fetch_fund_holdings
result = fetch_fund_holdings('007171', save=False)
assert result == [], f'债基应返回空列表，实际返回: {result}'
print('T2-bond-skip PASS')
"
```

期望：持仓条数 > 0，weight 合计 ≤ 100，债基返回 `[]`
