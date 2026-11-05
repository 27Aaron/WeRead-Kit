package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReadingStatsAggregation 三项数据独立拉取,单项失败不影响其余。
func TestReadingStatsAggregation(t *testing.T) {
	s := newChallengeServer(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/read/milestone":
			return stubResp(http.StatusOK, `{"shareInfo":{"wholeHours":54,"seqDay":22}}`), nil
		case "/pay/balance":
			return stubResp(http.StatusOK, `{"balance":0,"giftBalance":2}`), nil
		case "/readdata/summary":
			return stubResp(http.StatusInternalServerError, `{"errcode":-1}`), nil
		}
		return stubResp(http.StatusNotFound, `{}`), nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/accounts/v1/stats", nil)
	req.SetPathValue("vid", "v1")
	rec := httptest.NewRecorder()
	s.handleReadingStats(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"wholeHours":54`, `"giftBalance":2`, `"summary":null`} {
		if !strings.Contains(body, want) {
			t.Fatalf("响应缺少 %s: %s", want, body)
		}
	}
}
