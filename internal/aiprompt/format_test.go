package aiprompt

import "strings"
import "testing"

// 章节编号必须连续：某节因数据缺失被跳过时，不能出现 1、2、4 这种断号
func TestNumbererSkipsEmptySections(t *testing.T) {
	sec := &Numberer{}
	a := Section(sec.Next("资产配置"), "有内容")
	b := Section("不编号的空节", "") // 空节不占号
	c := Section(sec.Next("逐只持仓"), "有内容")
	out := Join(a, b, c)

	if !strings.Contains(out, "## 1. 资产配置") {
		t.Errorf("缺少第 1 节，实际输出:\n%s", out)
	}
	if !strings.Contains(out, "## 2. 逐只持仓") {
		t.Errorf("第二节应编号为 2，实际输出:\n%s", out)
	}
	if strings.Contains(out, "不编号的空节") {
		t.Error("空节不该出现在输出里")
	}
}

func TestPctTrimsZeros(t *testing.T) {
	cases := map[float64]string{
		7.1899999999999995: "7.19%", // 这个浮点数曾原样出现在导出里
		12.30:              "12.3%",
		12.00:              "12%",
		0:                  "0%",
	}
	for in, want := range cases {
		if got := Pct(in); got != want {
			t.Errorf("Pct(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestCategoryNameTranslates(t *testing.T) {
	if got := CategoryName("bond"); got != "债券型" {
		t.Errorf("bond 应译成债券型，得到 %q", got)
	}
	if got := CategoryName(""); got != "未分类" {
		t.Errorf("空分类应为未分类，得到 %q", got)
	}
}

// 空数据要给一句说明，不能留个空表格让 AI 以为数据缺失
func TestTableEmptyNote(t *testing.T) {
	got := Table([]string{"A", "B"}, nil, "（没有数据）")
	if got != "（没有数据）" {
		t.Errorf("空表应返回说明，得到 %q", got)
	}
}

// 列数不齐时补空，不能生成错位的 markdown
func TestTableRaggedRows(t *testing.T) {
	got := Table([]string{"A", "B", "C"}, [][]string{{"1"}}, "")
	if !strings.Contains(got, "| 1 |  |  |") {
		t.Errorf("短行应补空单元格，得到:\n%s", got)
	}
}
