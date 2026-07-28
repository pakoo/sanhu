// Package fundcat 是基金分类（bond/equity/mixed/qdii/money）的唯一判定处。
//
// 抓取时（fetcher）和灌初始数据后（bootstrap）都要判分类，映射表放一份，避免两边漂移——
// Go 重写初期就是因为 fetcher 自己搞了一套子串规则，把「指数型-固收」判成 equity、
// 「指数型-海外股票」判成 equity，与 Python 原版的精确映射表不一致。
package fundcat

import (
	"strings"

	"github.com/jmoiron/sqlx"
)

// exactMap 与 Python 原版 config.FUND_CATEGORY_MAP 一一对应，精确匹配优先。
var exactMap = map[string]string{
	"指数型-固收":   "bond",
	"债券型-混合一级": "bond",
	"债券型-混合二级": "bond",
	"债券型-长债":   "bond",
	"债券型-中短债":  "bond",
	"纯债型":      "bond",
	"股票型":      "equity",
	"指数型-股票":   "equity",
	"混合型-偏股":   "mixed",
	"混合型-平衡":   "mixed",
	"混合型-偏债":   "mixed",
	"混合型-灵活":   "mixed",
	"指数型-海外股票": "qdii",
	"QDII":     "qdii",
}

// keywordRules 仅用于 exactMap 未覆盖的新类型，顺序即优先级：
// 海外/QDII 先于股票，债/固收 先于指数——否则「指数型-XX债」这类会被误判成 equity。
var keywordRules = []struct{ keyword, category string }{
	{"QDII", "qdii"},
	{"海外", "qdii"},
	{"债", "bond"},
	{"固收", "bond"},
	{"货币", "money"},
	{"指数", "equity"},
	{"股票", "equity"},
	{"混合", "mixed"},
}

// Infer 按 fund_type 判定分类；无法判定时返回 mixed。
func Infer(fundType string) string {
	if cat, ok := exactMap[fundType]; ok {
		return cat
	}
	for _, rule := range keywordRules {
		if strings.Contains(fundType, rule.keyword) {
			return rule.category
		}
	}
	return "mixed"
}

// NormalizeAll 按 fund_type 重算全表 category，返回修正的行数。
// 内嵌 seed 是用旧代码生成的，category 全是 mixed 而 fund_type 是对的，
// 灌库后跑一次即可纠正；fund_type 为空的行保持不动，等抓取时补。
func NormalizeAll(db *sqlx.DB) (int, error) {
	var rows []struct {
		Code     string `db:"code"`
		FundType string `db:"fund_type"`
		Category string `db:"category"`
	}
	if err := db.Select(&rows, `
		SELECT code, COALESCE(fund_type,'') AS fund_type, COALESCE(category,'') AS category
		FROM funds WHERE COALESCE(fund_type,'') != ''`); err != nil {
		return 0, err
	}

	tx, err := db.Beginx()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	fixed := 0
	for _, r := range rows {
		want := Infer(r.FundType)
		if want == r.Category {
			continue
		}
		if _, err := tx.Exec(`UPDATE funds SET category=? WHERE code=?`, want, r.Code); err != nil {
			return 0, err
		}
		fixed++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return fixed, nil
}
