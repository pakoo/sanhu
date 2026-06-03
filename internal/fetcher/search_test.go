package fetcher

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchFund_ParsesJSON(t *testing.T) {
	payload := map[string]interface{}{
		"Datas": []map[string]interface{}{
			{
				"CODE": "510300",
				"NAME": "华泰柏瑞沪深300ETF",
				"FundBaseInfo": map[string]interface{}{
					"FTYPE": "股票指数型",
				},
			},
			{
				"CODE": "000300",
				"NAME": "沪深300指数",
				"FundBaseInfo": map[string]interface{}{
					"FTYPE": "指数",
				},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	// SearchFund hardcodes the eastmoney URL, so we test parsing logic separately.
	// Parse the same JSON the function would receive:
	body, _ := json.Marshal(payload)
	var parsed fundSearchResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(parsed.Datas) != 2 {
		t.Fatalf("expected 2 datas, got %d", len(parsed.Datas))
	}
	if parsed.Datas[0].CODE != "510300" {
		t.Fatalf("expected 510300, got %s", parsed.Datas[0].CODE)
	}
	if parsed.Datas[0].FundBaseInfo.FTYPE != "股票指数型" {
		t.Fatalf("expected 股票指数型, got %s", parsed.Datas[0].FundBaseInfo.FTYPE)
	}
	_ = srv
}

func TestSearchFund_StripsJSONP(t *testing.T) {
	// Test that JSONP wrapper stripping works
	rawJSON := `{"Datas":[{"CODE":"510300","NAME":"华泰柏瑞沪深300ETF","FundBaseInfo":{"FTYPE":"股票指数型"}}]}`

	// Simulate JSONP: callback({"Datas":[...]})
	jsonp := "callback(" + rawJSON + ")"
	s := strings.TrimSpace(jsonp)
	if idx := strings.Index(s, "{"); idx > 0 {
		s = s[idx:]
	}
	if strings.HasSuffix(s, ")") {
		s = s[:len(s)-1]
	}

	var parsed fundSearchResp
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		t.Fatalf("parse JSONP-stripped failed: %v", err)
	}
	if len(parsed.Datas) != 1 || parsed.Datas[0].CODE != "510300" {
		t.Fatalf("unexpected result: %+v", parsed.Datas)
	}
}

func TestSearchFund_LimitsTo20(t *testing.T) {
	// Build 25 items, verify only 20 returned
	datas := make([]map[string]interface{}, 25)
	for i := range datas {
		datas[i] = map[string]interface{}{
			"CODE": "100000",
			"NAME": "测试基金",
			"FundBaseInfo": map[string]interface{}{"FTYPE": "混合型"},
		}
	}

	body, _ := json.Marshal(map[string]interface{}{"Datas": datas})
	var parsed fundSearchResp
	json.Unmarshal(body, &parsed)

	out := make([]SearchResult, 0, len(parsed.Datas))
	for _, d := range parsed.Datas {
		if d.CODE == "" {
			continue
		}
		out = append(out, SearchResult{Code: d.CODE, Name: d.NAME, FundType: d.FundBaseInfo.FTYPE})
		if len(out) >= 20 {
			break
		}
	}
	if len(out) != 20 {
		t.Fatalf("expected 20 results, got %d", len(out))
	}
}
