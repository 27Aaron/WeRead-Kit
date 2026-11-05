package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/27Aaron/weread-kit/internal/store"
	"github.com/27Aaron/weread-kit/internal/weread"
)

// stubTransport 让 web 层测试能把官方接口桩在 RoundTripper 上,
// 模式与 internal/weread 的单测一致。
type stubTransport func(*http.Request) (*http.Response, error)

func (f stubTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func stubResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// newChallengeServer 构造直调 handler 的最小 Server:临时库 + 桩化的官方接口。
func newChallengeServer(t *testing.T, transport stubTransport) *Server {
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
	client.HTTP.Transport = transport
	return &Server{db: db, client: client}
}

func postJoin(s *Server, vid, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+vid+"/challenge/join", strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.SetPathValue("vid", vid)
	rec := httptest.NewRecorder()
	s.handleChallengeJoin(rec, req)
	return rec
}

// TestChallengeJoinValidation 覆盖请求体非 JSON 与白名单外的 challenge_id。
func TestChallengeJoinValidation(t *testing.T) {
	s := newChallengeServer(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("校验失败的请求不应触达官方接口")
		return nil, nil
	})
	if rec := postJoin(s, "v1", `not-json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("非 JSON body: status = %d, want 400", rec.Code)
	}
	if rec := postJoin(s, "v1", `{"challenge_id":"readchallenge4"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("付费挑战 ID: status = %d, want 400", rec.Code)
	}
}

// TestChallengeJoinUnknownAccount 未知账号返回 404。
func TestChallengeJoinUnknownAccount(t *testing.T) {
	s := newChallengeServer(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("未知账号不应触达官方接口")
		return nil, nil
	})
	if rec := postJoin(s, "ghost", `{"challenge_id":"ChallengeLongTermFree"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("未知账号: status = %d, want 404", rec.Code)
	}
}

// TestChallengeJoinAlreadyJoined 映射为 409,前端据此提示"已参与"。
func TestChallengeJoinAlreadyJoined(t *testing.T) {
	s := newChallengeServer(t, func(r *http.Request) (*http.Response, error) {
		return stubResp(http.StatusOK, `{"errcode":-2904,"errmsg":"-2904"}`), nil
	})
	if rec := postJoin(s, "v1", `{"challenge_id":"ChallengeLongTermFree"}`); rec.Code != http.StatusConflict {
		t.Fatalf("已参与: status = %d, want 409", rec.Code)
	}
}

// TestChallengeJoinSuccess 报名成功返回 200 并落一条审计日志。
func TestChallengeJoinSuccess(t *testing.T) {
	s := newChallengeServer(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/challenge/join" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		return stubResp(http.StatusOK, `{"succ":1}`), nil
	})
	rec := postJoin(s, "v1", `{"challenge_id":"ChallengeLongTermFree"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var msg string
	if err := s.db.QueryRow(`SELECT message FROM weread_log WHERE vid = 'v1' ORDER BY id DESC LIMIT 1`).Scan(&msg); err != nil {
		t.Fatalf("查询审计日志: %v", err)
	}
	if !strings.Contains(msg, "21 天阅读挑战") {
		t.Fatalf("审计日志 = %q, want 报名记录", msg)
	}
}
