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
	var pending []string // 需要后台补档案/净值的基金
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
			// 这里**不抓网**。
			//
			// 曾经是同步抓的：档案 2 个 URL + 净值 1 个 URL，每个 URL 在
			// fetcher.get() 里重试 3 次 × 15 秒超时 + 2 次 1 秒间隔 = 最坏 47 秒，
			// 单只基金最坏 141 秒，多只还是串行 —— 而前端 fetch 没有超时，
			// 于是按钮就永远停在「提交中…」。用户报的「卡住」就是这个。
			//
			// 现在提交只写库：entered_value 会兜住显示（见 migrations/002），
			// 净值和档案交给下面的后台任务补，失败原因记进 importFetchTask
			// 供前端生成可复制的报错信息。
			fundName := fmt.Sprint(row["name"])
			if fundName == "" {
				fundName = code
			}
			_, _ = h.DB.Exec(`INSERT OR IGNORE INTO funds (code, name) VALUES (?,?)`, code, fundName)

			if shares == 0 && amount > 0 {
				if nav := h.latestNavForImport(code); nav > 0 {
					shares = amount / nav
				}
				// 库里没净值就保留 shares=0，由 entered_value 兜底显示，
				// 后台任务抓到净值后会把份额补上（见 backfillImported）
			}
			pending = append(pending, code)

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

	// 抓取全部挪到后台：提交本身只写库，永远是毫秒级返回。
	// 前端拿 pending 去轮询 /api/import/status，失败明细在那里。
	if len(pending) > 0 {
		h.startImportBackfill(pending)
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "ok", "inserted": inserted, "updated": updated,
		"pending": pending,
	})
}
