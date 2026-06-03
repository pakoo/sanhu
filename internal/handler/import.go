package handler

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pakoo/sanhu/internal/importer"
	"github.com/pakoo/sanhu/internal/portfolio"
)

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
			if shares == 0 && amount > 0 {
				var nav float64
				_ = h.DB.Get(&nav, `SELECT nav FROM nav_history WHERE code=? AND nav>0 ORDER BY date DESC LIMIT 1`, code)
				if nav > 0 {
					shares = amount / nav
				}
				// even if NAV is missing, record with shares=0 so holding appears in list
			}

			// ensure fund record exists (holdings has FK → funds)
			fundName := fmt.Sprint(row["name"])
			if fundName == "" {
				fundName = code
			}
			_, _ = h.DB.Exec(`INSERT OR IGNORE INTO funds (code, name) VALUES (?,?)`, code, fundName)

			var cnt int
			if err := h.DB.Get(&cnt, `SELECT COUNT(*) FROM holdings WHERE code=?`, code); err != nil {
				fail(c, http.StatusInternalServerError, err.Error())
				return
			}

			var execErr error
			if cnt > 0 {
				_, execErr = h.DB.Exec(`UPDATE holdings SET shares=?, cost_amount=? WHERE code=?`, shares, costAmount, code)
				updated++
			} else {
				_, execErr = h.DB.Exec(`INSERT INTO holdings (code, shares, cost_amount, buy_date) VALUES (?,?,?,date('now'))`, code, shares, costAmount)
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
