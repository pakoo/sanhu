package handler

import (
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
)

type Handler struct {
	DB      *sqlx.DB
	Version string // 由 main.go 通过 ldflags 注入，e.g. "v1.0.0"
	GitTag  string
}

func New(db *sqlx.DB) *Handler { return &Handler{DB: db} }

// NewWithVersion 携带版本信息创建 Handler（生产用）
func NewWithVersion(db *sqlx.DB, version, gitTag string) *Handler {
	return &Handler{DB: db, Version: version, GitTag: gitTag}
}

type taskState struct {
	mu            sync.Mutex
	Running       bool     `json:"running"`
	Done          bool     `json:"done"`
	Logs          []string `json:"logs"`
	DoneCount     int      `json:"done_count"`
	Total         int      `json:"total"`
	Errors        []string `json:"errors"`
	Recent        []string `json:"recent"`
	LastRefreshAt string   `json:"last_refresh_at"`
}

var (
	navRefreshTask      taskState
	holdingsRefreshTask taskState
)

func ok(c *gin.Context, data interface{}) {
	c.JSON(200, gin.H{"status": "ok", "data": data})
}

func fail(c *gin.Context, code int, msg string) {
	c.JSON(code, gin.H{"status": "error", "msg": msg})
}

func allFundCodes(db *sqlx.DB) []string {
	var codes []string
	_ = db.Select(&codes, `SELECT DISTINCT code FROM (SELECT code FROM holdings UNION SELECT code FROM watchlist)`)
	return codes
}
