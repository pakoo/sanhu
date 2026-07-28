package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	sanhugo "github.com/pakoo/sanhu"
	"github.com/pakoo/sanhu/internal/bootstrap"
	"github.com/pakoo/sanhu/internal/config"
	"github.com/pakoo/sanhu/internal/db"
	"github.com/pakoo/sanhu/internal/handler"
	"github.com/pakoo/sanhu/internal/scheduler"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// 双击第二次时不要崩：已有实例在跑就把浏览器带过去，然后正常退出。
	// 端口被别的程序占用则明确报错，别让用户对着 "bind: address already in use" 猜。
	if alreadyRunning(cfg.Port) {
		log.Printf("sanhu 已在 :%d 运行，直接打开浏览器", cfg.Port)
		openBrowser(fmt.Sprintf("http://localhost:%d", cfg.Port))
		return
	}
	if portTaken(cfg.Port) {
		log.Fatalf("端口 %d 已被其他程序占用；换个端口启动：SANHU_PORT=8123", cfg.Port)
	}

	database, err := db.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer database.Close()

	if err := db.RunMigrations(database, sanhugo.MigrationsFS); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	// 首启空库时装载初始参考数据（默认解压内嵌数据，不联网）。
	// 失败非致命：空库仍可启动，下次启动自动重试。
	if err := bootstrap.EnsureSeed(context.Background(), database, sanhugo.SeedSQLGz, cfg.SeedURL, log.Printf); err != nil {
		log.Printf("warning: 初始数据装载失败（将空库启动，下次启动重试）: %v", err)
	}

	// strip "static" prefix so /static/css/style.css resolves correctly
	staticFS, err := fs.Sub(sanhugo.StaticFS, "static")
	if err != nil {
		log.Fatalf("open static fs: %v", err)
	}

	// 启动定时任务
	cronJob := scheduler.Start(database)
	defer cronJob.Stop()

	router := newRouter(staticFS, cfg.Debug, database, cfg.DataDir)
	addr := fmt.Sprintf(":%d", cfg.Port)
	// 用 ldflags 注入的 version（CI 打的是 -X main.version），
	// cfg.Version 来自 config 包的同名变量，CI 并没有注入它，日志会永远显示 dev
	log.Printf("sanhu %s listening on %s (data: %s)", version, addr, cfg.DataDir)

	// 双击启动的用户不会自己去浏览器敲地址，等服务就绪后替他打开
	go openBrowserWhenReady(cfg.Port)

	if err := router.Run(addr); err != nil {
		log.Fatalf("run server: %v", err)
	}
}

func newRouter(staticFS fs.FS, debug bool, database *sqlx.DB, dataDir string) *gin.Engine {
	if !debug {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())

	// 前端入口
	r.GET("/", func(c *gin.Context) {
		content, err := fs.ReadFile(staticFS, "index.html")
		if err != nil {
			c.String(http.StatusInternalServerError, "failed to load index.html")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", content)
	})

	// 静态资源（js/css/images）
	r.StaticFS("/static", http.FS(staticFS))

	// API 路由组（后续各 handler 在此注册）
	api := r.Group("/api")
	api.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	h := handler.NewWithVersion(database, version, gitTag, dataDir)
	handler.RegisterRoutes(r, h)

	// 未匹配的路由：/api/* 返回 501，其他返回 404
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotImplemented, gin.H{
				"status": "not implemented",
				"path":   c.Request.URL.Path,
			})
		} else {
			c.JSON(http.StatusNotFound, gin.H{"status": "not found"})
		}
	})

	return r
}
