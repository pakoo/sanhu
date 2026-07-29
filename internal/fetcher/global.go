package fetcher

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// 全球市场行情抓取（市场板块 docs/product-specs/market.html）。
//
// 两个数据源，都是公开免费接口：
//   - 新浪 hq.sinajs.cn：商品/汇率/美元指数/全球股指/美股/A股/VIX，支持一次批量拿
//   - OKX 公共行情：加密货币（新浪的加密接口已下线，实测返回空）
//
// 设计前提：免费接口一定会坏（本项目已被 fundgz 下线、hycc 失效、push2 限流各坑过一次）。
// 所以这里每个源独立返回、独立失败，上层按组降级，不会因为一个源挂了整页报错。

// Quote 一条行情。ChangePct 统一为百分数（+1.23 表示涨 1.23%）。
type Quote struct {
	Key       string  `json:"key"`        // 内部标识，如 gold / dji / nvda
	Name      string  `json:"name"`       // 显示名
	Price     float64 `json:"price"`      // 最新价
	Change    float64 `json:"change"`     // 涨跌额
	ChangePct float64 `json:"change_pct"` // 涨跌幅 %
	Note      string  `json:"note"`       // 补充说明，如「已收盘」
	Time      string  `json:"time"`       // 数据源给的时间
}

// sinaGBKDecode 新浪返回的是 GBK 编码，直接当 UTF-8 读会拿到乱码的中文名
func sinaGBKDecode(raw []byte) string {
	out, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), raw)
	if err != nil {
		return string(raw) // 解码失败就按原样返回，至少数字部分还能用
	}
	return string(out)
}

// FetchSinaQuotes 一次批量拉取新浪行情。
//
// codes 是新浪的原始代码（hf_GC / gb_$dji / fx_susdcny / sz300308 …），
// 返回 map[原始代码]Quote；解析不出来的条目直接跳过，不影响其他条目。
func FetchSinaQuotes(codes []string) (map[string]Quote, error) {
	if len(codes) == 0 {
		return map[string]Quote{}, nil
	}
	url := "https://hq.sinajs.cn/list=" + strings.Join(codes, ",")
	body, err := getWithReferer(url, "https://finance.sina.com.cn")
	if err != nil {
		return nil, fmt.Errorf("fetch sina quotes: %w", err)
	}

	text := sinaGBKDecode(body)
	out := make(map[string]Quote, len(codes))
	for _, line := range strings.Split(text, "\n") {
		code, payload, ok := parseSinaLine(line)
		if !ok {
			continue
		}
		q, ok := parseSinaQuote(code, payload)
		if !ok {
			continue
		}
		out[code] = q
	}
	return out, nil
}

// parseSinaLine 从 `var hq_str_hf_GC="a,b,c";` 里取出代码和内容
func parseSinaLine(line string) (code, payload string, ok bool) {
	line = strings.TrimSpace(line)
	const prefix = "var hq_str_"
	if !strings.HasPrefix(line, prefix) {
		return "", "", false
	}
	rest := line[len(prefix):]
	eq := strings.Index(rest, "=")
	if eq < 0 {
		return "", "", false
	}
	code = rest[:eq]
	payload = strings.Trim(strings.TrimSuffix(strings.TrimSpace(rest[eq+1:]), ";"), `"`)
	if payload == "" {
		return "", "", false // 接口对不认识的代码返回空串
	}
	return code, payload, true
}

// parseSinaQuote 按代码前缀分派 —— 新浪各品种的字段布局完全不同，
// 下面每种的下标都是拿真实响应数过的（2026-07-29），不是照文档抄的。
func parseSinaQuote(code, payload string) (Quote, bool) {
	f := strings.Split(payload, ",")
	num := func(i int) float64 {
		if i < 0 || i >= len(f) {
			return 0
		}
		v, _ := strconv.ParseFloat(strings.TrimSpace(f[i]), 64)
		return v
	}
	at := func(i int) string {
		if i < 0 || i >= len(f) {
			return ""
		}
		return strings.TrimSpace(f[i])
	}

	q := Quote{Key: code}

	switch {
	// 外盘期货：黄金/原油/白银/铜
	// [0]当前 [2]买 [3]卖 [4]最高 [5]最低 [6]时间 [7]昨收 [8]开盘 [12]日期 [13]名称
	case strings.HasPrefix(code, "hf_"):
		if len(f) < 14 {
			return q, false
		}
		q.Name, q.Price, q.Time = at(13), num(0), at(6)
		prev := num(7)
		if prev > 0 {
			q.Change = q.Price - prev
			q.ChangePct = q.Change / prev * 100
		}

	// 美元指数 DINIW
	// [0]时间 [1]当前 [2]当前的重复 [3]昨收 [4]成交量 [5]开盘 [6]最高 [7]最低 [9]名称
	// 注意昨收是 [3] 不是 [2]：[2] 与 [1] 恒等，用它算出来的涨跌永远是 0.00%
	// （与东财 UDI 的 -0.07% 对照确认过）
	case code == "DINIW":
		if len(f) < 10 {
			return q, false
		}
		q.Name, q.Price, q.Time = at(9), num(1), at(0)
		prev := num(3)
		if prev > 0 {
			q.Change = q.Price - prev
			q.ChangePct = q.Change / prev * 100
		}

	// 外汇 fx_s*
	// [0]时间 [1]当前 [9]名称 [10]涨跌幅% [11]涨跌额
	// 直接用接口给的涨跌幅：汇率小数位多，自己用昨收反推会有明显舍入误差
	case strings.HasPrefix(code, "fx_s"):
		if len(f) < 12 {
			return q, false
		}
		q.Name, q.Price, q.Time = at(9), num(1), at(0)
		q.ChangePct, q.Change = num(10), num(11)

	// 美股 gb_*：接口直接给涨跌幅，不用自己算
	// [0]名称 [1]当前 [2]涨跌幅% [3]时间 [4]涨跌额
	case strings.HasPrefix(code, "gb_"):
		if len(f) < 5 {
			return q, false
		}
		q.Name, q.Price, q.ChangePct, q.Time, q.Change = at(0), num(1), num(2), at(3), num(4)

	// 国际指数 int_* 与 VIX znb_*
	// [0]名称 [1]当前 [2]涨跌额 [3]涨跌幅%
	case strings.HasPrefix(code, "int_"), strings.HasPrefix(code, "znb_"):
		if len(f) < 4 {
			return q, false
		}
		q.Name, q.Price, q.Change, q.ChangePct = at(0), num(1), num(2), num(3)

	// A股 sh/sz/bj
	// [0]名称 [1]开盘 [2]昨收 [3]当前 [4]最高 [5]最低 … [30]日期 [31]时间
	case strings.HasPrefix(code, "sh"), strings.HasPrefix(code, "sz"), strings.HasPrefix(code, "bj"):
		if len(f) < 32 {
			return q, false
		}
		q.Name, q.Price = at(0), num(3)
		prev := num(2)
		// 停牌或集合竞价前当前价为 0，此时用昨收占位，避免显示成 0 元
		if q.Price == 0 {
			q.Price = prev
		}
		if prev > 0 {
			q.Change = q.Price - prev
			q.ChangePct = q.Change / prev * 100
		}
		q.Time = at(30) + " " + at(31)

	default:
		return q, false
	}

	if q.Name == "" || q.Price == 0 {
		return q, false
	}
	return q, true
}

// okxTickerResp OKX 公共行情返回体
type okxTickerResp struct {
	Code string `json:"code"`
	Data []struct {
		InstID string `json:"instId"`
		Last   string `json:"last"`
		Open24 string `json:"open24h"`
	} `json:"data"`
}

// FetchOKXTickers 拉取加密货币行情。
// 新浪的加密接口（btc_btcbitstamp 等）实测已返回空，改用 OKX 公共行情，无需鉴权。
func FetchOKXTickers(instIDs []string) (map[string]Quote, error) {
	out := make(map[string]Quote, len(instIDs))
	var firstErr error
	for _, id := range instIDs {
		url := fmt.Sprintf("https://www.okx.com/api/v5/market/ticker?instId=%s", id)
		body, err := get(url)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		var resp okxTickerResp
		if err := json.Unmarshal(body, &resp); err != nil || resp.Code != "0" || len(resp.Data) == 0 {
			if firstErr == nil {
				firstErr = fmt.Errorf("okx %s: 响应无法解析", id)
			}
			continue
		}
		d := resp.Data[0]
		last, _ := strconv.ParseFloat(d.Last, 64)
		open, _ := strconv.ParseFloat(d.Open24, 64)
		q := Quote{Key: id, Name: cryptoName(id), Price: last, Time: time.Now().Format("15:04:05")}
		if open > 0 {
			q.Change = last - open
			q.ChangePct = q.Change / open * 100
			q.Note = "24h"
		}
		out[id] = q
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func cryptoName(instID string) string {
	switch {
	case strings.HasPrefix(instID, "BTC"):
		return "比特币"
	case strings.HasPrefix(instID, "ETH"):
		return "以太坊"
	default:
		return instID
	}
}
