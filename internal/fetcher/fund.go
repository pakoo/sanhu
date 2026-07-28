package fetcher

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pakoo/sanhu/internal/fundcat"
)

var (
	reFSName      = regexp.MustCompile(`var\s+fS_name\s*=\s*"([^"]*)"`)
	reFSType      = regexp.MustCompile(`var\s+fS_type\s*=\s*"([^"]*)"`)
	reInception   = regexp.MustCompile(`成立日期[^<]*</th>\s*<td[^>]*>([0-9]{4}-[0-9]{2}-[0-9]{2})`)
	reAUM         = regexp.MustCompile(`基金规模[^<]*</th>\s*<td[^>]*>([0-9.]+)亿元`)
	reManager     = regexp.MustCompile(`基金经理[^<]*</th>\s*<td[^>]*><a[^>]*>([^<]+)</a>`)
	reCompany     = regexp.MustCompile(`基金公司[^<]*</th>\s*<td[^>]*><a[^>]*>([^<]+)</a>`)
	reFeeRate     = regexp.MustCompile(`管理费率[^<]*</th>\s*<td[^>]*>([0-9.]+)%`)
	reRiskLevel   = regexp.MustCompile(`风险等级[^<]*</th>\s*<td[^>]*>([^<]+)</td>`)
)

// 分类判定统一走 fundcat 包（映射表只此一份，见该包注释）
func inferCategory(fundType string) string { return fundcat.Infer(fundType) }

// FetchFundInfo 抓取单只基金基本信息，UPSERT 到 funds 表
func FetchFundInfo(db *sqlx.DB, code string) error {
	// 先从 pingzhongdata JS 拿名字和类型
	jsURL := fmt.Sprintf("https://fund.eastmoney.com/pingzhongdata/%s.js?v=%d", code, time.Now().Unix())
	jsBody, err := get(jsURL)
	if err != nil {
		return fmt.Errorf("fetch fund js %s: %w", code, err)
	}
	jsText := string(jsBody)

	name := extractRegex(reFSName, jsText)
	fundType := extractRegex(reFSType, jsText)

	// 再从 jbgk HTML 拿详细信息
	htmlURL := fmt.Sprintf("https://fundf10.eastmoney.com/jbgk_%s.html", code)
	htmlBody, err := get(htmlURL)
	if err != nil {
		log.Printf("[fetcher] fund detail html %s: %v", code, err)
	}
	htmlText := string(htmlBody)

	inception := extractRegex(reInception, htmlText)
	aumStr := extractRegex(reAUM, htmlText)
	manager := extractRegex(reManager, htmlText)
	company := extractRegex(reCompany, htmlText)
	feeStr := extractRegex(reFeeRate, htmlText)
	riskLevel := strings.TrimSpace(extractRegex(reRiskLevel, htmlText))

	aum, _ := strconv.ParseFloat(aumStr, 64)
	feeRate, _ := strconv.ParseFloat(feeStr, 64)
	category := inferCategory(fundType)

	_, err = db.Exec(`
		INSERT INTO funds (code, name, fund_type, category, risk_level, fee_rate,
		                   manager, company, inception_date, aum, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(code) DO UPDATE SET
		  name=excluded.name,
		  fund_type=CASE WHEN excluded.fund_type!='' THEN excluded.fund_type ELSE fund_type END,
		  category=CASE WHEN excluded.category!='' THEN excluded.category ELSE category END,
		  risk_level=CASE WHEN excluded.risk_level!='' THEN excluded.risk_level ELSE risk_level END,
		  fee_rate=CASE WHEN excluded.fee_rate!=0 THEN excluded.fee_rate ELSE fee_rate END,
		  manager=CASE WHEN excluded.manager!='' THEN excluded.manager ELSE manager END,
		  company=CASE WHEN excluded.company!='' THEN excluded.company ELSE company END,
		  inception_date=CASE WHEN excluded.inception_date!='' THEN excluded.inception_date ELSE inception_date END,
		  aum=CASE WHEN excluded.aum!=0 THEN excluded.aum ELSE aum END,
		  updated_at=datetime('now')
	`, code, name, fundType, category, riskLevel, feeRate,
		manager, company, inception, aum)
	return err
}

// FetchFundInfoAll 批量更新 holdings + watchlist 里所有基金（3 workers）
func FetchFundInfoAll(db *sqlx.DB) error {
	codes, err := allFundCodes(db)
	if err != nil {
		return err
	}
	log.Printf("[fetcher] FetchFundInfoAll: %d funds", len(codes))
	errs := workerPool(codes, 3, 500*time.Millisecond, func(code string) (struct{}, error) {
		return struct{}{}, FetchFundInfo(db, code)
	})
	for _, e := range errs {
		log.Printf("[fetcher] fund info error: %v", e)
	}
	return nil
}

func extractRegex(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}
