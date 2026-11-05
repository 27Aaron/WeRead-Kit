package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newPassthroughServer 构造直调 handler 的最小 Server,官方接口统一返回 body。
func newPassthroughServer(t *testing.T, body string) *Server {
	return newChallengeServer(t, func(r *http.Request) (*http.Response, error) {
		return stubResp(http.StatusOK, body), nil
	})
}

func TestMedalListHandler(t *testing.T) {
	s := newPassthroughServer(t, `{"available":[{"id":"M1","name":"连续阅读"}],"totalCount":1}`)
	get := func(vid string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/accounts/"+vid+"/medals", nil)
		req.SetPathValue("vid", vid)
		rec := httptest.NewRecorder()
		s.handleMedalList(rec, req)
		return rec
	}
	if get("ghost").Code != http.StatusNotFound {
		t.Fatal("未知账号: want 404")
	}
	rec := get("v1")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "连续阅读") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestChallengeHistoryHandler(t *testing.T) {
	s := newPassthroughServer(t, `{"challenges":[{"id":"ChallengeLongTermFree","status":2}],"totalCount":1}`)
	req := httptest.NewRequest(http.MethodGet, "/api/accounts/v1/challenge/history", nil)
	req.SetPathValue("vid", "v1")
	rec := httptest.NewRecorder()
	s.handleChallengeHistory(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ChallengeLongTermFree") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = io.Discard
}
