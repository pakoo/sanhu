package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// 双击启动的两个体验问题，在程序自身解决，不依赖外面的启动脚本：
//
//  1. 双击之后没有任何反应——用户不知道要去浏览器输 localhost:8000。
//     解决：服务起来后自动打开默认浏览器。
//  2. 双击第二次直接崩掉（端口被占）。
//     解决：启动前先探测，若已有本程序在跑，就只打开浏览器然后正常退出。
//
// 设 SANHU_NO_BROWSER=1 可关闭自动开浏览器（服务器 / 无头环境用）。

// alreadyRunning 检查目标端口上是否已经有一个 sanhu 在服务。
// 只认自家的 /api/health，避免把别的程序占用的端口误判成自己。
func alreadyRunning(port int) bool {
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/health", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// portTaken 判断端口是否被占用（不限于本程序）。
//
// 必须用和服务端相同的绑定地址（通配 ":port"）来探测：
// 若探测 127.0.0.1:port，而别的程序占的是 0.0.0.0:port，
// 在 macOS 上这次探测会成功，于是漏判，用户最终看到的还是
// 那句没头没脑的 "bind: address already in use"。
func portTaken(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	ln.Close()
	return false
}

// openBrowserWhenReady 等服务真正能响应了再打开浏览器，
// 否则用户会看到一个「无法连接」的页面，还以为程序坏了。
func openBrowserWhenReady(port int) {
	if v := strings.TrimSpace(os.Getenv("SANHU_NO_BROWSER")); v != "" && v != "0" && v != "false" {
		return
	}
	url := fmt.Sprintf("http://localhost:%d", port)
	for i := 0; i < 60; i++ {
		if alreadyRunning(port) {
			openBrowser(url)
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	log.Printf("服务启动超时，未自动打开浏览器；请手动访问 %s", url)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// 走 rundll32 而不是 `cmd /c start`：后者对含 & 的 URL 会截断，
		// 且会闪一个命令行窗口
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("打开浏览器失败（请手动访问 %s）: %v", url, err)
	}
}
