"""初始化持仓数据 - 从支付宝截图提取的数据录入数据库"""
import sys
import os
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from datetime import datetime
from config import INITIAL_HOLDINGS, DEFAULT_TARGET_ALLOCATION
from backend.database import init_db, get_connection
from backend.fetcher import fetch_fund_detail, fetch_nav_history
import time


def seed():
    """录入初始数据"""
    print("初始化数据库...")
    init_db()

    conn = get_connection()

    # 清空已有数据（注意外键顺序）
    conn.execute("DELETE FROM nav_history")
    conn.execute("DELETE FROM transactions")
    conn.execute("DELETE FROM holdings")
    conn.execute("DELETE FROM funds")
    conn.execute("DELETE FROM target_allocation")

    # 录入基金元数据和持仓
    for h in INITIAL_HOLDINGS:
        # 插入基金元数据
        conn.execute(
            """INSERT OR REPLACE INTO funds (code, name, fund_type, category, risk_level, updated_at)
               VALUES (?, ?, ?, ?, ?, ?)""",
            (
                h["code"],
                h["name"],
                h["fund_type"],
                h["category"],
                "low" if h["category"] == "bond" else ("high" if h["category"] in ("equity", "qdii") else "medium"),
                datetime.now().isoformat(),
            ),
        )

        # 根据金额和收益率反算份额
        # cost = amount / (1 + profit_rate) => cost_amount
        # 用当前金额和成本来推算
        cost_amount = h["cost"]
        current_amount = h["amount"]

        # 插入持仓 (份额暂时用金额代替，后续通过净值修正)
        conn.execute(
            """INSERT INTO holdings (code, shares, cost_amount, buy_date, notes)
               VALUES (?, ?, ?, ?, ?)""",
            (
                h["code"],
                current_amount,  # 暂时用当前金额作为"份额"，会在获取净值后修正
                cost_amount,
                "2024-01-01",  # 近似买入日期
                f"初始录入，当前金额{current_amount}，收益{h['profit']:.2f}",
            ),
        )

    # 录入目标配置
    for category, alloc in DEFAULT_TARGET_ALLOCATION.items():
        conn.execute(
            "INSERT OR REPLACE INTO target_allocation (category, target_pct, min_pct, max_pct) VALUES (?, ?, ?, ?)",
            (category, alloc["target_pct"], alloc["min_pct"], alloc["max_pct"]),
        )

    conn.commit()
    conn.close()
    print(f"已录入 {len(INITIAL_HOLDINGS)} 只基金的持仓数据")

    # 抓取每只基金的详情和净值
    print("\n开始抓取基金详情和历史净值...")
    for h in INITIAL_HOLDINGS:
        code = h["code"]
        print(f"  抓取 {code} {h['name']}...")

        try:
            detail = fetch_fund_detail(code)
            if detail:
                print(f"    详情: {detail.get('name', 'N/A')}, 经理: {detail.get('manager', 'N/A')}")
            time.sleep(1)

            nav_data = fetch_nav_history(code)
            print(f"    净值数据: {len(nav_data)} 条记录")
            time.sleep(1)

            # 用最新净值修正持仓份额
            if nav_data:
                latest_nav = nav_data[0]["nav"]  # 数据按日期倒序
                shares = h["amount"] / latest_nav
                conn = get_connection()
                conn.execute(
                    "UPDATE holdings SET shares=? WHERE code=?",
                    (shares, code),
                )
                conn.commit()
                conn.close()
                print(f"    最新净值: {latest_nav}, 修正份额: {shares:.2f}")

        except Exception as e:
            print(f"    抓取失败: {e}")

    print("\n数据初始化完成!")


if __name__ == "__main__":
    seed()
