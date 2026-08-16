package fetcher

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// get 发起 HTTP GET，失败自动重试 2 次，间隔 1 秒
func get(url string) ([]byte, error) {
	var lastErr error
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Second)
		}
		b, err := doGet(url)
		if err == nil {
			return b, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// getWithReferer 与 get 相同，但可指定 Referer。
// 东财不同子站对来源要求不一样：行情接口 push2 只认 quote 域，
// 用基金站的 Referer 去请求会拿到空响应。
func getWithReferer(url, referer string) ([]byte, error) {
	var lastErr error
	for i := 0; i < 3; i++ {
		if i > 0 {
			// 指数退避（1s、2s）而不是固定 1 秒：失败最常见的原因是被限流，
			// 而限流场景下等距重试只会加重限流（见 holdings_fetcher.go:109 的记录）。
			time.Sleep(time.Duration(1<<(i-1)) * time.Second)
		}
		b, err := doGetWithReferer(url, referer)
		if err == nil {
			return b, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// getNoRetry 只打一次，失败立刻返回。
//
// 给限流敏感的端点用（板块资金流的 clist/get 是重灾区）。这类接口宁可这次没数据、
// 让上层降级到缓存，也不要多打一次把封锁期拉长 —— 反正下一个调度周期还会再来。
func getNoRetry(url, referer string) ([]byte, error) {
	return doGetWithReferer(url, referer)
}

func doGet(url string) ([]byte, error) {
	return doGetWithReferer(url, "https://fundf10.eastmoney.com/")
}

func doGetWithReferer(url, referer string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", referer)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// workerPool 用 n 个 worker 并发处理 codes 列表，每次请求间隔 interval
func workerPool[T any](codes []string, n int, interval time.Duration, fn func(code string) (T, error)) []error {
	jobs := make(chan string, len(codes))
	errs := make(chan error, len(codes))
	for _, c := range codes {
		jobs <- c
	}
	close(jobs)

	for w := 0; w < n; w++ {
		go func() {
			for code := range jobs {
				if _, err := fn(code); err != nil {
					errs <- fmt.Errorf("%s: %w", code, err)
				} else {
					errs <- nil
				}
				time.Sleep(interval)
			}
		}()
	}

	var out []error
	for range codes {
		if err := <-errs; err != nil {
			out = append(out, err)
		}
	}
	return out
}
