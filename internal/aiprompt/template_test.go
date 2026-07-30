package aiprompt

import "strings"
import "testing"

// 模板里的开发者注释绝不能出现在用户复制的内容里 ——
// 原来的实现把 484 字的 version/data_dependencies/philosophy_check 一起返回了
func TestRenderStripsDeveloperComments(t *testing.T) {
	tpl := `<!--
version: 1.0
philosophy_check:
  - [x] 内部说明
-->

# 标题 {name}`
	out, missing := Render(tpl, map[string]string{"{name}": "测试"})
	if strings.Contains(out, "philosophy_check") || strings.Contains(out, "<!--") {
		t.Errorf("开发者注释没被剥掉:\n%s", out)
	}
	if !strings.HasPrefix(out, "# 标题 测试") {
		t.Errorf("正文渲染不对: %q", out)
	}
	if len(missing) != 0 {
		t.Errorf("不该有缺失占位符，得到 %v", missing)
	}
}

// 未提供的占位符要被报出来，绝不能把 {seed_code} 直接发给用户
func TestRenderReportsMissing(t *testing.T) {
	out, missing := Render("代码 {seed_code}，名称 {seed_name}", map[string]string{"{seed_code}": "006195"})
	if len(missing) != 1 || missing[0] != "{seed_name}" {
		t.Errorf("应报出 {seed_name} 缺失，得到 %v", missing)
	}
	filled := FillMissing(out, "（暂无）")
	if strings.Contains(filled, "{") {
		t.Errorf("兜底后仍有占位符: %q", filled)
	}
}

// 支持不带花括号的键名写法
func TestRenderAcceptsBareKeys(t *testing.T) {
	out, _ := Render("你好 {who}", map[string]string{"who": "世界"})
	if out != "你好 世界" {
		t.Errorf("裸键名替换失败: %q", out)
	}
}
