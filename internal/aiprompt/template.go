package aiprompt

import (
	"regexp"
	"strings"
)

// 模板渲染。
//
// prompts/theme_analysis.md 开头有一段 <!-- version / data_dependencies /
// philosophy_check --> 注释，那是写给开发者看的元信息。
// 原来的实现直接 ReadFile 原样返回，于是这 484 字连同未替换的 {seed_name}
// 一起被用户复制进了 Claude 的对话框。

var (
	htmlCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	placeholderRe = regexp.MustCompile(`\{[a-z_0-9]+\}`)
)

// Render 用 vars 替换模板里的 {占位符}，并剥掉开发者注释。
//
// 返回 rendered 和 missing：missing 是模板里出现、但 vars 没提供的占位符。
// 调用方据此决定是否放行 —— 宁可报错，也不要把 {seed_code} 这种东西发给用户。
func Render(tpl string, vars map[string]string) (rendered string, missing []string) {
	body := strings.TrimSpace(htmlCommentRe.ReplaceAllString(tpl, ""))

	for k, v := range vars {
		key := k
		if !strings.HasPrefix(key, "{") {
			key = "{" + key + "}"
		}
		body = strings.ReplaceAll(body, key, v)
	}

	seen := map[string]bool{}
	for _, m := range placeholderRe.FindAllString(body, -1) {
		if !seen[m] {
			seen[m] = true
			missing = append(missing, m)
		}
	}
	return body, missing
}

// FillMissing 把仍未替换的占位符换成占位说明。
//
// 用在「数据确实缺失」而不是「忘了传」的场合：比如某只基金没有季报持仓，
// 与其让整个接口失败，不如如实写「暂无数据」，AI 也能据此调整回答。
func FillMissing(body string, note string) string {
	if note == "" {
		note = "（暂无数据）"
	}
	return placeholderRe.ReplaceAllString(body, note)
}
