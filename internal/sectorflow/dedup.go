// Package sectorflow 组装板块资金流：层级去重、落库、内存缓存与降级。
//
// 抓取在 internal/fetcher/fundflow.go，那里有口径警告和限流纪律，改动前先读。
package sectorflow

import (
	"sort"
	"strings"

	"github.com/pakoo/sanhu/internal/fetcher"
)

// Group 去重后的一行榜单。
//
// MainNet 等取的是**代表板块**的值，不是组内求和 —— 求和会把父子板块的钱
// 算两遍，那正是去重要解决的问题。
type Group struct {
	fetcher.SectorFlow
	// Merged 被折叠进来的同源板块，按主力净额降序。前端点开可展开，
	// 不做静默丢弃：用户有权知道榜单替他合并了什么。
	Merged []fetcher.SectorFlow `json:"merged,omitempty"`
	// Reason 为什么判定为同源，直接显示给用户，让合并可解释、可质疑。
	Reason string `json:"reason,omitempty"`
}

// Dedup 折叠同源板块。
//
// ── 要解决的问题 ────────────────────────────────────────────────────────────
// 东财把父子层级混在同一个榜单里。2026-08-14 实测，行业榜前七名有六个是通信系：
//
//	通信 139.1亿 / 通信设备 118.7亿 / 通信线缆及配套 55.0亿 /
//	通信网络设备及器件 48.0亿 / 通信服务 20.3亿 / 通信终端及配件 15.8亿
//
// 同一批钱被反复计入父板块和各级子板块，结果是**一个热点就能刷屏整张榜**，
// 「今天哪些板块被买」这个问题反而看不出答案。
//
// ── 判据 ────────────────────────────────────────────────────────────────────
// 东财这个接口不返回父子关系，所以只能从数据本身推断。用两个信号：
//
//  1. **主力净流入最大股相同**。父子板块往往共享同一只龙头股 ——
//     实测「通信 / 通信设备 / 通信线缆及配套」都是亨通光电，
//     「稀土 / 小金属」都是中国稀土。这个信号是数据驱动的，不需要硬编码行业树。
//  2. **名称同源**（一个名字是另一个的前缀，且前缀≥2 字）。
//     光靠信号 1 不够：通信服务的龙头是数据港、通信终端及配件是剑桥科技，
//     它们和「通信」不共享龙头股，但显然是同一主题的细分。
//
// 两个信号取并集，用并查集连通。宁可多合一点也不要漏 —— 合错了用户点开就看见了，
// 漏了则榜单继续被刷屏。
//
// 不硬编码申万行业树，是因为那份映射要人工维护、会过时，而且东财的板块划分
// 本来就和申万对不齐。
func Dedup(rows []fetcher.SectorFlow) []Group {
	n := len(rows)
	if n == 0 {
		return nil
	}

	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]] // 路径压缩
			x = parent[x]
		}
		return x
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}

	// 信号 1：同一只主力净流入最大股
	byTop := map[string][]int{}
	for i, r := range rows {
		if r.TopStockCode == "" {
			continue // 空值不能当同源依据，否则所有缺数据的板块会被并成一坨
		}
		byTop[r.TopStockCode] = append(byTop[r.TopStockCode], i)
	}
	for _, idxs := range byTop {
		for k := 1; k < len(idxs); k++ {
			union(idxs[0], idxs[k])
		}
	}

	// 信号 2：名称同源（短名是长名的前缀）
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	// 按名字长度升序，让短名（父级）先被检查
	sort.Slice(order, func(a, b int) bool {
		return len([]rune(rows[order[a]].Name)) < len([]rune(rows[order[b]].Name))
	})
	for oi := 0; oi < len(order); oi++ {
		i := order[oi]
		short := []rune(rows[i].Name)
		if len(short) < 2 {
			continue // 单字名做前缀太容易误伤
		}
		for oj := oi + 1; oj < len(order); oj++ {
			j := order[oj]
			if strings.HasPrefix(rows[j].Name, rows[i].Name) {
				union(i, j)
			}
		}
	}

	// 收拢成组
	members := map[int][]int{}
	for i := 0; i < n; i++ {
		r := find(i)
		members[r] = append(members[r], i)
	}

	out := make([]Group, 0, len(members))
	for _, idxs := range members {
		// 代表 = 组内 |主力净额| 最大的那个。用绝对值是因为流出侧同样要选出
		// 最有代表性的一个（净额 -96 亿比 -3 亿更能说明问题）。
		sort.Slice(idxs, func(a, b int) bool {
			return abs(rows[idxs[a]].MainNet) > abs(rows[idxs[b]].MainNet)
		})
		g := Group{SectorFlow: rows[idxs[0]]}
		if len(idxs) > 1 {
			for _, k := range idxs[1:] {
				g.Merged = append(g.Merged, rows[k])
			}
			g.Reason = explain(g.SectorFlow, g.Merged)
		}
		out = append(out, g)
	}

	// 最终按主力净额降序：前端首屏两头取，头部是流入榜、尾部反向是流出榜
	sort.Slice(out, func(a, b int) bool { return out[a].MainNet > out[b].MainNet })
	return out
}

// explain 事后按**实际成员**反推合并理由，而不是在 union 时记「先触发的那个信号」。
//
// 一组可能同时因为两个信号被并到一起（通信系就是：通信/通信设备/通信线缆共享
// 亨通光电，通信服务/通信终端则是名称同源）。union 时记录的理由只反映最先触发的
// 那一条，显示出来会以为「通信服务的龙头股是亨通光电」—— 是错的。
// 这段文案要显示给用户看，让他判断这次合并对不对，写错比不写更糟。
func explain(rep fetcher.SectorFlow, merged []fetcher.SectorFlow) string {
	allPrefix, anyTop := true, false
	for _, m := range merged {
		if !strings.HasPrefix(m.Name, rep.Name) {
			allPrefix = false
		}
		if m.TopStockCode != "" && m.TopStockCode == rep.TopStockCode {
			anyTop = true
		}
	}
	switch {
	case allPrefix:
		return "名称同源：都以「" + rep.Name + "」开头"
	case anyTop:
		return "龙头股相同：" + rep.TopStock
	default:
		return "同源板块：名称或龙头股重合"
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// TopN 取流入前 n 和流出前 n。
//
// rows 必须已按主力净额降序（Dedup 的返回值就是）。
// 流出侧按净额升序返回（最负的在前），这样前端左右两栏都是「越靠上越极端」。
func TopN(rows []Group, n int) (inflow, outflow []Group) {
	for _, r := range rows {
		if r.MainNet > 0 && len(inflow) < n {
			inflow = append(inflow, r)
		}
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].MainNet < 0 && len(outflow) < n {
			outflow = append(outflow, rows[i])
		}
	}
	return inflow, outflow
}
