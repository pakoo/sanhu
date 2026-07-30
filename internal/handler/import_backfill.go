package handler

import (
	"fmt"
	"log"

	"github.com/gin-gonic/gin"
	"strings"
	"sync"
	"time"

	"github.com/pakoo/sanhu/internal/fetcher"
)

// 录入后的后台补数据。
//
// 为什么单独拆出来：提交路径过去在请求线程里同步抓档案 + 净值，
// 最坏 141 秒/只且串行，而前端 fetch 没有超时 —— 用户看到的就是
// 按钮永远停在「提交中…」。现在提交只写库，抓取挪到这里。
//
// 这里刻意把**每只基金的失败原因**留下来（而不是只 log），
// 因为用户报障时能复制走的那段错误信息就靠它。

// importItemStatus 一只基金的补数据结果
type importItemStatus struct {
	Code    string  `json:"code"`
	Name    string  `json:"name"`
	Stage   string  `json:"stage"`  // profile / nav / done
	Status  string  `json:"status"` // pending / ok / failed
	Detail  string  `json:"detail"` // 失败原因原文，报障时要看的就是这个
	NavDate string  `json:"nav_date"`
	Nav     float64 `json:"nav"`
}

type importBackfillState struct {
	mu        sync.Mutex
	Running   bool
	StartedAt string
	EndedAt   string
	Items     []importItemStatus
}

var importBackfill importBackfillState

// startImportBackfill 起一个后台任务补档案与净值。
// 已有任务在跑时把新代码并进去，不另起一个 —— 单连接数据库经不起并发写。
func (h *Handler) startImportBackfill(codes []string) {
	importBackfill.mu.Lock()
	alreadyRunning := importBackfill.Running
	if !alreadyRunning {
		importBackfill.Items = nil
		importBackfill.StartedAt = time.Now().Format("2006-01-02 15:04:05")
		importBackfill.EndedAt = ""
		importBackfill.Running = true
	}
	existing := map[string]bool{}
	for _, it := range importBackfill.Items {
		existing[it.Code] = true
	}
	for _, c := range codes {
		if existing[c] {
			continue
		}
		importBackfill.Items = append(importBackfill.Items, importItemStatus{
			Code: c, Status: "pending", Stage: "profile",
		})
	}
	importBackfill.mu.Unlock()

	if alreadyRunning {
		return // 已有 goroutine 会捡走新加进来的条目
	}
	go h.runImportBackfill()
}

func (h *Handler) runImportBackfill() {
	defer func() {
		importBackfill.mu.Lock()
		importBackfill.Running = false
		importBackfill.EndedAt = time.Now().Format("2006-01-02 15:04:05")
		importBackfill.mu.Unlock()
	}()

	for {
		// 每轮取一个还没处理的，这样跑到一半又提交的基金也能被捡走
		idx := -1
		importBackfill.mu.Lock()
		for i, it := range importBackfill.Items {
			if it.Status == "pending" {
				idx = i
				break
			}
		}
		var code string
		if idx >= 0 {
			code = importBackfill.Items[idx].Code
		}
		importBackfill.mu.Unlock()
		if idx < 0 {
			return
		}

		name, stage, detail := h.backfillOne(code)

		importBackfill.mu.Lock()
		it := &importBackfill.Items[idx]
		it.Name = name
		it.Stage = stage
		if detail == "" {
			it.Status = "ok"
		} else {
			it.Status = "failed"
			it.Detail = detail
		}
		it.Nav, it.NavDate = h.latestNavWithDate(code)
		importBackfill.mu.Unlock()
	}
}

// backfillOne 补一只基金的档案与净值。
// 返回 (基金名, 失败发生在哪一步, 失败原因)；三者中 detail 为空表示成功。
func (h *Handler) backfillOne(code string) (name, stage, detail string) {
	stage = "profile"

	// 档案：只有还没有 fund_type 时才抓。空壳基金（只有 code+name）拿不到
	// 分类、评分、风险和穿透，所以这一步值得做。
	var known int
	_ = h.DB.Get(&known, `SELECT COUNT(*) FROM funds WHERE code=? AND COALESCE(fund_type,'')!=''`, code)
	if known == 0 {
		if err := fetcher.FetchFundInfo(h.DB, code); err != nil {
			log.Printf("[import] 抓取 %s 档案失败: %v", code, err)
			detail = "抓取基金档案失败：" + err.Error()
		}
	}
	_ = h.DB.Get(&name, `SELECT COALESCE(name,'') FROM funds WHERE code=?`, code)

	// 净值：抓到才能把用户填的金额换算成份额
	stage = "nav"
	if nav, _ := h.latestNavWithDate(code); nav <= 0 {
		if err := fetcher.FetchNav(h.DB, code, 1095); err != nil {
			log.Printf("[import] 抓取 %s 净值失败: %v", code, err)
			navErr := "抓取净值失败：" + err.Error()
			if detail == "" {
				detail = navErr
			} else {
				detail += "；" + navErr
			}
		}
	}

	// 把份额补上：提交时没净值只写了 entered_value，现在能算了
	if err := h.recomputeShares(code); err != nil {
		log.Printf("[import] 回算 %s 份额失败: %v", code, err)
	}

	// 抓完还是没净值 —— 这一步必须报出来。
	// 东财对不存在的基金代码不会返回 HTTP 错误，而是返回一个空列表，
	// 于是上面两次抓取都「成功」了，结果却什么都没拿到。不判这一下的话，
	// 用户敲错代码时会看到「一切正常」，但市值永远不涨不跌，无从排查。
	if nav, _ := h.latestNavWithDate(code); nav <= 0 && detail == "" {
		detail = "数据源没有返回这只基金的净值 —— 多半是基金代码不存在或已清盘，" +
			"请核对代码；确认无误的话可能是数据源暂时不可用，稍后重试"
	}

	if detail == "" {
		stage = "done"
	}
	return name, stage, detail
}

// recomputeShares 用最新净值把 entered_value 换算成份额。
// 提交时净值还没抓到，市值只能靠 entered_value 兜底显示；抓到之后
// 补上份额，涨跌才会跟着净值动。
func (h *Handler) recomputeShares(code string) error {
	nav, _ := h.latestNavWithDate(code)
	if nav <= 0 {
		return nil
	}
	var entered float64
	if err := h.DB.Get(&entered, `SELECT COALESCE(entered_value,0) FROM holdings WHERE code=?`, code); err != nil {
		return err
	}
	if entered <= 0 {
		return nil
	}
	var shares float64
	_ = h.DB.Get(&shares, `SELECT COALESCE(shares,0) FROM holdings WHERE code=?`, code)
	if shares > 0 {
		return nil // 用户自己填了份额，不覆盖
	}
	_, err := h.DB.Exec(`UPDATE holdings SET shares=? WHERE code=?`, entered/nav, code)
	return err
}

func (h *Handler) latestNavWithDate(code string) (float64, string) {
	var row struct {
		Nav  float64 `db:"nav"`
		Date string  `db:"date"`
	}
	_ = h.DB.Get(&row, `SELECT nav, date FROM nav_history WHERE code=? AND nav>0 ORDER BY date DESC LIMIT 1`, code)
	return row.Nav, row.Date
}

// GetImportStatus 前端轮询这个接口看补数据进度；失败明细也在这里，
// 用来拼「可复制的报错信息」。
func (h *Handler) GetImportStatus(c *gin.Context) {
	importBackfill.mu.Lock()
	items := make([]importItemStatus, len(importBackfill.Items))
	copy(items, importBackfill.Items)
	running := importBackfill.Running
	startedAt, endedAt := importBackfill.StartedAt, importBackfill.EndedAt
	importBackfill.mu.Unlock()

	okCount, failCount, pendingCount := 0, 0, 0
	var failures []string
	for _, it := range items {
		switch it.Status {
		case "ok":
			okCount++
		case "failed":
			failCount++
			label := it.Code
			if it.Name != "" {
				label = fmt.Sprintf("%s %s", it.Code, it.Name)
			}
			failures = append(failures, fmt.Sprintf("%s —— %s", label, it.Detail))
		default:
			pendingCount++
		}
	}

	ok(c, gin.H{
		"running": running, "started_at": startedAt, "ended_at": endedAt,
		"items": items, "ok": okCount, "failed": failCount, "pending": pendingCount,
		"failure_text": strings.Join(failures, "\n"),
	})
}
