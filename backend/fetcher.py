"""Eastmoney 数据抓取模块"""
from __future__ import annotations
import re
import json
import time
import requests
from datetime import datetime, timedelta
from config import (
    EASTMONEY_NAV_URL,
    EASTMONEY_REALTIME_URL,
    EASTMONEY_DETAIL_URL,
    EASTMONEY_SEARCH_URL,
    HEADERS,
    REQUEST_DELAY,
    FUND_CATEGORY_MAP,
    CATEGORY_RISK,
)
from backend.database import get_connection


def fetch_nav_history(code: str, start_date: str = None, end_date: str = None, save: bool = True) -> list[dict]:
    """获取基金历史净值数据（增量）

    Args:
        code: 基金代码
        start_date: 开始日期 YYYY-MM-DD，默认1年前
        end_date: 结束日期 YYYY-MM-DD，默认今天
        save: 是否存入数据库

    Returns:
        [{"date": "2024-01-01", "nav": 1.0, "acc_nav": 1.1, "daily_return": 0.5}, ...]
    """
    if not end_date:
        end_date = datetime.now().strftime("%Y-%m-%d")
    if not start_date:
        start_date = (datetime.now() - timedelta(days=365 * 3)).strftime("%Y-%m-%d")

    all_data = []
    page = 1
    page_size = 20

    while True:
        params = {
            "callback": "",
            "fundCode": code,
            "pageIndex": page,
            "pageSize": page_size,
            "startDate": start_date,
            "endDate": end_date,
            "_": int(time.time() * 1000),
        }

        try:
            resp = requests.get(EASTMONEY_NAV_URL, params=params, headers=HEADERS, timeout=10)
            text = resp.text.strip()

            # 去掉JSONP包裹
            if text.startswith("("):
                text = text[1:-1]

            data = json.loads(text)
            records = data.get("Data", {}).get("LSJZList", [])

            if not records:
                break

            for r in records:
                try:
                    nav_val = float(r["DWJZ"]) if r.get("DWJZ") else None
                    acc_val = float(r["LJJZ"]) if r.get("LJJZ") else None
                    ret_val = float(r["JZZZL"]) if r.get("JZZZL") else None
                except (ValueError, TypeError):
                    continue

                if nav_val is None:
                    continue

                all_data.append({
                    "date": r["FSRQ"],
                    "nav": nav_val,
                    "acc_nav": acc_val,
                    "daily_return": ret_val,
                })

            total_count = data.get("Data", {}).get("TotalCount")
            if total_count and page * page_size >= total_count:
                break
            # If TotalCount is None, continue fetching until no more records
            if not total_count and len(records) < page_size:
                break

            page += 1
            time.sleep(REQUEST_DELAY)

        except Exception as e:
            print(f"获取 {code} 净值数据失败 (page {page}): {e}")
            break

    if save and all_data:
        conn = get_connection()
        conn.executemany(
            "INSERT OR IGNORE INTO nav_history (code, date, nav, acc_nav, daily_return) VALUES (?, ?, ?, ?, ?)",
            [(code, d["date"], d["nav"], d["acc_nav"], d["daily_return"]) for d in all_data],
        )
        conn.commit()
        conn.close()

    return all_data


def fetch_realtime_estimate(code: str) -> dict | None:
    """获取基金实时估值（盘中）

    Returns:
        {"code": "007171", "name": "...", "estimated_nav": 1.05, "change_pct": 0.12, "estimate_time": "2024-01-01 15:00"}
    """
    url = EASTMONEY_REALTIME_URL.format(code=code)
    try:
        resp = requests.get(url, headers=HEADERS, timeout=10)
        text = resp.text.strip()

        # JSONP格式: jsonpgz({...});
        match = re.search(r"jsonpgz\((.+)\)", text)
        if not match:
            return None

        data = json.loads(match.group(1))
        return {
            "code": data.get("fundcode", code),
            "name": data.get("name", ""),
            "estimated_nav": float(data.get("gsz", 0)),
            "change_pct": float(data.get("gszzl", 0)),
            "estimate_time": data.get("gztime", ""),
        }
    except Exception as e:
        print(f"获取 {code} 实时估值失败: {e}")
        return None


def _parse_jbgk_page(code: str) -> dict:
    """抓取 eastmoney 基金档案 jbgk 页面，提取档案信息与投资范围

    jbgk 页面同时包含两类信息：
    - 顶部 <table class="info"> 键值表：基金公司 / 成立日期 / 净资产规模 / 基金经理…
    - 下方 <div class="box"><h4><label>XXX</label></h4>…<p>YYY</p> 段落：投资目标 / 投资范围 / 投资策略 / 分红政策 / 风险收益特征
    """
    out: dict = {}
    try:
        url = f"https://fundf10.eastmoney.com/jbgk_{code}.html"
        resp = requests.get(url, headers=HEADERS, timeout=12)
        text = resp.text
    except Exception as e:
        print(f"[jbgk] {code} 请求失败: {e}")
        return out

    # ---- 顶部 info 表 ----
    kv: dict[str, str] = {}
    tbl_match = re.search(r'<table[^>]*class="info[^"]*"[^>]*>(.*?)</table>', text, re.DOTALL)
    if tbl_match:
        tbl = tbl_match.group(1)
        rows = re.findall(r'<tr[^>]*>(.*?)</tr>', tbl, re.DOTALL)
        for row in rows:
            # 以 <th> 作为切分点，每个 th 与其后的文本（直到下一 th）作为一对 kv
            # 可容忍 eastmoney 偶发的未闭合 td
            ths = list(re.finditer(r'<th[^>]*>(.*?)</th>', row, re.DOTALL))
            for i, th in enumerate(ths):
                label = re.sub(r'<[^>]+>', '', th.group(1)).strip().rstrip(':：')
                start = th.end()
                end = ths[i + 1].start() if i + 1 < len(ths) else len(row)
                value = re.sub(r'<[^>]+>', '', row[start:end]).strip()
                if label:
                    kv[label] = value

    # 基金公司
    if kv.get("基金管理人"):
        out["company"] = kv["基金管理人"]

    # 成立日期 — "成立日期/规模" 形如 "2019年07月08日 / 32.878亿份"
    raw_inception = kv.get("成立日期/规模") or kv.get("成立日期", "")
    if raw_inception:
        m = re.search(r"(\d{4})年(\d{1,2})月(\d{1,2})日", raw_inception)
        if m:
            out["inception_date"] = f"{m.group(1)}-{int(m.group(2)):02d}-{int(m.group(3)):02d}"

    # 基金规模（亿元） — 从 "净资产规模" 如 "16.02亿元（截止至：2025年12月31日）"
    raw_aum = kv.get("净资产规模", "")
    if raw_aum:
        m = re.search(r"([\d.]+)\s*亿元", raw_aum)
        if m:
            try:
                out["aum"] = float(m.group(1))
            except ValueError:
                pass

    # ---- 投资目标 / 范围 / 策略 段落 ----
    # 模式：<label class="left">XXX</label> ... <p...>VALUE</p>
    scope_keys = ["投资目标", "投资范围", "投资策略"]
    sections: list[str] = []
    for match in re.finditer(
        r'<label\s+class="left">([^<]+)</label>.*?<p[^>]*>(.*?)</p>',
        text,
        re.DOTALL,
    ):
        label = match.group(1).strip()
        if label not in scope_keys:
            continue
        value = re.sub(r'<[^>]+>', '', match.group(2))
        # 去除空白行但保留中文内容换行
        value = re.sub(r'[ \t\r]+', '', value).strip()
        if value and value != "暂无数据":
            sections.append(f"【{label}】\n{value}")

    if sections:
        out["scope"] = "\n\n".join(sections)

    return out


def fetch_fund_detail(code: str, save: bool = True) -> dict | None:
    """获取基金详情（通过 pingzhongdata JS 解析）

    Returns:
        {"code": "007171", "name": "...", "fund_type": "...", "manager": "...",
         "returns": {"1m": 0.5, "6m": 2.0, ...}, "asset_allocation": {...}, ...}
    """
    url = EASTMONEY_DETAIL_URL.format(code=code)
    try:
        resp = requests.get(url, headers=HEADERS, timeout=15)
        text = resp.text

        result = {"code": code}

        # 解析基金名称
        name_match = re.search(r'var\s+fS_name\s*=\s*"(.+?)"', text)
        if name_match:
            result["name"] = name_match.group(1)

        # 解析基金代码
        code_match = re.search(r'var\s+fS_code\s*=\s*"(.+?)"', text)
        if code_match:
            result["code"] = code_match.group(1)

        # 解析收益率
        for key, var_name in [
            ("return_1m", "syl_1y"),
            ("return_6m", "syl_6y"),
            ("return_1y", "syl_1n"),
            ("return_3y", "syl_3n"),
        ]:
            match = re.search(rf'var\s+{var_name}\s*=\s*"(.+?)"', text)
            if match:
                try:
                    result[key] = float(match.group(1))
                except ValueError:
                    pass

        # 解析基金经理
        manager_match = re.search(r'var\s+Data_currentFundManager\s*=\s*(\[.+?\]);', text, re.DOTALL)
        if manager_match:
            try:
                managers = json.loads(manager_match.group(1))
                if managers:
                    result["manager"] = managers[0].get("name", "")
            except (json.JSONDecodeError, IndexError):
                pass

        # 解析资产配置
        allocation_match = re.search(r'var\s+Data_assetAllocation\s*=\s*(\{.+?\});', text, re.DOTALL)
        if allocation_match:
            try:
                alloc = json.loads(allocation_match.group(1))
                # 取最新一期
                categories = alloc.get("categories", [])
                series = alloc.get("series", [])
                if categories and series:
                    latest_idx = -1  # 最新一期
                    asset_alloc = {}
                    for s in series:
                        name = s.get("name", "")
                        data = s.get("data", [])
                        if data and len(data) > abs(latest_idx):
                            asset_alloc[name] = data[latest_idx]
                    result["asset_allocation"] = asset_alloc
            except (json.JSONDecodeError, IndexError):
                pass

        # 解析业绩评价
        perf_match = re.search(r'var\s+Data_performanceEvaluation\s*=\s*(\{.+?\});', text, re.DOTALL)
        if perf_match:
            try:
                result["performance_evaluation"] = json.loads(perf_match.group(1))
            except json.JSONDecodeError:
                pass

        # 基金规模 AUM —— 从 pingzhongdata 的 Data_fluctuationScale 取最新一期
        scale_match = re.search(
            r'Data_fluctuationScale\s*=\s*\{.*?"series"\s*:\s*(\[.+?\])',
            text, re.DOTALL,
        )
        if scale_match:
            try:
                series = json.loads(scale_match.group(1))
                if series:
                    y = series[-1].get("y")
                    if y is not None:
                        result["aum"] = float(y)
            except (json.JSONDecodeError, ValueError, TypeError):
                pass

        # 从 jbgk 基金档案页补充公司 / 成立日期 / 投资范围文字
        jbgk = _parse_jbgk_page(code)
        for k in ("company", "inception_date", "scope"):
            if jbgk.get(k):
                result[k] = jbgk[k]
        # AUM 若 pingzhongdata 没有，fallback 到 jbgk 的净资产规模
        if "aum" not in result and jbgk.get("aum") is not None:
            result["aum"] = jbgk["aum"]

        # 保存基金元数据到数据库
        if save and result.get("name"):
            fund_type = ""
            type_match = re.search(r'var\s+fS_type\s*=\s*"(.+?)"', text)
            if type_match:
                fund_type = type_match.group(1)

            conn = get_connection()

            # 如果数据库已有该基金且有分类，保留原有分类
            existing = conn.execute("SELECT category, fund_type FROM funds WHERE code=?", (result["code"],)).fetchone()
            if existing and existing["category"] and fund_type == "":
                category = existing["category"]
                fund_type = existing["fund_type"] or ""
            else:
                category = FUND_CATEGORY_MAP.get(fund_type, "mixed")

            risk_level = CATEGORY_RISK.get(category, "medium")

            conn.execute(
                """INSERT OR REPLACE INTO funds
                   (code, name, fund_type, category, risk_level, manager,
                    company, scope, inception_date, aum, updated_at)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                (
                    result["code"],
                    result.get("name", ""),
                    fund_type,
                    category,
                    risk_level,
                    result.get("manager", ""),
                    result.get("company", ""),
                    result.get("scope", ""),
                    result.get("inception_date", ""),
                    result.get("aum"),
                    datetime.now().isoformat(),
                ),
            )
            conn.commit()
            conn.close()

        return result

    except Exception as e:
        print(f"获取 {code} 基金详情失败: {e}")
        return None


def search_fund(keyword: str) -> list[dict]:
    """搜索基金

    Returns:
        [{"code": "007171", "name": "...", "type": "...", "nav": 1.05}, ...]
    """
    params = {
        "callback": "",
        "m": 1,
        "key": keyword,
        "_": int(time.time() * 1000),
    }
    try:
        resp = requests.get(EASTMONEY_SEARCH_URL, params=params, headers=HEADERS, timeout=10)
        text = resp.text.strip()

        # 去JSONP包裹
        if text.startswith("("):
            text = text[1:-1]

        data = json.loads(text)
        results = []
        for item in data.get("Datas", []):
            results.append({
                "code": item.get("CODE", ""),
                "name": item.get("NAME", ""),
                "type": item.get("FundBaseInfo", {}).get("FTYPE", ""),
                "nav": item.get("FundBaseInfo", {}).get("DWJZ", ""),
            })
        return results
    except Exception as e:
        print(f"搜索基金 '{keyword}' 失败: {e}")
        return []


def refresh_all_funds() -> dict:
    """刷新所有持仓基金的净值和详情数据

    Returns:
        {"refreshed": ["007171", ...], "errors": ["006195", ...]}
    """
    conn = get_connection()
    funds = conn.execute("SELECT code FROM funds").fetchall()
    conn.close()

    if not funds:
        # 如果 funds 表为空，从 holdings 表获取
        conn = get_connection()
        codes = [r["code"] for r in conn.execute("SELECT DISTINCT code FROM holdings").fetchall()]
        watchlist_codes = [r["code"] for r in conn.execute("SELECT DISTINCT code FROM watchlist").fetchall()]
        codes = list(set(codes + watchlist_codes))
        conn.close()
        funds = [{"code": code} for code in codes]

    refreshed = []
    errors = []

    for fund in funds:
        code = fund["code"]
        try:
            # 获取详情
            fetch_fund_detail(code)
            time.sleep(REQUEST_DELAY)

            # 获取最新净值
            fetch_nav_history(code)
            time.sleep(REQUEST_DELAY)

            refreshed.append(code)
        except Exception as e:
            print(f"刷新 {code} 失败: {e}")
            errors.append(code)

    return {"refreshed": refreshed, "errors": errors}


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

        # 响应包含多个季度，只取第一期（最新季度）
        sections = re.split(r"<div class='boxitem", text)
        first_section = sections[1] if len(sections) > 1 else text

        # 从「截止至：<font ...>YYYY-MM-DD</font>」提取日期
        date_match = re.search(r'截止至：<font[^>]*>(\d{4}-\d{2}-\d{2})</font>', first_section)
        report_date = date_match.group(1) if date_match else ""

        # 提取持仓数据：解析 HTML table（仅第一期）
        rows = re.findall(r'<tr[^>]*>(.*?)</tr>', first_section, re.DOTALL)
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
                "industry": None,
                "report_date": report_date,
            })

        # 为每只股票查申万行业（f127 字段）
        for item in results:
            sc = item["stock_code"]
            # secid 规则：6开头沪市用"1."，其余用"0."
            market = "1" if sc.startswith("6") else "0"
            try:
                r = requests.get(
                    f"https://push2.eastmoney.com/api/qt/stock/get",
                    params={"fltt": 2, "invt": 2, "fields": "f127", "secid": f"{market}.{sc}"},
                    headers={**HEADERS, "Referer": "https://quote.eastmoney.com/"},
                    timeout=5,
                )
                industry = r.json().get("data", {}).get("f127")
                if industry:
                    item["industry"] = industry
                time.sleep(0.2)
            except Exception:
                pass

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


def fetch_fund_industry(code: str, save: bool = True) -> list[dict]:
    """从 fund_holdings 的行业字段聚合行业分布（hycc 接口已失效，改用持仓股行业推导）

    债券基金（category='bond'）直接返回 []

    Returns:
        [{"industry": "半导体", "weight": 15.2, "report_date": "2025-12-31"}, ...]
    """
    conn = get_connection()
    row = conn.execute("SELECT category FROM funds WHERE code=?", (code,)).fetchone()
    if row and row["category"] == "bond":
        conn.close()
        return []

    # 取该基金最新一期持仓，按行业聚合 weight
    latest = conn.execute(
        "SELECT MAX(report_date) as d FROM fund_holdings WHERE code=?", (code,)
    ).fetchone()
    if not latest or not latest["d"]:
        conn.close()
        return []

    report_date = latest["d"]
    holdings = conn.execute(
        "SELECT industry, weight FROM fund_holdings WHERE code=? AND report_date=? AND industry IS NOT NULL",
        (code, report_date)
    ).fetchall()
    conn.close()

    if not holdings:
        return []

    # 按行业聚合 weight（申万二级行业 → 直接用原名）
    industry_map: dict = {}
    for h in holdings:
        ind = h["industry"]
        w = h["weight"] or 0
        industry_map[ind] = industry_map.get(ind, 0) + w

    results = [
        {"industry": ind, "weight": round(w, 2), "report_date": report_date}
        for ind, w in sorted(industry_map.items(), key=lambda x: x[1], reverse=True)
    ]

    if save and results:
        conn = get_connection()
        conn.executemany(
            """INSERT OR REPLACE INTO fund_industry
               (code, industry, weight, report_date)
               VALUES (?, ?, ?, ?)""",
            [(code, r["industry"], r["weight"], r["report_date"]) for r in results]
        )
        conn.commit()
        conn.close()

    return results


def refresh_holdings_for_all_funds(log_cb=None) -> dict:
    """批量刷新所有权益类基金的持仓和行业数据

    Args:
        log_cb: 可选回调 log_cb(msg: str)，每步进度推送给调用方

    Returns:
        {"refreshed": [...], "skipped": [...], "errors": [...]}
    """
    def _log(msg: str):
        print(msg)
        if log_cb:
            log_cb(msg)

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
            _log(f"— {code} 跳过（债券型）")
            continue

        _log(f"⏳ {code} 抓取中...")
        try:
            fetch_fund_holdings(code)
            time.sleep(REQUEST_DELAY)
            fetch_fund_industry(code)
            time.sleep(REQUEST_DELAY)
            refreshed.append(code)
            _log(f"✓ {code} 完成")
        except Exception as e:
            print(f"刷新 {code} 持仓失败: {e}")
            errors.append(code)
            _log(f"✗ {code} 失败：{e}")

    _log(f"完成 {len(refreshed)} 只 / 跳过 {len(skipped)} 只债券 / 失败 {len(errors)} 只")
    return {"refreshed": refreshed, "skipped": skipped, "errors": errors}
