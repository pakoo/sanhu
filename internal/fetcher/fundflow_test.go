package fetcher

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// 夹具是 2026-08-15 从东财真实抓下来的响应，不是手编的。
// 特别说明分时那份：klt=1 接口**只返回当天**，8-14 那 240 个点过期就再也拿不回来，
// 所以它是不可复现的样本，别删。
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读夹具 %s: %v", name, err)
	}
	return b
}

func TestParseSectorRank(t *testing.T) {
	rows, err := parseSectorRank(fixture(t, "fflow_rank_industry_20260814.json"), KindIndustry)
	if err != nil {
		t.Fatalf("parseSectorRank: %v", err)
	}
	if len(rows) != 8 {
		t.Fatalf("期望 8 条（夹具是截断保存的真实响应），得到 %d", len(rows))
	}

	top := rows[0]
	if top.Code != "BK1215" || top.Name != "通信" {
		t.Errorf("榜首应是 BK1215 通信，得到 %s %s", top.Code, top.Name)
	}
	if top.Kind != "industry" {
		t.Errorf("Kind 应为 industry，得到 %q", top.Kind)
	}
	if math.Abs(top.MainNet-13906735360) > 1 {
		t.Errorf("主力净额应为 13906735360，得到 %.0f", top.MainNet)
	}
	if math.Abs(top.ChangePct-2.06) > 1e-9 {
		t.Errorf("涨跌幅应为 2.06，得到 %v", top.ChangePct)
	}
	if top.TopStock != "亨通光电" || top.TopStockCode != "600487" {
		t.Errorf("主力流入最大股应为 亨通光电/600487，得到 %s/%s", top.TopStock, top.TopStockCode)
	}

	// 四档字段最容易张冠李戴（顺序是 小/中/大/超大，不是从大到小）。
	// 用恒等式 主力 = 超大单 + 大单 兜住：映射错位这条一定会崩。
	for _, r := range rows {
		if d := math.Abs(r.MainNet - r.XLNet - r.LGNet); d > 1 {
			t.Errorf("%s(%s) 主力 ≠ 超大单 + 大单，差 %.0f 元 —— 八成是四档字段映射错位",
				r.Name, r.Code, d)
		}
	}
}

func TestParseSectorRankRejectsThrottledResponse(t *testing.T) {
	// 被限流时东财给的是 HTTP 200 + data:null。这必须当错误抛出去，
	// 否则上层会把「被限流」当成「今天没有板块」写进库，图表变成一片空白还查不出原因。
	for _, body := range []string{
		`{"rc":0,"rt":6,"data":null}`,
		`{"rc":0,"rt":6,"data":{"total":0,"diff":[]}}`,
	} {
		if _, err := parseSectorRank([]byte(body), KindIndustry); err == nil {
			t.Errorf("空响应 %s 应当报错，却被当成了合法的空榜单", body)
		}
	}
}

func TestParseFlowMinute(t *testing.T) {
	pts, err := parseFlowMinute(fixture(t, "fflow_minute_BK1215_20260814.json"))
	if err != nil {
		t.Fatalf("parseFlowMinute: %v", err)
	}
	if len(pts) != 240 {
		t.Fatalf("A 股一个交易日 240 分钟，期望 240 个点，得到 %d", len(pts))
	}
	if pts[0].Time != "09:31" || pts[len(pts)-1].Time != "15:00" {
		t.Errorf("时间应从 09:31 到 15:00，得到 %s → %s", pts[0].Time, pts[len(pts)-1].Time)
	}

	last := pts[len(pts)-1]
	if d := math.Abs(last.MainNet - last.XLNet - last.LGNet); d > 1 {
		t.Errorf("收盘点 主力 ≠ 超大 + 大，差 %.0f —— 四档顺序解析错了", d)
	}
	// 分时是**当日累计**而不是每分钟增量：末点应当约等于全天日线值 139.07 亿。
	// 这条挂了说明把累计当增量（或反过来）理解了，图会画成完全不同的东西。
	if math.Abs(last.MainNet-13906735334) > 1000 {
		t.Errorf("收盘累计主力应约 139.07 亿，得到 %.0f —— 分时是累计值不是增量", last.MainNet)
	}
	// 11:30 半日快照：产品稿据此论证「半天统计一次会漏掉一多半」
	var half float64
	for _, p := range pts {
		if p.Time == "11:30" {
			half = p.MainNet
		}
	}
	if half == 0 {
		t.Fatal("没找到 11:30 的点")
	}
	if ratio := half / last.MainNet; ratio < 0.35 || ratio > 0.45 {
		t.Errorf("11:30 累计应约为全天的 39%%，实得 %.0f%% —— 数据或解析变了，产品稿的论证要跟着复核", ratio*100)
	}
}

func TestParseFlowDaily(t *testing.T) {
	days, err := parseFlowDaily(fixture(t, "fflow_daykline_BK1215_20260814.json"))
	if err != nil {
		t.Fatalf("parseFlowDaily: %v", err)
	}
	// 东财对历史日线卡 120 个交易日，lmt=0 也突破不了。
	// 这个数字变了是好事（说明放宽了），但产品稿里「必须自己落库攒历史」的论断要复核。
	if len(days) != 120 {
		t.Fatalf("期望 120 个交易日（东财硬上限），得到 %d", len(days))
	}
	if days[0].Date != "2026-02-13" || days[len(days)-1].Date != "2026-08-14" {
		t.Errorf("日期范围应为 2026-02-13 → 2026-08-14，得到 %s → %s",
			days[0].Date, days[len(days)-1].Date)
	}

	last := days[len(days)-1]
	if math.Abs(last.MainNet-13906735360) > 1 {
		t.Errorf("最后一天主力净额应为 13906735360，得到 %.0f", last.MainNet)
	}
	if math.Abs(last.Close-7736.89) > 1e-6 || math.Abs(last.ChangePct-2.06) > 1e-6 {
		t.Errorf("收盘/涨跌幅应为 7736.89/2.06，得到 %v/%v —— 日线字段偏移了", last.Close, last.ChangePct)
	}
	if math.Abs(last.MainPct-6.86) > 1e-6 {
		t.Errorf("主力净占比应为 6.86，得到 %v", last.MainPct)
	}
	for _, d := range days {
		if x := math.Abs(d.MainNet - d.XLNet - d.LGNet); x > 1 {
			t.Errorf("%s 主力 ≠ 超大 + 大，差 %.0f", d.Date, x)
		}
	}
}

func TestEmNumTolerLatesMissingValues(t *testing.T) {
	// 东财在数据缺失时给字符串 "-"，直接按 float64 反序列化会整条记录失败。
	body := `{"data":{"total":1,"diff":[{"f12":"BK9999","f14":"测试板块",
		"f2":"-","f3":"-","f62":123.0,"f184":"-","f66":100.0,"f72":23.0,
		"f78":"-","f84":"-","f204":"","f205":""}]}}`
	rows, err := parseSectorRank([]byte(body), KindConcept)
	if err != nil {
		t.Fatalf("含 \"-\" 的响应不该报错: %v", err)
	}
	if len(rows) != 1 || rows[0].Price != 0 || rows[0].MainPct != 0 {
		t.Fatalf("\"-\" 应当被吃成 0，得到 %+v", rows)
	}
	if rows[0].MainNet != 123 {
		t.Errorf("同一条里的正常字段应保留，主力净额期望 123，得到 %v", rows[0].MainNet)
	}
}

func TestFsOfRejectsUnknownKind(t *testing.T) {
	if _, err := fsOf(SectorKind("region")); err == nil {
		t.Error("地域板块是故意不支持的，应当报错而不是静默返回空 fs")
	}
}

func TestParseSectorRankDerivesDataDate(t *testing.T) {
	// f124 是行情时间戳。必须用它来判定数据属于哪个交易日，而不是 time.Now()：
	// 周末/节假日请求时东财返回的是**上一个交易日**的数据，用「今天」落库
	// 会把周五的数字记到周六名下，历史图表整体错位且极难发现。
	rows, err := parseSectorRank(fixture(t, "fflow_rank_industry_20260814.json"), KindIndustry)
	if err != nil {
		t.Fatalf("parseSectorRank: %v", err)
	}
	// 夹具是 2026-08-15（周六）抓的，但 f124 指向 2026-08-14（周五）收盘
	for _, r := range rows {
		if r.DataDate != "2026-08-14" {
			t.Fatalf("%s 的 DataDate 应为 2026-08-14（f124 时间戳所属交易日），得到 %q",
				r.Code, r.DataDate)
		}
	}
}

func TestParseSectorRankToleratesMissingTimestamp(t *testing.T) {
	body := `{"data":{"total":1,"diff":[{"f12":"BK1","f14":"甲","f62":1.0}]}}`
	rows, err := parseSectorRank([]byte(body), KindIndustry)
	if err != nil {
		t.Fatalf("缺 f124 不该报错: %v", err)
	}
	if rows[0].DataDate != "" {
		t.Errorf("没有时间戳时 DataDate 应留空，让上层决定怎么办，得到 %q", rows[0].DataDate)
	}
}
