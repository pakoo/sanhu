"""基金投资决策助手 - 配置"""
import os

# 项目路径
BASE_DIR = os.path.dirname(os.path.abspath(__file__))
DATA_DIR = os.path.join(BASE_DIR, "data")
DB_PATH = os.path.join(DATA_DIR, "jijin.db")

# Eastmoney API
EASTMONEY_NAV_URL = "https://api.fund.eastmoney.com/f10/lsjz"
EASTMONEY_REALTIME_URL = "http://fundgz.1234567.com.cn/js/{code}.js"
EASTMONEY_DETAIL_URL = "https://fund.eastmoney.com/pingzhongdata/{code}.js"
EASTMONEY_SEARCH_URL = "https://fundsuggest.eastmoney.com/FundSearch/api/FundSearchAPI.ashx"

HEADERS = {
    "Referer": "https://fundf10.eastmoney.com/",
    "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
}

# 请求间隔(秒)
REQUEST_DELAY = 1.0

# 基金类别映射
FUND_CATEGORY_MAP = {
    "指数型-固收": "bond",
    "债券型-混合一级": "bond",
    "债券型-混合二级": "bond",
    "债券型-长债": "bond",
    "债券型-中短债": "bond",
    "纯债型": "bond",
    "股票型": "equity",
    "指数型-股票": "equity",
    "混合型-偏股": "mixed",
    "混合型-平衡": "mixed",
    "混合型-偏债": "mixed",
    "混合型-灵活": "mixed",
    "指数型-海外股票": "qdii",
    "QDII": "qdii",
}

# 风险等级映射
CATEGORY_RISK = {
    "bond": "low",
    "equity": "high",
    "mixed": "medium",
    "qdii": "high",
}

# 初始持仓数据 (从支付宝截图提取)
INITIAL_HOLDINGS = [
    {
        "code": "007171",
        "name": "易方达中债3-5年国开行债券指数A",
        "fund_type": "指数型-固收",
        "category": "bond",
        "amount": 200294.84,
        "cost": 194270.41,  # amount - profit
        "profit": 6024.43,
        "profit_rate": 0.0301,
    },
    {
        "code": "014248",
        "name": "兴业一年持有期债券A",
        "fund_type": "债券型-混合一级",
        "category": "bond",
        "amount": 153194.02,
        "cost": 150000.00,
        "profit": 3194.02,
        "profit_rate": 0.0213,
    },
    {
        "code": "006195",
        "name": "国金量化多因子股票A",
        "fund_type": "股票型",
        "category": "equity",
        "amount": 20905.01,
        "cost": 21000.00,
        "profit": -94.99,
        "profit_rate": -0.0045,
    },
    {
        "code": "021095",
        "name": "东方低碳经济混合C",
        "fund_type": "混合型-偏股",
        "category": "mixed",
        "amount": 14831.97,
        "cost": 16000.00,
        "profit": -1168.03,
        "profit_rate": -0.0898,
    },
    {
        "code": "024203",
        "name": "永赢制造升级智选混合C",
        "fund_type": "混合型-偏股",
        "category": "mixed",
        "amount": 11602.37,
        "cost": 12000.00,
        "profit": -397.63,
        "profit_rate": -0.0331,
    },
    {
        "code": "018957",
        "name": "中航机遇领航混合C",
        "fund_type": "混合型-偏股",
        "category": "mixed",
        "amount": 6351.48,
        "cost": 5000.00,
        "profit": 1351.48,
        "profit_rate": 0.2703,
    },
    {
        "code": "019172",
        "name": "摩根纳斯达克100指数(QDII)A",
        "fund_type": "指数型-海外股票",
        "category": "qdii",
        "amount": 5803.26,
        "cost": 5000.00,
        "profit": 803.26,
        "profit_rate": 0.1607,
    },
    {
        "code": "006533",
        "name": "易方达科融混合",
        "fund_type": "混合型-偏股",
        "category": "mixed",
        "amount": 2132.55,
        "cost": 2000.00,
        "profit": 132.55,
        "profit_rate": 0.0663,
    },
]

# 默认目标配置
DEFAULT_TARGET_ALLOCATION = {
    "bond": {"target_pct": 60.0, "min_pct": 50.0, "max_pct": 70.0},
    "equity": {"target_pct": 20.0, "min_pct": 10.0, "max_pct": 30.0},
    "mixed": {"target_pct": 10.0, "min_pct": 5.0, "max_pct": 20.0},
    "qdii": {"target_pct": 10.0, "min_pct": 5.0, "max_pct": 15.0},
}
