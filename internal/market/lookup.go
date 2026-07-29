package market

import (
	"fmt"
	"strings"

	"github.com/pakoo/sanhu/internal/fetcher"
)

// NormalizeQuoteCode 把用户输入的代码转成新浪的行情代码。
//
// 用户会怎么输：NVDA / nvda / 300308 / 600519 / 688256。
// 返回空串表示识别不了，调用方据此给出明确提示，而不是拿着错代码去请求。
func NormalizeQuoteCode(raw string) (code, unit string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ""
	}

	// 已经是完整的新浪代码就直接用
	for _, p := range []string{"gb_", "sh", "sz", "bj", "hf_", "fx_", "int_", "znb_"} {
		if strings.HasPrefix(s, p) {
			unit = "$"
			if p == "sh" || p == "sz" || p == "bj" {
				unit = "¥"
			}
			return s, unit
		}
	}

	// 6 位纯数字 = A股，按首位判断交易所
	if len(s) == 6 && isDigits(s) {
		switch s[0] {
		case '6': // 沪市主板 60xxxx / 科创板 688xxx
			return "sh" + s, "¥"
		case '0', '3': // 深市主板 00xxxx / 创业板 30xxxx
			return "sz" + s, "¥"
		case '4', '8': // 北交所
			return "bj" + s, "¥"
		}
		return "", ""
	}

	// 纯字母 = 美股代码
	if isLetters(s) {
		return "gb_" + strings.ToLower(s), "$"
	}
	return "", ""
}

// LookupOne 查一个代码的行情，用于添加名单时的即时校验
func LookupOne(code string) (fetcher.Quote, error) {
	quotes, err := fetcher.FetchSinaQuotes([]string{code})
	if err != nil {
		return fetcher.Quote{}, err
	}
	q, ok := quotes[code]
	if !ok {
		return fetcher.Quote{}, fmt.Errorf("no quote for %s", code)
	}
	return q, nil
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

func isLetters(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '.' {
			return false
		}
	}
	return len(s) > 0
}
