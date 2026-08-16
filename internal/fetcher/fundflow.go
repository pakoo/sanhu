package fetcher

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 板块资金流抓取（产品稿 docs/product-specs/sector-fund-flow.html）。
//
// ── 口径警告，改这个文件前必读 ───────────────────────────────────────────────
// 「主力净流入」不是真实的资金进出。每笔成交都有买方和卖方，钱没有净进出。
// 它是把成交按**单笔金额**分档（东财口径：超大单≥100万、大单20–100万、
// 中单4–20万、小单<4万），再按主动买/主动卖方向推断出来的**统计指标**，
// 衡量的是「大额成交里主动买比主动卖多多少」。恒等式 主力 = 超大单 + 大单。
//
// 两条实测佐证（2026-08-14 通信板块 BK1215）：
//   - 122.23亿 + 16.84亿 = 139.07亿，与接口返回的主力净额差 0.0000 亿 → 分档是硬规则
//   - 四档相加 = +0.35亿 而不是 0 → 有一部分成交没被归入任何一档（集合竞价、
//     无法判定主动方向的中性盘）。这正是「它是推断而非流水账」的证据。
//
// 因此：**只能同源比较**。同一只亨通光电同一天，东财给 31.2 亿、新浪给 46.9 亿，
// 两家分档阈值不同，数字不可混用。前端必须有术语提示，别让用户当成真金白银。
//
// ── 限流纪律（产品稿红线 1）─────────────────────────────────────────────────
// 东财对 push2 有限流，项目里早有两处独立记录，是踩出来的：
//   - holdings_fetcher.go:109「刷 20 只基金就是 200 个请求，实测会被东财限流
//     （push2 直接返回空，且持续数分钟）」
//   - global.go:20「本项目已被 fundgz 下线、hycc 失效、push2 限流各坑过一次」
//
// 所以这个文件的三个抓取函数一律：
//   - 全量榜单一次请求拿全（pz=600），不分页、不并发
//   - 用 getNoRetry，**失败立即返回**，由上层降级到缓存
//   - 绝不原地重试。重试只会加重限流，下一个调度周期再试就行
//
// ── push2 会长时间整体不可用（归因别猜，直接切备源）───────────────────────
// 2026-08-16 实测：push2 / push2his 全线 000（TLS 握手后连接被掐、无状态码），
// 而同一时刻 fund.eastmoney.com、fundf10.eastmoney.com 照常 200。
// **持续 9 小时以上**，且换一台完全不同 IP 的机器同样不通 ——
// 所以既不是「按 IP 封禁」也不是地域限制，归因反复三轮都没坐实。
//
// 结论是操作性的而非解释性的：**别花时间猜为什么，直接切 push2delay**
// （见下面的主备主机定义）。降级链条（内存 → 数据库）也因此是必需品，
// 不是锦上添花 —— 缺口可能持续一整个下午。
//
// ── 排查陷阱：别用境外服务量出口 IP ─────────────────────────────────────────
// Clash 等工具默认**分流**：国内域名走直连（真实宽带 IP），境外域名走代理（节点 IP）。
// 拿 api.ipify.org 这类**境外**服务量到的是代理节点 IP，和东财看到的完全是两回事 ——
// 我据此误判过一轮「东财拒绝境外 IP」。要量就用国内回显服务：curl cip.cc
// 另注意 TUN 模式即使代理端口没监听也在接管流量（dig 返回 198.18.x.x 即 fake-ip），
// 此时 curl --noproxy 并不能真正直连。
const (
	// 东财行情接口的通用票据。这不是密钥，是公开页面里写死的常量。
	emUT = "b2884a393a59ad64002292a3e90d46a5"
	// push2 只认 quote/data 域的 Referer，用基金站的会拿到空响应（见 client.go 注释）
	emFlowReferer = "https://data.eastmoney.com/"
)

// SectorKind 板块类型。地域板块（t:1）对基金决策没用，故意不提供。
type SectorKind string

const (
	KindIndustry SectorKind = "industry" // 行业板块，实测 496 个
	KindConcept  SectorKind = "concept"  // 概念板块，实测 504 个
)

// fsOf 板块类型对应东财的 fs 筛选串
func fsOf(k SectorKind) (string, error) {
	switch k {
	case KindIndustry:
		return "m:90+t:2", nil
	case KindConcept:
		return "m:90+t:3", nil
	}
	return "", fmt.Errorf("未知板块类型 %q", k)
}

// SectorFlow 一个板块的资金流快照。金额单位统一为**元**，
// 换算成「亿」放到前端做 —— 后端存原值，避免早早丢精度。
type SectorFlow struct {
	Code      string  `json:"code"` // BK1215
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	Price     float64 `json:"price"`      // 板块点位
	ChangePct float64 `json:"change_pct"` // 涨跌幅 %

	MainNet float64 `json:"main_net"` // 主力净额 = 超大单 + 大单
	MainPct float64 `json:"main_pct"` // 主力净占比 %
	XLNet   float64 `json:"xl_net"`   // 超大单
	XLPct   float64 `json:"xl_pct"`
	LGNet   float64 `json:"lg_net"` // 大单
	LGPct   float64 `json:"lg_pct"`
	MDNet   float64 `json:"md_net"` // 中单
	MDPct   float64 `json:"md_pct"`
	SMNet   float64 `json:"sm_net"` // 小单
	SMPct   float64 `json:"sm_pct"`

	TopStock     string `json:"top_stock"`      // 板块内主力净流入最大的个股
	TopStockCode string `json:"top_stock_code"` //

	// DataDate 这批数据**自己**的交易日，从东财的 f124 时间戳换算而来，
	// 不是「今天」。两者会不一致：节假日和周末请求，东财返回的是上一个
	// 交易日的数据。用 time.Now() 当日期落库，会把周五的数字记到周六名下，
	// 之后所有历史图表都跟着错位，且很难发现。
	DataDate string `json:"data_date"`
}

// FlowPoint 分时资金流的一个点。**Net 是当日累计值，不是这一分钟的增量** ——
// 东财就是这么给的，画出来是一条累计曲线。搞错会把图画成锯齿状。
type FlowPoint struct {
	Time    string  `json:"time"` // "09:31"（日期在外层，这里只留时分）
	MainNet float64 `json:"main_net"`
	XLNet   float64 `json:"xl_net"`
	LGNet   float64 `json:"lg_net"`
	MDNet   float64 `json:"md_net"`
	SMNet   float64 `json:"sm_net"`
}

// FlowDay 历史日线的一天
type FlowDay struct {
	Date      string  `json:"date"` // 2026-08-14
	MainNet   float64 `json:"main_net"`
	MainPct   float64 `json:"main_pct"`
	XLNet     float64 `json:"xl_net"`
	LGNet     float64 `json:"lg_net"`
	MDNet     float64 `json:"md_net"`
	SMNet     float64 `json:"sm_net"`
	Close     float64 `json:"close"`
	ChangePct float64 `json:"change_pct"`
}

// ── 抓取 ────────────────────────────────────────────────────────────────────

// 主用主机与备用主机。
//
// push2 被限流后可能数小时不可用（见文件头），而 **push2delay 是同一套 API、
// 同样的字段、同样的数值**，只是行情延时。2026-08-16 实测：push2 全挂时
// push2delay 照常 200，返回的 496 个行业板块数据与 push2 逐字段一致。
//
// 延时对本功能几乎无影响：板块资金流是盘中每 30 分钟看一次的东西，
// 慢几分钟看不出来。所以主源挂了就自动切备源，而不是让用户看空白页。
const (
	hostQuote      = "push2.eastmoney.com"      // 实时
	hostQuoteDelay = "push2delay.eastmoney.com" // 延时，主源不可用时的备胎
	hostHistory    = "push2his.eastmoney.com"   // 历史日线，**没有可用备胎**
)

// delayPageLimit 延时主机的单页上限。
//
// push2 给 pz=600 能一次返回全部 496 个；push2delay **无视 pz、每页最多 100 条**。
// 但排行榜只关心两头，所以走备源时用 po=1（降序）拿流入前 100、
// po=0（升序）拿流出前 100，两个请求就够 —— 中间那段没人看。
const delayPageLimit = 100

// FetchSectorFlowRank 拿某一类板块的资金流排行，主源失败自动切延时备源。
//
// 正常路径：push2 一个请求拿全 496/504 个（pz=600，实测够用）。
// 降级路径：push2delay 两个请求（流入头 + 流出头），合计 200 个，覆盖榜单两端。
func FetchSectorFlowRank(kind SectorKind) ([]SectorFlow, error) {
	fs, err := fsOf(kind)
	if err != nil {
		return nil, err
	}

	// 主源：一个请求拿全
	rows, primaryErr := fetchRankPage(hostQuote, fs, kind, 1, 600)
	if primaryErr == nil && len(rows) > 0 {
		return rows, nil
	}

	// 备源：两头各取一页再合并
	head, errHead := fetchRankPage(hostQuoteDelay, fs, kind, 1, delayPageLimit)
	tail, errTail := fetchRankPage(hostQuoteDelay, fs, kind, 0, delayPageLimit)
	if errHead != nil && errTail != nil {
		// 两个源都不行，把主源的错误报出去 —— 那才是根因
		return nil, fmt.Errorf("抓板块资金流(%s): 实时源 %v；延时源 %v", kind, primaryErr, errHead)
	}
	merged := mergeByCode(head, tail)
	if len(merged) == 0 {
		return nil, fmt.Errorf("抓板块资金流(%s): 实时源与延时源都没有返回数据", kind)
	}
	return merged, nil
}

// fetchRankPage 取一页。po=1 按主力净额降序（流入在前），po=0 升序（流出在前）。
func fetchRankPage(host, fs string, kind SectorKind, po, pz int) ([]SectorFlow, error) {
	url := fmt.Sprintf(
		"https://%s/api/qt/clist/get"+
			"?fid=f62&po=%d&pz=%d&pn=1&np=1&fltt=2&invt=2&ut=%s&fs=%s"+
			"&fields=f12,f14,f2,f3,f62,f184,f66,f69,f72,f75,f78,f81,f84,f87,f204,f205,f124",
		host, po, pz, emUT, strings.ReplaceAll(fs, "+", "%2B"))

	body, err := getNoRetry(url, emFlowReferer)
	if err != nil {
		return nil, err
	}
	return parseSectorRank(body, kind)
}

// mergeByCode 合并两页并按板块代码去重（板块总数 <200 时两页会重叠）。
// 保持「先来的优先」，因为流入头那一页的顺序更接近最终展示顺序。
func mergeByCode(pages ...[]SectorFlow) []SectorFlow {
	seen := make(map[string]bool)
	var out []SectorFlow
	for _, p := range pages {
		for _, r := range p {
			if r.Code == "" || seen[r.Code] {
				continue
			}
			seen[r.Code] = true
			out = append(out, r)
		}
	}
	return out
}

// FetchSectorMinute 取某板块当日的分钟级累计资金流。
//
// 关键性质：**一次请求返回当日全部 240 个点**（09:31→15:00），
// 所以画分钟曲线根本不需要轮询。也正因为它只给「当日」，
// 想要历史分时就必须自己落库（产品稿 Q4）。
func FetchSectorMinute(bkCode string) ([]FlowPoint, error) {
	tpl := "https://%s/api/qt/stock/fflow/kline/get" +
		"?lmt=0&klt=1&secid=90.%s&ut=%s" +
		"&fields1=f1,f2,f3,f7&fields2=f51,f52,f53,f54,f55,f56"

	// 主源挂了切延时源。实测延时源同样返回完整的 240 个点，末点值与实时源一致。
	var firstErr error
	for _, host := range []string{hostQuote, hostQuoteDelay} {
		body, err := getNoRetry(fmt.Sprintf(tpl, host, bkCode, emUT), emFlowReferer)
		if err == nil {
			if pts, perr := parseFlowMinute(body); perr == nil {
				return pts, nil
			} else if firstErr == nil {
				firstErr = perr
			}
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, fmt.Errorf("抓板块分时(%s): %w", bkCode, firstErr)
}

// FetchSectorDaily 取某板块的历史日线资金流。
//
// 东财只给 120 个交易日（约半年），lmt=0 也拿不到更多 —— 这是硬上限，
// 不是参数没调对。想要更长历史只能自己一天天攒（产品稿红线 3）。
//
// **这个端点没有可用备胎**：2026-08-16 实测 push2delay 只返回 1 条、
// push2hisdelay 是 302 跳转、quote 与 hsmarketwg 都是 404。
// 所以 push2his 挂掉时历史图只能靠本地库 —— 这反过来又说明了落库的价值：
// 攒得越久，对这个接口的依赖越小。
func FetchSectorDaily(bkCode string) ([]FlowDay, error) {
	url := fmt.Sprintf(
		"https://"+hostHistory+"/api/qt/stock/fflow/daykline/get"+
			"?lmt=0&klt=101&secid=90.%s&ut=%s"+
			"&fields1=f1,f2,f3,f7&fields2=f51,f52,f53,f54,f55,f56,f57,f58,f59,f60,f61,f62,f63,f64,f65",
		bkCode, emUT)

	body, err := getNoRetry(url, emFlowReferer)
	if err != nil {
		return nil, fmt.Errorf("抓板块日线(%s): %w", bkCode, err)
	}
	return parseFlowDaily(body)
}

// ── 解析（与网络分离，方便用 testdata 夹具做离线单测）─────────────────────────

// emNum 东财的数字字段。正常是 number，但数据缺失时会给字符串 "-"，
// 直接 float64 反序列化会整条记录报错。这里统一吃掉，缺失当 0。
type emNum float64

func (n *emNum) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "-" || s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil // 解析不出来当 0，不让一个坏字段废掉整个榜单
	}
	*n = emNum(f)
	return nil
}

type rankResp struct {
	Data *struct {
		Total int `json:"total"`
		Diff  []struct {
			F12  string `json:"f12"`
			F14  string `json:"f14"`
			F2   emNum  `json:"f2"`
			F3   emNum  `json:"f3"`
			F62  emNum  `json:"f62"`
			F184 emNum  `json:"f184"`
			F66  emNum  `json:"f66"`
			F69  emNum  `json:"f69"`
			F72  emNum  `json:"f72"`
			F75  emNum  `json:"f75"`
			F78  emNum  `json:"f78"`
			F81  emNum  `json:"f81"`
			F84  emNum  `json:"f84"`
			F87  emNum  `json:"f87"`
			F204 string `json:"f204"`
			F205 string `json:"f205"`
			F124 emNum  `json:"f124"` // 行情时间戳（秒），用来判定这批数据属于哪个交易日
		} `json:"diff"`
	} `json:"data"`
}

func parseSectorRank(raw []byte, kind SectorKind) ([]SectorFlow, error) {
	var r rankResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("解析板块榜单: %w", err)
	}
	// data 为 null 是东财限流时的典型响应（HTTP 200 但没数据）。
	// 必须当错误抛，否则上层会把「被限流」当成「今天没有板块」写进库。
	if r.Data == nil || len(r.Data.Diff) == 0 {
		return nil, fmt.Errorf("板块榜单返回空 —— 多半是被限流了，别重试，等下个周期")
	}

	out := make([]SectorFlow, 0, len(r.Data.Diff))
	for _, d := range r.Data.Diff {
		if d.F12 == "" {
			continue
		}
		dataDate := ""
		if d.F124 > 0 {
			dataDate = time.Unix(int64(d.F124), 0).Format("2006-01-02")
		}
		out = append(out, SectorFlow{
			DataDate: dataDate,
			Code:     d.F12, Name: d.F14, Kind: string(kind),
			Price: float64(d.F2), ChangePct: float64(d.F3),
			MainNet: float64(d.F62), MainPct: float64(d.F184),
			XLNet: float64(d.F66), XLPct: float64(d.F69),
			LGNet: float64(d.F72), LGPct: float64(d.F75),
			MDNet: float64(d.F78), MDPct: float64(d.F81),
			SMNet: float64(d.F84), SMPct: float64(d.F87),
			TopStock: d.F204, TopStockCode: d.F205,
		})
	}
	return out, nil
}

type klineResp struct {
	Data *struct {
		Code   string   `json:"code"`
		Name   string   `json:"name"`
		Klines []string `json:"klines"`
	} `json:"data"`
}

// klines 是逗号分隔的字符串数组，字段顺序由请求里的 fields2 决定。
// 分时(f51–f56) 与日线(f51–f65) 共用前 6 位：
//
//	[0]时间 [1]主力净 [2]小单净 [3]中单净 [4]大单净 [5]超大单净
//
// 注意 2/3/4/5 的顺序是「小 中 大 超大」，不是从大到小 —— 写反了不会报错，
// 只会让图上的四档张冠李戴，很难发现。已用实测数据校验：主力 = 超大 + 大。
func splitKlines(raw []byte) ([]string, *klineResp, error) {
	var r klineResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, nil, fmt.Errorf("解析资金流 K 线: %w", err)
	}
	if r.Data == nil || len(r.Data.Klines) == 0 {
		return nil, nil, fmt.Errorf("资金流 K 线返回空 —— 可能被限流，或该板块代码不存在")
	}
	return r.Data.Klines, &r, nil
}

func atof(ss []string, i int) float64 {
	if i >= len(ss) {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(ss[i]), 64)
	if err != nil {
		return 0
	}
	return f
}

func parseFlowMinute(raw []byte) ([]FlowPoint, error) {
	lines, _, err := splitKlines(raw)
	if err != nil {
		return nil, err
	}
	out := make([]FlowPoint, 0, len(lines))
	for _, ln := range lines {
		p := strings.Split(ln, ",")
		if len(p) < 6 {
			continue
		}
		// "2026-08-14 09:31" → "09:31"。日期对分时图没用，且前端画轴时更短更省事。
		t := p[0]
		if i := strings.IndexByte(t, ' '); i >= 0 {
			t = t[i+1:]
		}
		out = append(out, FlowPoint{
			Time:    t,
			MainNet: atof(p, 1),
			SMNet:   atof(p, 2),
			MDNet:   atof(p, 3),
			LGNet:   atof(p, 4),
			XLNet:   atof(p, 5),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("分时数据一条都没解析出来")
	}
	return out, nil
}

func parseFlowDaily(raw []byte) ([]FlowDay, error) {
	lines, _, err := splitKlines(raw)
	if err != nil {
		return nil, err
	}
	out := make([]FlowDay, 0, len(lines))
	for _, ln := range lines {
		p := strings.Split(ln, ",")
		if len(p) < 13 {
			continue
		}
		out = append(out, FlowDay{
			Date:    p[0],
			MainNet: atof(p, 1),
			SMNet:   atof(p, 2),
			MDNet:   atof(p, 3),
			LGNet:   atof(p, 4),
			XLNet:   atof(p, 5),
			MainPct: atof(p, 6),
			// 7/8/9/10 分别是小/中/大/超大的占比，暂时用不上，先不占字段
			Close:     atof(p, 11),
			ChangePct: atof(p, 12),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("日线数据一条都没解析出来")
	}
	return out, nil
}
