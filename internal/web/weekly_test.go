// 「我的阅读」页书币余额接口的单测:桩化官方 /pay/balance,覆盖正常返回、
// 上游业务错误与未知账号。
package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/27Aaron/weread-kit/internal/store"
	"github.com/27Aaron/weread-kit/internal/weread"
)

// newBalanceServer 构造桩化官方接口的最小 Server,按请求路径分支返回。
func newBalanceServer(t *testing.T, respond func(path string) *http.Response) *Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Save(db, &store.Credential{Vid: "v1", RefreshToken: "rt", DeviceID: "dev", AccessToken: "tok"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	client := weread.NewClient()
	client.HTTP.Transport = stubTransport(func(r *http.Request) (*http.Response, error) {
		return respond(r.URL.Path), nil
	})
	return &Server{db: db, client: client}
}

// getBalance 直调 handler 发一次余额请求,返回响应记录器。
func getBalance(s *Server, vid string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/accounts/"+vid+"/balance", nil)
	req.SetPathValue("vid", vid)
	rec := httptest.NewRecorder()
	s.handleAccountBalance(rec, req)
	return rec
}

// TestAccountBalance 覆盖余额接口的三条路径:小数解析(上游返回如 1.00,
// 整数解析会直接报错)、上游业务错误、未知账号。
func TestAccountBalance(t *testing.T) {
	s := newBalanceServer(t, func(path string) *http.Response {
		if path != "/pay/balance" {
			t.Fatalf("unexpected path %q", path)
		}
		return stubResp(http.StatusOK, `{"balance":1.00,"giftBalance":1.00}`)
	})

	rec := getBalance(s, "v1")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	var b struct {
		Vid         string  `json:"vid"`
		Balance     float64 `json:"balance"`
		GiftBalance float64 `json:"gift_balance"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("响应不是 JSON: %v (%s)", err, rec.Body.String())
	}
	if b.Vid != "v1" || b.Balance != 1 || b.GiftBalance != 1 {
		t.Fatalf("响应 = %+v, want vid=v1 balance=1 gift_balance=1", b)
	}

	// 上游业务错误(如参数被拒)→ 502。
	s2 := newBalanceServer(t, func(path string) *http.Response {
		return stubResp(http.StatusOK, `{"errCode":-2003,"errMsg":"bad request"}`)
	})
	if rec := getBalance(s2, "v1"); rec.Code != http.StatusBadGateway {
		t.Fatalf("业务错误 code = %d, want 502", rec.Code)
	}

	// 未知账号 → 404,且不触达官方接口。
	if rec := getBalance(s, "ghost"); rec.Code != http.StatusNotFound {
		t.Fatalf("ghost code = %d, want 404", rec.Code)
	}
}
