// Package importer 支付宝基金截图 OCR 导入
package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"github.com/jmoiron/sqlx"
)

// ─── 公开数据类型 ──────────────────────────────────────────────────────────────

type HoldingRow struct {
	RawName   string  `json:"raw_name"`
	Shares    float64 `json:"shares"`
	Amount    float64 `json:"amount"`
	Profit    float64 `json:"profit"`
	ProfitPct float64 `json:"profit_pct"`
	// matched fields (filled by FuzzyMatchFund)
	Code       string       `json:"code"`
	Name       string       `json:"name"`
	Confidence string       `json:"confidence"`
	Candidates []FundMatch  `json:"candidates"`
}

type TransactionRow struct {
	RawName    string  `json:"raw_name"`
	Date       string  `json:"date"`
	TxType     string  `json:"tx_type"`
	Amount     float64 `json:"amount"`
	Shares     float64 `json:"shares"`
	Nav        float64 `json:"nav"`
	Confidence string  `json:"confidence"`
	// matched fields
	Code       string      `json:"code"`
	Name       string      `json:"name"`
	Candidates []FundMatch `json:"candidates"`
}

type FundMatch struct {
	Code  string  `json:"code"`
	Name  string  `json:"name"`
	Score float64 `json:"score"`
}

// ─── OCR.space 调用 ────────────────────────────────────────────────────────────

// OCRImage 调用 OCR.space API，返回按阅读顺序排列的文本行
func OCRImage(apiKey string, imageBytes []byte, filename string) ([]string, error) {
	if apiKey == "" {
		apiKey = "K88263306488957" // 公共 demo key，不稳定
	}

	// 检测文件名
	fname := "screenshot.jpg"
	if filename != "" {
		fname = filename
	} else if len(imageBytes) >= 4 {
		if imageBytes[0] == 0x89 && imageBytes[1] == 'P' {
			fname = "screenshot.png"
		} else if bytes.HasPrefix(imageBytes, []byte("RIFF")) {
			fname = "screenshot.webp"
		}
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("apikey", apiKey)
	_ = mw.WriteField("language", "chs")
	_ = mw.WriteField("isOverlayRequired", "false")
	_ = mw.WriteField("OCREngine", "2")
	fw, err := mw.CreateFormFile("file", fname)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := fw.Write(imageBytes); err != nil {
		return nil, fmt.Errorf("write image: %w", err)
	}
	mw.Close()

	req, err := http.NewRequest("POST", "https://api.ocr.space/parse/image", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// 不设 mime content-type，让 multipart boundary 控制

	client := &http.Client{Timeout: 30 * 1000000000} // 30s
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ocr.space request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ocr.space HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var result struct {
		IsErroredOnProcessing bool   `json:"IsErroredOnProcessing"`
		ErrorMessage          string `json:"ErrorMessage"`
		ParsedResults         []struct {
			ParsedText string `json:"ParsedText"`
		} `json:"ParsedResults"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse ocr response: %w", err)
	}
	if result.IsErroredOnProcessing {
		return nil, fmt.Errorf("ocr.space error: %s", result.ErrorMessage)
	}
	if len(result.ParsedResults) == 0 {
		return nil, nil
	}

	text := result.ParsedResults[0].ParsedText
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// ─── 数字清洗 ──────────────────────────────────────────────────────────────────

var (
	reHoldingsDetail = regexp.MustCompile(
		`持有份额[：:\s]*([\d,，]+\.?\d*)` +
			`.*?持有(?:市值|金额)[：:\s]*([\d,，]+\.?\d*)元` +
			`.*?持有收益[：:\s]*([+\-＋－]?[\d,，]+\.?\d*)元`,
	)
	reDateTx = regexp.MustCompile(
		`(\d{4}[-年]\d{1,2}[-月]\d{1,2}日?)\s*(买入成功|卖出成功|买入|卖出|赎回成功|赎回)`,
	)
	reTxDetail = regexp.MustCompile(
		`(?:金额|成交金额)[：:\s]*([\d,，]+\.?\d*)元` +
			`.*?(?:份额|成交份额)[：:\s]*([\d,，]+\.?\d*)份` +
			`.*?(?:净值|成交净值)[：:\s]*([\d.]+)元`,
	)
	reNumToken    = regexp.MustCompile(`[+\-]?\d[\d]*(?:\.\d+)?`)
	reBigAmtLine  = regexp.MustCompile(`^\d{1,3}(?:,\d{3})*\.\d{2}$`)
	reExplicitPct = regexp.MustCompile(`^([+\-]?\d+\.\d+)%\.?$`)
	reSignedVal   = regexp.MustCompile(`^([+\-]\d[\d,.]+)$`)
	reContLine    = regexp.MustCompile(`^(.*?)\s*[\d.]+\s+([+\-]?\d+\.\d+)%`)
	rePctInRest   = regexp.MustCompile(`([+\-]?\d+\.\d+)%`)
	reSignedRest  = regexp.MustCompile(`([+\-]\d[\d,.]+)`)
	reInlineNum   = regexp.MustCompile(`\d[\d,\.]+\d`)
	reChinese     = regexp.MustCompile(`[\x{4e00}-\x{9fff}]`)
	reCNSlash     = regexp.MustCompile(`[\x{4e00}-\x{9fff}]+/[\x{4e00}-\x{9fff}]+`)
	reCNBracket   = regexp.MustCompile(`[\x{4e00}-\x{9fff}]+[（(]元[）)]`)
	reOnlyNums    = regexp.MustCompile(`^[\d.,%+\-\s元份额市值收益昨日持有排序全部偏股偏债指数黄金]+$`)
	reNoisePfx    = regexp.MustCompile(`^[\[\(（【][^\s]{0,10}[\]\)）】]?\s*`)
	reNoiseSuffix = regexp.MustCompile(`^\S{1,8}超额收益\S{0,4}\s*`)
)

var skipNames = map[string]bool{
	"收益明细": true, "持仓分析": true, "交易记录": true, "投资计划": true,
	"清仓分析": true, "反馈与投诉": true, "我的持有": true, "金额排序": true,
	"更多产品": true, "去市场看看": true, "热销基金": true, "去看看": true,
	"基金销售服务": true, "资金安全有保障": true, "我的总资产": true, "市场解读": true,
	"名称": true, "金额": true, "昨日收益": true, "持有收益": true, "持有收益率": true,
}

// cleanNum 解析数字，兼容 OCR 常见错误
func cleanNum(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "，", "")
	s = strings.ReplaceAll(s, "＋", "+")
	s = strings.ReplaceAll(s, "－", "-")
	s = strings.ReplaceAll(s, "~", "-")

	sign := ""
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		sign = string(s[0])
		s = s[1:]
	}

	dots := strings.Count(s, ".")
	commas := strings.Count(s, ",")

	switch {
	case dots >= 2:
		last := strings.LastIndex(s, ".")
		s = strings.ReplaceAll(s[:last], ".", "") + "." + s[last+1:]
	case dots == 0 && commas >= 2:
		last := strings.LastIndex(s, ",")
		s = strings.ReplaceAll(s[:last], ",", "") + "." + s[last+1:]
	case dots == 1 && commas >= 1:
		s = strings.ReplaceAll(s, ",", "")
	case dots == 0 && commas == 1:
		parts := strings.SplitN(s, ",", 2)
		if len(parts[1]) == 2 {
			s = parts[0] + "." + parts[1]
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	default:
		s = strings.ReplaceAll(s, ",", "")
	}

	v, err := strconv.ParseFloat(sign+s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// normalizeNum fixes OCR number artifacts within a string
func normalizeNum(s string) string {
	return reInlineNum.ReplaceAllStringFunc(s, func(m string) string {
		dots := strings.Count(m, ".")
		commas := strings.Count(m, ",")
		switch {
		case dots >= 2:
			last := strings.LastIndex(m, ".")
			return strings.ReplaceAll(m[:last], ".", "") + "." + m[last+1:]
		case dots == 0 && commas >= 2:
			last := strings.LastIndex(m, ",")
			return strings.ReplaceAll(m[:last], ",", "") + "." + m[last+1:]
		case dots == 1 && commas == 1:
			di := strings.Index(m, ".")
			ci := strings.Index(m, ",")
			if di < ci {
				return strings.ReplaceAll(strings.ReplaceAll(m, ".", ""), ",", ".")
			}
			return strings.ReplaceAll(m, ",", "")
		case dots == 0 && commas == 1:
			last := strings.LastIndex(m, ",")
			after := m[last+1:]
			if len(after) == 2 {
				return m[:last] + "." + after
			}
			return strings.ReplaceAll(m, ",", "")
		default:
			return strings.ReplaceAll(m, ",", "")
		}
	})
}

// isFundName returns true if a line looks like a fund name
func isFundName(s string) bool {
	s = strings.TrimSpace(s)
	if len([]rune(s)) < 2 {
		return false
	}
	if !reChinese.MatchString(s) {
		return false
	}
	if reOnlyNums.MatchString(s) {
		return false
	}
	if skipNames[s] {
		return false
	}
	if regexp.MustCompile(`^(产品提醒|市场解读|热销基金|反馈|纯债|超额收益|\[|金选)`).MatchString(s) {
		return false
	}
	if regexp.MustCompile(`^[＜＞〈〉◎○●★☆]`).MatchString(s) {
		return false
	}
	if strings.Contains(s, "资金安全有保障") || strings.Contains(s, "基金销售服务") {
		return false
	}
	if regexp.MustCompile(`\[=|囚|吕`).MatchString(s) {
		return false
	}
	if reCNSlash.MatchString(s) {
		return false
	}
	if reCNBracket.MatchString(s) {
		return false
	}
	return true
}

func normalizeDate(raw string) string {
	cleaned := regexp.MustCompile(`[年月]`).ReplaceAllString(raw, "-")
	cleaned = strings.TrimSuffix(cleaned, "日")
	parts := strings.Split(cleaned, "-")
	if len(parts) == 3 {
		y := parts[0]
		m := parts[1]
		d := parts[2]
		if len(m) == 1 {
			m = "0" + m
		}
		if len(d) == 1 {
			d = "0" + d
		}
		return y + "-" + m + "-" + d
	}
	return cleaned
}

// ─── 持仓截图解析 ──────────────────────────────────────────────────────────────

func ParseHoldingsScreenshot(lines []string) []HoldingRow {
	// Format A: detail page
	var rows []HoldingRow
	detailFound := false

	for i := 0; i < len(lines); i++ {
		combined := lines[i]
		if i+1 < len(lines) {
			combined = lines[i] + " " + lines[i+1]
		}
		m := reHoldingsDetail.FindStringSubmatch(combined)
		if m != nil {
			detailFound = true
			shares, _ := cleanNum(m[1])
			amount, _ := cleanNum(m[2])
			profitStr := strings.NewReplacer("＋", "+", "－", "-").Replace(m[3])
			profit, _ := cleanNum(profitStr)

			rawName := ""
			for j := i - 1; j >= 0 && j >= i-5; j-- {
				if isFundName(strings.TrimSpace(lines[j])) {
					rawName = strings.TrimSpace(lines[j])
					break
				}
			}

			conf := "low"
			filled := 0
			for _, v := range []float64{shares, amount, profit} {
				if v != 0 {
					filled++
				}
			}
			if filled == 3 {
				conf = "high"
			} else if filled == 2 {
				conf = "medium"
			}

			rows = append(rows, HoldingRow{
				RawName:    rawName,
				Shares:     shares,
				Amount:     amount,
				Profit:     profit,
				Confidence: conf,
			})
			i++
		}
	}
	if detailFound {
		return rows
	}

	// Format B: list page (inline amounts)
	for i := 0; i < len(lines); i++ {
		line := normalizeNum(strings.ReplaceAll(lines[i], "~", "-"))

		matches := reNumToken.FindAllStringIndex(line, -1)
		type numMatch struct {
			start, end int
			val        float64
		}
		var nums []numMatch
		for _, idx := range matches {
			v, ok := cleanNum(line[idx[0]:idx[1]])
			if ok {
				nums = append(nums, numMatch{idx[0], idx[1], v})
			}
		}

		// find first "big number" (>=100 with decimal) preceded by Chinese
		var firstNum *numMatch
		namePart1 := ""
		for k := range nums {
			n := &nums[k]
			if !strings.Contains(line[n.start:n.end], ".") {
				continue
			}
			if n.val < 100 {
				continue
			}
			candidateName := strings.TrimSpace(line[:n.start])
			if reChinese.MatchString(candidateName) {
				firstNum = n
				namePart1 = candidateName
				break
			}
		}
		if firstNum == nil || !isFundName(namePart1) {
			continue
		}
		amount := firstNum.val
		if amount < 100 {
			continue
		}

		rest := line[firstNum.end:]
		var pct, profit float64
		var hasPct bool
		if pm := rePctInRest.FindStringSubmatch(rest); pm != nil {
			if v, ok := cleanNum(pm[1]); ok {
				pct = v
				hasPct = true
			}
		}
		if pm := reSignedRest.FindStringSubmatch(rest); pm != nil && !hasPct {
			if v, ok := cleanNum(pm[1]); ok {
				profit = v
			}
		}

		rawName := namePart1
		// look for continuation line
		for look := 1; look <= 2; look++ {
			if i+look >= len(lines) {
				break
			}
			line2 := normalizeNum(strings.ReplaceAll(lines[i+look], "~", "-"))
			line2 = reNoisePfx.ReplaceAllString(line2, "")
			line2 = reNoiseSuffix.ReplaceAllString(line2, "")
			mc := reContLine.FindStringSubmatch(line2)
			if mc != nil {
				name2 := strings.TrimSpace(mc[1])
				if v, ok := cleanNum(mc[2]); ok {
					if !hasPct {
						pct = v
						hasPct = true
					}
				}
				if isFundName(name2) {
					rawName = namePart1 + name2
				}
				i += look
				break
			}
		}

		if profit == 0 && hasPct && amount > 0 {
			profit = roundTo2(amount * pct / (100 + pct))
		}

		conf := "medium"
		if hasPct {
			conf = "high"
		}
		rows = append(rows, HoldingRow{
			RawName:    rawName,
			Shares:     0,
			Amount:     amount,
			Profit:     profit,
			ProfitPct:  pct,
			Confidence: conf,
		})
	}
	if len(rows) > 0 {
		return rows
	}

	// Format C: amount as anchor on its own line
	var anchorIndices []int
	for i, line := range lines {
		if !reBigAmtLine.MatchString(strings.TrimSpace(line)) {
			continue
		}
		v, ok := cleanNum(strings.TrimSpace(line))
		if !ok || v < 500 {
			continue
		}
		for j := max0(i-5); j < i; j++ {
			if isFundName(strings.TrimSpace(lines[j])) {
				anchorIndices = append(anchorIndices, i)
				break
			}
		}
	}

	for k, idx := range anchorIndices {
		amount, _ := cleanNum(lines[idx])

		var nameParts []string
		j := idx - 1
		for j >= max0(idx-6) && len(nameParts) < 2 {
			cand := strings.TrimSpace(lines[j])
			if isFundName(cand) {
				nameParts = append([]string{cand}, nameParts...)
			}
			j--
		}
		if len(nameParts) == 0 {
			continue
		}
		rawName := strings.Join(nameParts, "")

		end := len(lines)
		if k+1 < len(anchorIndices) {
			end = anchorIndices[k+1]
		}

		var explicitPcts []float64
		var signedVals []float64
		for j := idx + 1; j < end; j++ {
			l := strings.TrimSpace(lines[j])
			if pm := reExplicitPct.FindStringSubmatch(l); pm != nil {
				if v, ok := cleanNum(pm[1]); ok {
					explicitPcts = append(explicitPcts, v)
				}
				continue
			}
			if sm := reSignedVal.FindStringSubmatch(l); sm != nil {
				if v, ok := cleanNum(sm[1]); ok {
					signedVals = append(signedVals, v)
				}
			}
		}

		var pct, profit float64
		var hasPct bool
		if len(explicitPcts) > 0 {
			pct = explicitPcts[0]
			hasPct = true
			for _, v := range signedVals {
				if absF(v) < amount {
					profit = v
				}
			}
		} else if len(signedVals) > 0 {
			for _, v := range signedVals {
				if absF(v) < 100 {
					pct = v
					hasPct = true
					break
				}
			}
		}

		if profit == 0 && hasPct && amount > 0 {
			profit = roundTo2(amount * pct / (100 + pct))
		}

		conf := "medium"
		if hasPct {
			conf = "high"
		}
		rows = append(rows, HoldingRow{
			RawName:    rawName,
			Amount:     amount,
			Profit:     profit,
			ProfitPct:  pct,
			Confidence: conf,
		})
	}
	return rows
}

// ─── 交易记录截图解析 ──────────────────────────────────────────────────────────

func ParseTransactionScreenshot(lines []string) []TransactionRow {
	var rows []TransactionRow
	for i := 0; i < len(lines); i++ {
		dm := reDateTx.FindStringSubmatch(lines[i])
		if dm == nil {
			continue
		}
		dateStr := normalizeDate(dm[1])
		direction := dm[2]
		txType := "buy"
		if strings.Contains(direction, "卖出") || strings.Contains(direction, "赎回") {
			txType = "sell"
		}

		rawName := ""
		for j := i + 1; j < i+4 && j < len(lines); j++ {
			cand := strings.TrimSpace(lines[j])
			if cand != "" && reDateTx.FindString(cand) == "" {
				rawName = cand
				break
			}
		}

		var amount, shares, nav float64
		for j := i + 1; j < i+6 && j < len(lines); j++ {
			start := max0(j - 1)
			end := j + 2
			if end > len(lines) {
				end = len(lines)
			}
			combined := strings.Join(lines[start:end], " ")
			m := reTxDetail.FindStringSubmatch(combined)
			if m != nil {
				amount, _ = cleanNum(m[1])
				shares, _ = cleanNum(m[2])
				nav, _ = cleanNum(m[3])
				break
			}
		}

		filled := 0
		for _, v := range []float64{amount, shares, nav} {
			if v != 0 {
				filled++
			}
		}
		conf := "low"
		if filled == 3 {
			conf = "high"
		} else if filled >= 1 {
			conf = "medium"
		}

		rows = append(rows, TransactionRow{
			RawName:    rawName,
			Date:       dateStr,
			TxType:     txType,
			Amount:     amount,
			Shares:     shares,
			Nav:        nav,
			Confidence: conf,
		})
	}
	return rows
}

// ─── 基金名模糊匹配 ────────────────────────────────────────────────────────────

func FuzzyMatchFunds(db *sqlx.DB, rawName string) ([]FundMatch, error) {
	type row struct {
		Code string `db:"code"`
		Name string `db:"name"`
	}
	var funds []row
	if err := db.Select(&funds, `SELECT code, name FROM funds WHERE name != ''`); err != nil {
		return nil, err
	}

	matches := make([]FundMatch, 0, 5)
	for _, f := range funds {
		score := similarityScore(rawName, f.Name)
		matches = append(matches, FundMatch{Code: f.Code, Name: f.Name, Score: score})
	}

	// sort descending
	for i := 0; i < len(matches); i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[j].Score > matches[i].Score {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}

	if len(matches) > 5 {
		matches = matches[:5]
	}
	return matches, nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}

func absF(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func roundTo2(x float64) float64 {
	return float64(int(x*100+0.5)) / 100
}

// similarityScore computes a simple character-overlap ratio (LCS-based approximation)
func similarityScore(a, b string) float64 {
	ra := []rune(a)
	rb := []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	// build character set of a
	setA := make(map[rune]int)
	for _, r := range ra {
		setA[r]++
	}
	common := 0
	for _, r := range rb {
		if setA[r] > 0 {
			common++
			setA[r]--
		}
	}
	return float64(2*common) / float64(len(ra)+len(rb))
}

// IsImage returns true if the file extension or bytes look like an image
func IsImage(filename string, data []byte) bool {
	ext := strings.ToLower(filename)
	if strings.HasSuffix(ext, ".jpg") || strings.HasSuffix(ext, ".jpeg") ||
		strings.HasSuffix(ext, ".png") || strings.HasSuffix(ext, ".webp") {
		return true
	}
	if len(data) >= 4 {
		if data[0] == 0xFF && data[1] == 0xD8 {
			return true // JPEG
		}
		if data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
			return true // PNG
		}
		if bytes.HasPrefix(data, []byte("RIFF")) {
			return true // WebP
		}
	}
	return false
}

