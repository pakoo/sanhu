-- 板块资金流落库（产品稿 docs/product-specs/sector-fund-flow.html · Q4）
--
-- 为什么要落库 —— 这是一次**有意的架构破例**：
-- internal/market/board.go 开头写着「不落库：行情是易失数据，没有历史图表需求，
-- 缓存放进程内存」。资金流恰恰有历史图表需求，所以在这里破这个例。
-- board.go 那段注释已同步补充说明，免得下一个人以为这是违规实现。
--
-- 为什么非存不可：东财的历史日线**只给 120 个交易日**（约半年），lmt=0 也突破不了。
-- 不从今天开始攒，一年后依然只有半年。这是不可逆的 —— 今天不存，历史就永远补不回来。
--
-- 金额单位一律**元**（不是万元、不是亿元）。换算放前端做，后端存原值不丢精度。

-- ── 日线：全量板块，每个交易日一行 ──────────────────────────────────────────
-- 体量：行业 496 + 概念 504 ≈ 1000 行/交易日 ≈ 25 万行/年 ≈ 25 MB/年。
CREATE TABLE IF NOT EXISTS sector_fund_flow_daily (
    date           TEXT NOT NULL,              -- 2026-08-14
    bk_code        TEXT NOT NULL,              -- BK1215
    kind           TEXT NOT NULL,              -- industry / concept
    name           TEXT NOT NULL DEFAULT '',   -- 存当时的名字：东财改名不影响历史行的可读性

    -- 五档净额。恒等式：main_net = xl_net + lg_net（实测精确成立）。
    -- 注意四档不会相加为 0，有未分类成交（集合竞价/中性盘），这是正常的。
    main_net       REAL NOT NULL DEFAULT 0,    -- 主力 = 超大单 + 大单
    main_pct       REAL NOT NULL DEFAULT 0,    -- 主力净占比 %
    xl_net         REAL NOT NULL DEFAULT 0,    -- 超大单 ≥100万
    xl_pct         REAL NOT NULL DEFAULT 0,
    lg_net         REAL NOT NULL DEFAULT 0,    -- 大单 20–100万
    lg_pct         REAL NOT NULL DEFAULT 0,
    md_net         REAL NOT NULL DEFAULT 0,    -- 中单 4–20万
    md_pct         REAL NOT NULL DEFAULT 0,
    sm_net         REAL NOT NULL DEFAULT 0,    -- 小单 <4万
    sm_pct         REAL NOT NULL DEFAULT 0,

    close_point    REAL NOT NULL DEFAULT 0,    -- 板块点位（不是价格，别当股价用）
    change_pct     REAL NOT NULL DEFAULT 0,

    -- 板块内主力净流入最大的个股。除了展示，还是**层级去重的主要判据**：
    -- 父子板块（通信/通信设备/通信线缆）往往共享同一只龙头股。
    top_stock      TEXT NOT NULL DEFAULT '',
    top_stock_code TEXT NOT NULL DEFAULT '',

    PRIMARY KEY (date, bk_code)
);

-- 查某板块的历史曲线：按代码取一段日期
CREATE INDEX IF NOT EXISTS idx_sff_daily_code ON sector_fund_flow_daily(bk_code, date DESC);
-- 查某天的排行榜：按日期取、按主力净额排序
CREATE INDEX IF NOT EXISTS idx_sff_daily_rank ON sector_fund_flow_daily(date, kind, main_net DESC);

-- ── 分时：只存当日榜单 Top20，不是全量 ──────────────────────────────────────
-- 全量存不起：1000 板块 × 240 点 × 250 日 ≈ 6000 万行/年 ≈ 2.4 GB/年。
-- 只留每天最热的 20 个：20 × 240 × 250 ≈ 120 万行/年 ≈ 50 MB/年。
--
-- 为什么必须存而不是现取：东财的 klt=1 接口**只返回当天**，收盘后就查不到
-- 昨天的分钟曲线了。不存下来，「回看上周三资金什么时候进的」就永远做不了。
--
-- main_net 等是**当日累计值**，不是每分钟增量 —— 画出来是累计曲线。
CREATE TABLE IF NOT EXISTS sector_fund_flow_minute (
    date     TEXT NOT NULL,               -- 2026-08-14
    bk_code  TEXT NOT NULL,
    time     TEXT NOT NULL,               -- 09:31（只存时分，日期在 date 列）
    main_net REAL NOT NULL DEFAULT 0,
    xl_net   REAL NOT NULL DEFAULT 0,
    lg_net   REAL NOT NULL DEFAULT 0,
    md_net   REAL NOT NULL DEFAULT 0,
    sm_net   REAL NOT NULL DEFAULT 0,
    PRIMARY KEY (date, bk_code, time)
);

CREATE INDEX IF NOT EXISTS idx_sff_minute_code ON sector_fund_flow_minute(bk_code, date DESC);
