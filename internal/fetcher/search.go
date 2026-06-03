package fetcher

import (
	"encoding/json"
	"net/url"
	"strings"
)

// SearchResult 基金搜索结果
type SearchResult struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	FundType string `json:"fund_type"`
}

type fundSearchResp struct {
	Datas []struct {
		CODE         string `json:"CODE"`
		NAME         string `json:"NAME"`
		FundBaseInfo struct {
			FTYPE string `json:"FTYPE"`
		} `json:"FundBaseInfo"`
	} `json:"Datas"`
}

// SearchFund 搜索基金，返回最多 20 条
func SearchFund(query string) ([]SearchResult, error) {
	endpoint := "https://fundsuggest.eastmoney.com/FundSearch/api/FundSearchAPI.ashx?m=1&key=" +
		url.QueryEscape(query)

	body, err := get(endpoint)
	if err != nil {
		return nil, err
	}

	// 接口有时返回 JSONP 格式，去掉包装
	s := strings.TrimSpace(string(body))
	if idx := strings.Index(s, "{"); idx > 0 {
		s = s[idx:]
	}
	if strings.HasSuffix(s, ")") {
		s = s[:len(s)-1]
	}

	var parsed fundSearchResp
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return nil, err
	}

	out := make([]SearchResult, 0, len(parsed.Datas))
	for _, d := range parsed.Datas {
		if d.CODE == "" {
			continue
		}
		out = append(out, SearchResult{
			Code:     d.CODE,
			Name:     d.NAME,
			FundType: d.FundBaseInfo.FTYPE,
		})
		if len(out) >= 20 {
			break
		}
	}
	return out, nil
}
