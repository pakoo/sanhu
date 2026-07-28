package handler

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/fetcher"
	"github.com/pakoo/sanhu/internal/importer"
	"github.com/pakoo/sanhu/internal/portfolio"
)

// latestNavForImport 取一只基金的最新净值，没有则返回 0
func (h *Handler) latestNavForImport(code string) float64 {
	var nav float64
	_ = h.DB.Get(&nav, `SELECT nav FROM nav_history WHERE code=? AND nav>0 ORDER BY date DESC LIMIT 1`, code)
	return nav
}

func (h *Handler) ImportParse(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	importType := c.PostForm("import_type")
	if importType != "holdings" && importType != "transaction" {
		fail(c, http.StatusBadRequest, "only holdings or transaction supported")
		return
	}

	fh, err := file.Open()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	defer fh.Close()

	data, err := io.ReadAll(fh)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	// ── 图片走 OCR 路径 ──────────────────────────────────────────────────────
	if importer.IsImage(file.Filename, data) {
		settings, _ := getSettingsCompat(h.DB)
		apiKey := settings["ocrspace_api_key"]

		lines, err := importer.OCRImage(apiKey, data, file.Filename)
		if err != nil {
			fail(c, http.StatusInternalServerError, "OCR 解析失败："+err.Error())
			return
		}
		if len(lines) == 0 {
			ok(c, gin.H{"import_type": importType, "rows": []interface{}{}})
			return
		}

		if importType == "holdings" {
			parsed := importer.ParseHoldingsScreenshot(lines)
			// 模糊匹配基金
			for i := range parsed {
				matches, _ := importer.FuzzyMatchFunds(h.DB, parsed[i].RawName)
				parsed[i].Candidates = matches
				if len(matches) > 0 {
					parsed[i].Code = matches[0].Code
					parsed[i].Name = matches[0].Name
				}
			}
			ok(c, gin.H{"import_type": importType, "rows": parsed})
		} else {
			parsed := importer.ParseTransactionScreenshot(lines)
			for i := range parsed {
				matches, _ := importer.FuzzyMatchFunds(h.DB, parsed[i].RawName)
				parsed[i].Candidates = matches
				if len(matches) > 0 {
					parsed[i].Code = matches[0].Code
					parsed[i].Name = matches[0].Name
				}
			}
			ok(c, gin.H{"import_type": importType, "rows": parsed})
		}
		return
	}

	// ── CSV 路径 ─────────────────────────────────────────────────────────────
	if !strings.HasSuffix(strings.ToLower(file.Filename), ".csv") {
		fail(c, http.StatusBadRequest, "仅支持图片（JPG/PNG/WebP）或 CSV 文件")
		return
	}

	reader := csv.NewReader(strings.NewReader(string(data)))
	header, err := reader.Read()
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	var rows []map[string]string
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		item := make(map[string]string, len(header))
		for i, key := range header {
			if i < len(record) {
				item[key] = record[i]
			}
		}
		rows = append(rows, item)
	}

	ok(c, gin.H{"import_type": importType, "rows": rows})
}

func (h *Handler) ImportCommit(c *gin.Context) {
	var req struct {
		ImportType string                   `json:"import_type"`
		Rows       []map[string]interface{} `json:"rows"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	inserted, updated := 0, 0
	switch req.ImportType {
	case "holdings":
		for _, row := range req.Rows {
			code := fmt.Sprint(row["code"])
			if code == "" {
				continue
			}
			shares := parseFloat64(row["shares"])
			// frontend manual-add sends current_value; OCR/CSV parse sends amount
			amount := parseFloat64(row["amount"])
			if amount == 0 {
				amount = parseFloat64(row["current_value"])
			}
			profit := parseFloat64(row["profit"])

			// 从持有金额反推份额和成本
			costAmount := parseFloat64(row["cost_amount"])
			if costAmount == 0 && amount > 0 {
				costAmount = amount - profit
			}
			// 顺序要紧：先保证 funds 里有这只基金，再抓净值。
			// nav_history 对 funds(code) 有外键，反过来做的话净值写入会被外键直接拒掉。
			//
			// 先试着抓真实档案：只塞 code+name 会得到一只没有 fund_type / category 的空壳基金，
			// 分类、评分、风险、持仓穿透全部拿它没办法。
			var known int
			_ = h.DB.Get(&known, `SELECT COUNT(*) FROM funds WHERE code=? AND COALESCE(fund_type,'')!=''`, code)
			if known == 0 {
				if err := fetcher.FetchFundInfo(h.DB, code); err != nil {
					log.Printf("[import] 抓取 %s 档案失败: %v", code, err)
				}
			}
			fundName := fmt.Sprint(row["name"])
			if fundName == "" {
				fundName = code
			}
			_, _ = h.DB.Exec(`INSERT OR IGNORE INTO funds (code, name) VALUES (?,?)`, code, fundName)

			if shares == 0 && amount > 0 {
				nav := h.latestNavForImport(code)
				if nav == 0 {
					// 库里没有净值 —— 内置数据只有 11 只基金，用户录入的基金多半不在里面。
					// 不补这一刀的话 shares 落 0，市值 = 净值 × 份额 = 0，用户填的钱就没了。
					if err := fetcher.FetchNav(h.DB, code, 1095); err != nil {
						log.Printf("[import] 抓取 %s 净值失败（将按「净值未更新」显示）: %v", code, err)
					}
					nav = h.latestNavForImport(code)
				}
				if nav > 0 {
					shares = amount / nav
				}
				// 仍然抓不到时保留 shares=0，由 entered_value 兜底显示，见 migrations/002
			}

			var cnt int
			if err := h.DB.Get(&cnt, `SELECT COUNT(*) FROM holdings WHERE code=?`, code); err != nil {
				fail(c, http.StatusInternalServerError, err.Error())
				return
			}

			// 存下用户填的持有金额原值：净值抓不到时用它显示市值，而不是显示 0
			var enteredValue interface{}
			if amount > 0 {
				enteredValue = amount
			}

			var execErr error
			if cnt > 0 {
				_, execErr = h.DB.Exec(`UPDATE holdings SET shares=?, cost_amount=?, entered_value=? WHERE code=?`,
					shares, costAmount, enteredValue, code)
				updated++
			} else {
				_, execErr = h.DB.Exec(`INSERT INTO holdings (code, shares, cost_amount, entered_value, buy_date) VALUES (?,?,?,?,date('now'))`,
					code, shares, costAmount, enteredValue)
				inserted++
			}
			if execErr != nil {
				fail(c, http.StatusInternalServerError, execErr.Error())
				return
			}

			// 自动加入关注列表
			_, _ = h.DB.Exec(`INSERT OR IGNORE INTO watchlist (code) VALUES (?)`, code)
		}
	case "transaction":
		for _, row := range req.Rows {
			if err := portfolio.RecordTransaction(
				h.DB,
				fmt.Sprint(row["code"]),
				fmt.Sprint(row["tx_type"]),
				fmt.Sprint(row["date"]),
				parseFloat64(row["amount"]),
				parseFloat64(row["nav"]),
				parseFloat64(row["fee"]),
				fmt.Sprint(row["notes"]),
			); err != nil {
				fail(c, http.StatusInternalServerError, err.Error())
				return
			}
			inserted++
		}
	default:
		fail(c, http.StatusBadRequest, "unsupported import_type")
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "inserted": inserted, "updated": updated})
}
