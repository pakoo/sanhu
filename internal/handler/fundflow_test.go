package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
)

// 校验入参把关：bk 会被拼进东财请求串，不校验等于把拼接权交给调用方。
func TestSectorFlowRejectsBadParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	r := gin.New()
	r.GET("/api/fundflow/minute", h.GetSectorFlowMinute)
	r.GET("/api/fundflow/daily", h.GetSectorFlowDaily)
	r.GET("/api/fundflow/rank", h.GetSectorFlowRank)

	bad := []string{"", "BK12", "bk1215", "BK1215'", "../../etc", "BK1215&x=1", "BK1215 "}
	for _, bk := range bad {
		for _, ep := range []string{"minute", "daily"} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/fundflow/"+ep+"?bk="+url.QueryEscape(bk), nil))
			if w.Code != http.StatusBadRequest {
				t.Errorf("%s?bk=%q 应被拒(400)，得到 %d", ep, bk, w.Code)
			}
		}
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/fundflow/rank?kind=region", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("地域板块是故意不支持的，应拒绝，得到 %d", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "error" {
		t.Errorf("错误响应应带 status=error，得到 %v", body)
	}
}
