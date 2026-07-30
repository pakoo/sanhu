// Package aiprompt 生成给用户复制、粘贴到 Claude / ChatGPT 的提示词。
//
// 产品稿：docs/product-specs/ai-everywhere.html（场景定义见 scene.go 顶部的决策摘要）
//
// 三条硬规则，八个场景共用：
//  1. **金额策略由调用方显式选择**。Q7=B 定的是默认带真实金额、可一键脱敏，
//     所以金额一律走 Options.Amount()，不要在 builder 里直接 fmt 金额 ——
//     漏一处就等于绕过了脱敏开关。开头的 Options.RedactNote() 会如实告诉 AI 当前是哪种口径。
//  2. **数字必须格式化**。之前选基导出里出现过 7.1899999999999995，
//     这种东西粘给 AI 既浪费 token 又显得不可信。
//  3. **有指令、有提问**。光给数据 AI 只会反问「你想让我分析什么」——
//     这正是原来那几个入口失败的原因。结尾统一挂 Footer()。
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

// Pp 百分点差值。注意不能写成 Pct(v)+"pp" —— Pct 自带 % 号，
// 拼出来是 "+22.3%pp" 这种既不是百分比也不是百分点的东西（实测踩过）。
func Pp(v float64) string {
	s := trimZeros(fmt.Sprintf("%.2f", v)) + "pp"
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

// Footer 每份提示词结尾都带上的输出约束 + 工具操作地图。
//
// 这一段是「AI 输出可达性」的保障，八个场景共用一份 —— 各写各的必然漂移，
// 现实里就发生过：赛道分析那份写得很完整，另外三个入口一条都没有。
//
// 三件事：① 逼 AI 用普通投资者能看懂的话；② 每条建议要落到工具里的具体路径；
// ③ 不许用「请咨询专业理财顾问」这类免责套话充当主要回答。
func Footer() string {
	return strings.TrimSpace(`
---

## 输出约束（请在正文开头先复述第一句再开始）

> **请在正文第一句写**："我将使用普通投资者能看懂的语言，为专业术语附上生活类比，并为每个建议指明工具内的操作路径。"

1. **语言**：每个专业术语**首次出现时**用括号附 20 字以内的生活类比。例：最大回撤（从最高点跌下来的最大幅度）。
2. **禁用词**：alpha / beta / 均值回归 / 右侧上涨 / 左侧布局 / capex / 实际利率，以及各类行话缩写。要表达这些概念请换成白话。
3. **行动路径**：每个"建议做 X"后面用括号写明工具内的操作路径（见下方地图）。
4. **免责声明**：不要用"请咨询专业理财顾问"这类套话充当主要回答。我要的是基于上面数据的具体判断，免责话放结尾一句带过。

## 工具内操作地图（生成行动路径时引用）

- 把一只基金加入长期跟踪 → 「总览」→ 子 Tab「关注列表」→ 点 + → 输入代码（工具 24 小时内自动抓持仓）
- 用假钱假装买一笔（不花真钱）→ 「决策」→ 手动创建 → 勾选 🧪 虚拟建仓 → 填代码提交
- 让工具按策略模拟一段历史 → 「🧪 模拟盘」→ 填策略和参数 → 创建并运行
- 查一只基金的同赛道竞品 → 点开基金详情 → 「🛰️ 赛道横评」
- 查当前持仓的估值与集中度风险 → 「风险分析」或「调仓建议」
- 看穿透后的真实行业敞口与重叠持股 → 「持仓透视」

*本提示词由本地工具根据你的真实数据生成，AI 回答仅供参考，不构成投资建议。*`)
}
