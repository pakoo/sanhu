"""Pydantic 数据模型"""
from typing import Dict, List, Optional
from pydantic import BaseModel


class FundInfo(BaseModel):
    code: str
    name: str
    fund_type: str = ""
    category: str = ""
    risk_level: str = ""
    manager: str = ""


class HoldingDetail(BaseModel):
    code: str
    name: str
    category: str
    shares: float
    cost_amount: float
    current_nav: float = 0.0
    current_value: float = 0.0
    profit: float = 0.0
    profit_rate: float = 0.0
    daily_return: float = 0.0
    daily_profit: float = 0.0


class PortfolioSummary(BaseModel):
    total_value: float
    total_cost: float
    total_profit: float
    total_profit_rate: float
    daily_profit: float
    holdings: List[HoldingDetail]
    allocation: Dict[str, float]
    allocation_amount: Dict[str, float]


class TransactionRequest(BaseModel):
    code: str
    type: str
    date: str
    amount: float
    nav: Optional[float] = None
    fee: float = 0.0
    notes: str = ""


class TargetAllocationRequest(BaseModel):
    allocations: Dict[str, float]


class DCABacktestRequest(BaseModel):
    code: str
    amount: float
    frequency: str = "monthly"
    start_date: str = ""
    end_date: str = ""


class TakeProfitBacktestRequest(BaseModel):
    code: str
    amount: float
    frequency: str = "monthly"
    take_profit_pct: float = 15.0
    stop_loss_pct: Optional[float] = None
    start_date: str = ""
    end_date: str = ""


class PortfolioBacktestRequest(BaseModel):
    allocations: Dict[str, float]
    total_amount: float
    rebalance_freq: str = "quarterly"
    start_date: str = ""
    end_date: str = ""


class NavPoint(BaseModel):
    date: str
    nav: float
    acc_nav: Optional[float] = None
    daily_return: Optional[float] = None


class RiskAlert(BaseModel):
    level: str
    category: str
    message: str
    detail: str = ""


class RebalanceSuggestion(BaseModel):
    category: str
    current_pct: float
    target_pct: float
    delta_pct: float
    action: str
    amount: float
    fund_suggestions: List[str] = []


class ImportHoldingRow(BaseModel):
    code: str
    shares: float
    cost_amount: float
    buy_date: str = ""


class ImportTransactionRow(BaseModel):
    code: str
    tx_type: str
    date: str
    amount: float
    nav: float
    fee: float = 0.0


class ImportCommitRequest(BaseModel):
    import_type: str  # "holdings" or "transactions"
    rows: list


class WatchlistAddRequest(BaseModel):
    code: str


class DecisionCreateRequest(BaseModel):
    code: str
    decision_type: str
    target_amount: Optional[float] = None
    target_nav_max: Optional[float] = None
    target_tp_pct: Optional[float] = None
    target_sl_pct: Optional[float] = None
    source_session_id: Optional[str] = None
    notes: Optional[str] = None
    is_virtual: bool = False


class LinkTransactionRequest(BaseModel):
    transaction_id: int


class SimulationCreateRequest(BaseModel):
    name: str
    strategy_name: str
    params: Dict = {}
    fund_pool: List[str] = []
    initial_capital: float
    start_date: str
    end_date: Optional[str] = None
    mode: str = "backtest"  # "backtest" or "forward"
    notes: Optional[str] = None
