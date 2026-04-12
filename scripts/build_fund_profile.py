from __future__ import annotations

import json
import re
import sqlite3
from datetime import datetime
from typing import Optional

try:
    import akshare as ak
except ModuleNotFoundError:
    ak = None

from config import DB_PATH
from backend.database import init_db


THEME_RULES = [
    ("新能源", re.compile(r"新能源|光伏|风电|锂电|碳中和|清洁能源|绿色能源|电池|储能")),
    ("科技", re.compile(r"科技|信息|互联网|半导体|芯片|数字|人工智能|AI|创新|电子|软件", re.IGNORECASE)),
    ("消费", re.compile(r"消费|白酒|食品|饮料|日常|零售|品牌")),
    ("医药", re.compile(r"医药|医疗|生物|健康|制药|医美|基因")),
    ("军工", re.compile(r"军工|国防|航空|航天|装备")),
    ("红利", re.compile(r"红利|分红|高股息|价值|稳健")),
    ("出海", re.compile(r"出海|全球|国际|跨境")),
]

REGION_RULES = [
    ("港股", re.compile(r"港股|香港|恒生|H股", re.IGNORECASE)),
    ("美股", re.compile(r"美股|纳指|纳斯达克|标普|美国|北美")),
    ("全球", re.compile(r"QDII|海外|跨境|全球|国际", re.IGNORECASE)),
]

BROAD_INDEX_PATTERN = re.compile(r"沪深300|中证500|中证800|中证1000|中证A500|上证50")
LARGE_VALUE_PATTERN = re.compile(r"价值|红利|央企|国企|银行|保险")
MID_PATTERN = re.compile(r"中盘|中小盘")
SMALL_PATTERN = re.compile(r"小盘|小微|创业板|科创板")

CATEGORY_RULES = [
    ("股票型", "equity"),
    ("混合型", "mixed"),
    ("债券型", "bond"),
    ("QDII", "qdii"),
    ("指数型", "index"),
]

SKIP_CATEGORY_KEYWORDS = ("货币型", "FOF")

PROFILE_SQL = """
INSERT OR REPLACE INTO fund_profile (
    code,
    name,
    category,
    sub_style,
    theme_tags,
    region_tag,
    aum,
    inception_date,
    manager,
    manager_since,
    fee_rate,
    top10_concentration,
    top_industry,
    top_industry_weight,
    profile_source,
    updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
"""


def pick_column(columns: list[str], *candidates: str) -> Optional[str]:
    for candidate in candidates:
        if candidate in columns:
            return candidate
    return None


def infer_category(raw_value: object) -> Optional[str]:
    text = str(raw_value or "").strip()
    if not text:
        return None
    if any(keyword in text for keyword in SKIP_CATEGORY_KEYWORDS):
        return None
    for keyword, mapped in CATEGORY_RULES:
        if keyword in text:
            return mapped
    return None


def infer_theme_tags(name: str) -> list[str]:
    tags = [tag for tag, pattern in THEME_RULES if pattern.search(name)]
    return tags


def infer_region_tag(name: str) -> str:
    for tag, pattern in REGION_RULES:
        if pattern.search(name):
            return tag
    return "A股"


def infer_sub_style(name: str, category: str, theme_tags: list[str]) -> Optional[str]:
    if BROAD_INDEX_PATTERN.search(name):
        return "broad_index"
    if category in {"equity", "mixed"} and LARGE_VALUE_PATTERN.search(name):
        return "large_value"
    if MID_PATTERN.search(name):
        return "mid"
    if SMALL_PATTERN.search(name):
        return "small"
    if theme_tags:
        return f"theme_{theme_tags[0]}"
    if category in {"equity", "mixed"}:
        return "large_growth"
    return None


def build_offline_market_funds() -> list[dict]:
    prefixes = [f"离线样本{i:03d}" for i in range(1, 121)]
    templates = [
        ("股票型", "新能源先锋股票A"),
        ("股票型", "科技创新股票A"),
        ("股票型", "消费升级股票A"),
        ("股票型", "医疗健康股票A"),
        ("股票型", "军工装备股票A"),
        ("股票型", "红利价值股票A"),
        ("股票型", "中盘成长股票A"),
        ("股票型", "小盘创新股票A"),
        ("股票型", "创业板科技股票A"),
        ("股票型", "科创板人工智能股票A"),
        ("混合型", "新能源机遇混合A"),
        ("混合型", "科技成长混合A"),
        ("混合型", "消费精选混合A"),
        ("混合型", "医疗健康混合A"),
        ("混合型", "军工领航混合A"),
        ("混合型", "价值红利混合A"),
        ("混合型", "中小盘优势混合A"),
        ("混合型", "港股科技混合A"),
        ("债券型", "中短债债券A"),
        ("债券型", "纯债债券A"),
        ("债券型", "信用债债券A"),
        ("债券型", "利率债债券A"),
        ("债券型", "稳健增利债券A"),
        ("债券型", "双债增强债券A"),
        ("QDII", "纳斯达克100(QDII)A"),
        ("QDII", "标普500(QDII)A"),
        ("QDII", "恒生科技(QDII)A"),
        ("QDII", "全球出海(QDII)A"),
        ("QDII", "美国科技(QDII)A"),
        ("QDII", "全球消费(QDII)A"),
        ("指数型", "沪深300指数A"),
        ("指数型", "中证500指数A"),
        ("指数型", "中证800指数A"),
        ("指数型", "中证1000指数A"),
        ("指数型", "中证A500指数A"),
        ("指数型", "上证50指数A"),
        ("指数型", "港股红利指数A"),
        ("指数型", "科创板创新指数A"),
    ]

    rows = []
    for prefix in prefixes:
        for fund_type, suffix in templates:
            rows.append(
                {
                    "code": f"{900000 + len(rows):06d}",
                    "name": f"{prefix}{suffix}",
                    "fund_type": fund_type,
                }
            )
    print(f"Using offline fallback market list with {len(rows)} synthetic rows")
    return rows


def load_market_funds() -> list[dict]:
    if ak is None:
        print("akshare is not installed; falling back to offline market list")
        return build_offline_market_funds()

    try:
        df = ak.fund_name_em()
    except Exception as exc:
        print(f"akshare.fund_name_em() failed: {exc}")
        return build_offline_market_funds()

    columns = [str(col) for col in df.columns]
    code_col = pick_column(columns, "基金代码", "基金编码", "代码", "基金代码/基金简称")
    name_col = pick_column(columns, "基金简称", "基金名称", "名称")
    type_col = pick_column(columns, "基金类型", "类型")

    if not (code_col and name_col and type_col):
        print("Unexpected fund_name_em schema; columns:")
        print(columns)

        inferred_code_col = code_col
        inferred_name_col = name_col
        inferred_type_col = type_col
        for col in columns:
            if inferred_code_col is None and ("代码" in col or col.lower() == "code"):
                inferred_code_col = col
            if inferred_name_col is None and ("简称" in col or "名称" in col or col.lower() == "name"):
                inferred_name_col = col
            if inferred_type_col is None and ("类型" in col or col.lower() == "type"):
                inferred_type_col = col

        code_col = inferred_code_col
        name_col = inferred_name_col
        type_col = inferred_type_col

    if not (code_col and name_col and type_col):
        print("Unable to map required columns from akshare.fund_name_em(); falling back to offline market list")
        return build_offline_market_funds()

    rows = []
    for record in df.to_dict(orient="records"):
        rows.append(
            {
                "code": str(record.get(code_col, "")).strip(),
                "name": str(record.get(name_col, "")).strip(),
                "fund_type": str(record.get(type_col, "")).strip(),
            }
        )
    return rows


def build_profiles() -> int:
    init_db()
    market_rows = load_market_funds()
    total_rows = len(market_rows)
    inserted = 0

    conn = sqlite3.connect(DB_PATH)
    try:
        for idx, row_dict in enumerate(market_rows, start=1):
            code = str(row_dict.get("code", "")).strip()
            name = str(row_dict.get("name", "")).strip()
            category = infer_category(row_dict.get("fund_type"))

            if not code or not name or category is None:
                if idx % 500 == 0:
                    print(f"Processed {idx}/{total_rows}, inserted {inserted}")
                continue

            theme_tags = infer_theme_tags(name)
            sub_style = infer_sub_style(name, category, theme_tags)
            region_tag = infer_region_tag(name)
            updated_at = datetime.now().isoformat()

            conn.execute(
                PROFILE_SQL,
                (
                    code,
                    name,
                    category,
                    sub_style,
                    json.dumps(theme_tags, ensure_ascii=False),
                    region_tag,
                    None,
                    None,
                    None,
                    None,
                    None,
                    None,
                    None,
                    None,
                    "inferred",
                    updated_at,
                ),
            )
            inserted += 1

            if idx % 500 == 0:
                print(f"Processed {idx}/{total_rows}, inserted {inserted}")

        conn.commit()
    finally:
        conn.close()

    print(f"Done. total rows={total_rows}, inserted={inserted}")
    return inserted


if __name__ == "__main__":
    build_profiles()
