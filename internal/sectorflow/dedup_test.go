package sectorflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pakoo/sanhu/internal/fetcher"
)

// 用真实夹具跑：内容是 2026-08-14 行业榜前 8 名，正是「一个热点刷屏整张榜」的典型样本。
func realRows(t *testing.T) []fetcher.SectorFlow {
	t.Helper()
	p := filepath.Join("..", "fetcher", "testdata", "fflow_rank_industry_20260814.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读夹具: %v", err)
	}
	var raw struct {
		Data struct {
			Diff []struct {
				F12, F14, F204, F205 string
				F62, F3              float64
			} `json:"diff"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("解析夹具: %v", err)
	}
	out := make([]fetcher.SectorFlow, 0, len(raw.Data.Diff))
	for _, d := range raw.Data.Diff {
		out = append(out, fetcher.SectorFlow{
			Code: d.F12, Name: d.F14, Kind: "industry",
			MainNet: d.F62, ChangePct: d.F3,
			TopStock: d.F204, TopStockCode: d.F205,
		})
	}
	return out
}

func TestDedupCollapsesTelecomFamily(t *testing.T) {
	rows := realRows(t)
	if len(rows) != 8 {
		t.Fatalf("夹具应有 8 条，得到 %d", len(rows))
	}

	got := Dedup(rows)

	// 8 条里 6 条是通信系、2 条是有色系，应当收敛成 2 组
	if len(got) != 2 {
		names := []string{}
		for _, g := range got {
			names = append(names, g.Name)
		}
		t.Fatalf("期望收敛成 2 组（通信系 + 有色系），得到 %d 组: %v", len(got), names)
	}

	top := got[0]
	if top.Name != "通信" {
		t.Errorf("组代表应是净额最大的「通信」，得到 %q", top.Name)
	}
	if len(top.Merged) != 5 {
		t.Errorf("通信组应折叠 5 个细分板块，得到 %d", len(top.Merged))
	}
	// 代表值必须是父板块自己的净额，**不能是组内求和** —— 求和就是重复计算，
	// 正是去重要消灭的东西。
	if top.MainNet != 13906735360 {
		t.Errorf("代表净额应为通信自身的 13906735360（不是组内求和），得到 %.0f", top.MainNet)
	}
	// 通信系 5 个细分全部以「通信」开头，理由必须说名称同源 ——
	// 若说成「龙头股相同：亨通光电」就是错的（通信服务的龙头是数据港）。
	if top.Reason != "名称同源：都以「通信」开头" {
		t.Errorf("通信组的理由应为名称同源，得到 %q", top.Reason)
	}

	second := got[1]
	if second.Name != "稀土" || len(second.Merged) != 1 {
		t.Errorf("第二组应是稀土 + 折叠小金属，得到 %q 折叠 %d 个", second.Name, len(second.Merged))
	}
	// 稀土与小金属名称不同源，是靠龙头股信号并到一起的，理由必须如实说明
	if second.Reason != "龙头股相同：中国稀土" {
		t.Errorf("稀土组的合并理由应指向龙头股信号，得到 %q", second.Reason)
	}
}

func TestDedupKeepsUnrelatedSectorsApart(t *testing.T) {
	rows := []fetcher.SectorFlow{
		{Code: "BK1", Name: "银行", MainNet: 50, TopStock: "招商银行", TopStockCode: "600036"},
		{Code: "BK2", Name: "白酒", MainNet: 40, TopStock: "贵州茅台", TopStockCode: "600519"},
		{Code: "BK3", Name: "光伏设备", MainNet: -30, TopStock: "隆基绿能", TopStockCode: "601012"},
	}
	got := Dedup(rows)
	if len(got) != 3 {
		t.Fatalf("三个无关板块不该被合并，得到 %d 组", len(got))
	}
	for _, g := range got {
		if len(g.Merged) != 0 {
			t.Errorf("%s 不该折叠任何板块", g.Name)
		}
	}
}

func TestDedupIgnoresEmptyTopStock(t *testing.T) {
	// 龙头股字段缺失时不能当同源依据，否则所有缺数据的板块会被并成一坨。
	rows := []fetcher.SectorFlow{
		{Code: "BK1", Name: "板块甲", MainNet: 10},
		{Code: "BK2", Name: "板块乙", MainNet: 20},
		{Code: "BK3", Name: "板块丙", MainNet: 30},
	}
	got := Dedup(rows)
	if len(got) != 3 {
		t.Fatalf("龙头股为空的板块不该互相合并，得到 %d 组", len(got))
	}
}

func TestDedupSortsByMainNetDesc(t *testing.T) {
	rows := []fetcher.SectorFlow{
		{Code: "A", Name: "甲", MainNet: -100, TopStockCode: "1"},
		{Code: "B", Name: "乙", MainNet: 300, TopStockCode: "2"},
		{Code: "C", Name: "丙", MainNet: 50, TopStockCode: "3"},
	}
	got := Dedup(rows)
	for i := 1; i < len(got); i++ {
		if got[i-1].MainNet < got[i].MainNet {
			t.Fatalf("结果必须按主力净额降序，位置 %d 破坏了顺序", i)
		}
	}
}

func TestTopNSplitsInflowAndOutflow(t *testing.T) {
	rows := Dedup([]fetcher.SectorFlow{
		{Code: "A", Name: "甲", MainNet: 300, TopStockCode: "1"},
		{Code: "B", Name: "乙", MainNet: 100, TopStockCode: "2"},
		{Code: "C", Name: "丙", MainNet: -50, TopStockCode: "3"},
		{Code: "D", Name: "丁", MainNet: -900, TopStockCode: "4"},
	})
	in, out := TopN(rows, 10)

	if len(in) != 2 || in[0].Name != "甲" {
		t.Errorf("流入侧应为 [甲 乙]，得到 %d 条，首位 %v", len(in), in)
	}
	// 流出侧最负的排最前，前端左右两栏才都是「越靠上越极端」
	if len(out) != 2 || out[0].Name != "丁" {
		t.Errorf("流出侧应以最负的丁打头，得到 %v", out)
	}
}

func TestTopNRespectsLimit(t *testing.T) {
	var rows []fetcher.SectorFlow
	for i := 0; i < 30; i++ {
		rows = append(rows, fetcher.SectorFlow{
			Code: string(rune('A' + i)), Name: string(rune('A' + i)),
			MainNet: float64(100 - i*10), TopStockCode: string(rune('a' + i)),
		})
	}
	in, out := TopN(Dedup(rows), 5)
	if len(in) != 5 || len(out) != 5 {
		t.Errorf("两侧都应被截到 5 条，得到 流入%d / 流出%d", len(in), len(out))
	}
}
