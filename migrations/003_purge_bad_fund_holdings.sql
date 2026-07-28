-- 清掉早期解析器写坏的持仓行。
--
-- 那版解析器按写死的列下标取值，而东财的真实表头是
--   序号 | 股票代码 | 股票名称 | 最新价 | 涨跌幅 | 相关资讯 | 占净值比例 | 持股数 | 持仓市值
-- 结果 stock_name 落成了股票代码，weight 取到了「持仓市值（万元）」——
-- 带千分位逗号解析失败就是 0，侥幸解析成功的会出现「单只股票占净值 876%」。
--
-- 两个特征都能机械识别，删掉即可；下次抓取会用修好的解析器重新写入
-- （写入已由 INSERT OR IGNORE 改为 INSERT OR REPLACE，不会再被旧行挡住）。
DELETE FROM fund_holdings WHERE stock_name = stock_code;
DELETE FROM fund_holdings WHERE weight > 100;
