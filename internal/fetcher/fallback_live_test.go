package fetcher

import (
	"os"
	"testing"
)

// 联网的降级验证。默认跳过，需要时：go test ./internal/fetcher/ -run Live -v -tags= 且设 SANHU_LIVE=1
// 之所以不默认跑：它会真的打东财，CI/离线环境下既慢又会失败，
// 而且高频跑正是限流的成因。
func TestLiveRankFallsBackToDelayHost(t *testing.T) {
	if os.Getenv("SANHU_LIVE") == "" {
		t.Skip("需要 SANHU_LIVE=1 才跑（会真实请求东财）")
	}
	rows, err := FetchSectorFlowRank(KindIndustry)
	if err != nil {
		t.Fatalf("主源和备源都失败: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("没拿到任何板块")
	}
	t.Logf("拿到 %d 个板块，数据日期 %s", len(rows), rows[0].DataDate)
	t.Logf("榜首: %s %s 主力 %.1f 亿", rows[0].Code, rows[0].Name, rows[0].MainNet/1e8)
	// 两头都要有：流入侧和流出侧
	var pos, neg int
	for _, r := range rows {
		if r.MainNet > 0 {
			pos++
		} else if r.MainNet < 0 {
			neg++
		}
	}
	t.Logf("流入 %d 个 / 流出 %d 个", pos, neg)
	if pos == 0 || neg == 0 {
		t.Errorf("榜单两头都要有数据，得到 流入%d 流出%d —— 备源的 po=0 那一页可能没取到", pos, neg)
	}
	for _, r := range rows {
		if d := r.MainNet - r.XLNet - r.LGNet; d > 1 || d < -1 {
			t.Fatalf("%s 主力 ≠ 超大+大，字段错位", r.Code)
		}
	}
}

func TestLiveMinuteFallsBack(t *testing.T) {
	if os.Getenv("SANHU_LIVE") == "" {
		t.Skip("需要 SANHU_LIVE=1 才跑")
	}
	pts, err := FetchSectorMinute("BK1215")
	if err != nil {
		t.Fatalf("分时主源和备源都失败: %v", err)
	}
	t.Logf("分时 %d 个点，%s → %s，收盘累计 %.1f 亿",
		len(pts), pts[0].Time, pts[len(pts)-1].Time, pts[len(pts)-1].MainNet/1e8)
	if len(pts) < 100 {
		t.Errorf("分时点数偏少（%d），备源可能返回了残缺数据", len(pts))
	}
}
