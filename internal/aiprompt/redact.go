package aiprompt

import (
	"fmt"
	"strings"
)

// 金额脱敏（产品稿 Q7 = B：默认带真实金额，可一键脱敏）。
//
// 这个开关存在的理由：提示词是要被复制到外部 AI 服务的，一旦粘出去就离开了本机，
// 对方的留存策略我们控制不了。占比能表达几乎同样的信息（"减到总仓位的 7%"
// 和 "减 8,000 元" 对判断质量的差别很小），只是要用户自己做一次乘法。
// 默认按 Q7=B 关闭脱敏，需要时前端传 ?redact=1 打开。

// Options 渲染选项
type Options struct {
	// Redact 为 true 时，所有绝对金额都不写进提示词，只保留占比与收益率
	Redact bool
}

// Amount 金额。脱敏时返回 ("", false)，调用方据此决定整列/整句是否输出。
func (o Options) Amount(v float64) (string, bool) {
	if o.Redact {
		return "", false
	}
	return "¥" + thousands(v), true
}

// AmountOr 金额，脱敏时返回给定的替代文本（用于表格里必须占一格的场合）
func (o Options) AmountOr(v float64, alt string) string {
	if s, ok := o.Amount(v); ok {
		return s
	}
	return alt
}

// AmountLine 组装「标签：金额」一行，脱敏时整行返回空串，
// 由 Join / Section 自动跳过 —— 比留一行 "总市值：—" 干净。
func (o Options) AmountLine(label string, v float64) string {
	s, ok := o.Amount(v)
	if !ok {
		return ""
	}
	return fmt.Sprintf("- %s：**%s**", label, s)
}

// RedactNote 写进提示词开头的数据边界说明。两种模式说法不同，
// 让 AI 知道该用绝对数还是百分比来给建议。
func (o Options) RedactNote() string {
	if o.Redact {
		return "> 数据边界：以下内容**只含比例与收益率，不含任何持仓金额**。" +
			"因此请用「占总仓位的百分之几」来表述你的建议，不要给出具体金额。"
	}
	return "> 数据边界：以下内容含我的真实持仓金额，但**不含账户、姓名等身份信息**。" +
		"你可以直接用金额来表述建议。"
}

// thousands 12345.6 → 12,345.60；给人看的金额一律带千分位，
// 之前选基导出里出现过 7.1899999999999995 这种，粘给 AI 既费 token 又显得不可信。
func thousands(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%.2f", v)
	intPart, frac, _ := strings.Cut(s, ".")

	var b strings.Builder
	for i, ch := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	out := b.String()
	if frac != "00" {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}
