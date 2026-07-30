// Package aiprompt 生成给用户复制、粘贴到 Claude / ChatGPT 的提示词。
//
// 产品稿：docs/product-specs/ai-workflow.html
//
//	Q2 = 完整 prompt（背景 + 结构化数据 + 具体提问）
//	Q3 = 只复制，不给任何外链 —— 本包不产出任何 URL
//	Q5 = 本轮不落库
//
// 三条硬规则，四个入口共用：
//  1. **不含金额与账户信息**。只发占比、分类、涨跌幅这类相对量。
//     用户敢用的前提是知道发出去的是什么，预览弹窗里也照这个口径写。
//  2. **数字必须格式化**。之前选基导出里出现过 7.1899999999999995，
//     这种东西粘给 AI 既浪费 token 又显得不可信。
//  3. **有指令、有提问**。光给数据 AI 只会反问「你想让我分析什么」——
//     这正是原来那几个入口失败的原因。
package aiprompt

import (
	"fmt"
	"strings"
)

// CategoryNames 分类的中文名。
// 之前 AI 上下文里直接输出 bond/equity/qdii，AI 得自己猜这是什么。
var CategoryNames = map[string]string{
	"bond":   "债券型",
	"equity": "股票型",
	"mixed":  "混合型",
	"qdii":   "QDII/海外",
	"money":  "货币型",
}

// CategoryName 取中文名，未知分类原样返回
func CategoryName(cat string) string {
	if v, ok := CategoryNames[cat]; ok {
		return v
	}
	if cat == "" {
		return "未分类"
	}
	return cat
}

// Pct 百分数：保留两位，去掉尾随的 0，带 % 号
func Pct(v float64) string {
	return trimZeros(fmt.Sprintf("%.2f", v)) + "%"
}

// PctSigned 带正负号的百分数，用于涨跌幅
func PctSigned(v float64) string {
	s := Pct(v)
	if v > 0 {
		return "+" + s
	}
	return s
}

// Num 一般数值：保留两位、去尾零
func Num(v float64) string {
	return trimZeros(fmt.Sprintf("%.2f", v))
}

// trimZeros 12.30 → 12.3，12.00 → 12
func trimZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// Table 生成 markdown 表格。行数为 0 时返回一句说明，
// 而不是留一个空表格让 AI 以为数据缺失。
func Table(headers []string, rows [][]string, emptyNote string) string {
	if len(rows) == 0 {
		if emptyNote == "" {
			emptyNote = "（暂无数据）"
		}
		return emptyNote
	}

	var b strings.Builder
	b.WriteString("| " + strings.Join(headers, " | ") + " |\n")
	seps := make([]string, len(headers))
	for i := range seps {
		seps[i] = "---"
	}
	b.WriteString("| " + strings.Join(seps, " | ") + " |\n")
	for _, r := range rows {
		// 列数不齐时补空，避免生成错位的 markdown
		cells := make([]string, len(headers))
		for i := range headers {
			if i < len(r) {
				cells[i] = r[i]
			}
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// Section 拼一个带标题的段落，内容为空时整段跳过 ——
// 宁可少一节，也不要给 AI 一个空标题让它去猜。
func Section(title, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	return "## " + title + "\n\n" + body
}

// Numberer 给章节自动编号。
//
// 不能把序号写死在标题里：某一节因为数据缺失被跳过时，
// 编号就会出现 1、2、4 这种断号（实测出现过）。
type Numberer struct{ n int }

// Next 返回下一个带序号的标题
func (x *Numberer) Next(title string) string {
	x.n++
	return fmt.Sprintf("%d. %s", x.n, title)
}

// Join 把若干段落用空行连起来，自动跳过空段
func Join(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, strings.TrimSpace(p))
		}
	}
	return strings.Join(out, "\n\n")
}

// Questions 结尾的提问块。没有它，AI 只会反问「你想让我分析什么」。
func Questions(qs ...string) string {
	if len(qs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 请回答\n\n")
	for i, q := range qs {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, q))
	}
	return strings.TrimRight(b.String(), "\n")
}

// PrivacyNote 每份提示词结尾都带上，让 AI 知道数据边界，
// 也让用户复制走的时候看得见这句承诺。
const PrivacyNote = "> 以上数据由本地工具整理，仅含比例与分类，**不含持仓金额、账户信息**。"
