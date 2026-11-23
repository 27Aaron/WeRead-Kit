package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/27Aaron/weread-kit/internal/store"
)

// TestSessionToken 验证签名 cookie 的签发与校验:有效放行,篡改/过期/垃圾值拒绝。
func TestSessionToken(t *testing.T) {
	s := &Server{sessionKey: []byte("test-key")}
	valid := s.sessionToken(time.Now().Add(time.Hour).Unix())
	if !s.validSession(valid) {
		t.Fatal("valid token rejected")
	}
	if s.validSession(valid + "ff") {
		t.Fatal("tampered token accepted")
	}
	if s.validSession(s.sessionToken(time.Now().Add(-time.Minute).Unix())) {
		t.Fatal("expired token accepted")
	}
	if s.validSession("not-a-token") {
		t.Fatal("garbage token accepted")
	}
}

func TestSessionKeyPersistsAcrossRestart(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	s1 := New(db)
	s2 := New(db)
	token := s1.sessionToken(time.Now().Add(time.Hour).Unix())
	if !s2.validSession(token) {
		t.Fatal("token issued before restart rejected after restart")
	}
}

// TestAuthMiddleware 验证鉴权中间件:页面 302、API 401、带有效 cookie 放行、未配置凭据全放行。
func TestAuthMiddleware(t *testing.T) {
	s := &Server{username: "u", password: "p", sessionKey: []byte("k")}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := s.auth(next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("page without cookie: want 302, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("api without cookie: want 401, got %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "weread_session", Value: s.sessionToken(time.Now().Add(time.Hour).Unix())})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with valid cookie: want 200, got %d", rec.Code)
	}

	open := &Server{sessionKey: []byte("k")}
	rec = httptest.NewRecorder()
	open.auth(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("no credentials configured: want bypass 200, got %d", rec.Code)
	}
}

// TestHandleLoginPost 验证正确凭据发签名 cookie 跳首页,错误凭据回登录页。
func TestHandleLoginPost(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	s := &Server{username: "u", password: "p", sessionKey: []byte("k"), db: db}

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
		req.Header.Set("content-type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		s.handleLoginPost(rec, req)
		return rec
	}

	rec := post("username=u&password=p")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("valid login: want 303 -> /, got %d -> %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "weread_session" || cookies[0].Value == "" || cookies[0].Value == "ok" {
		t.Fatalf("valid login: unexpected cookie %+v", cookies)
	}

	rec = post("username=u&password=wrong")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?error=1" {
		t.Fatalf("bad login: want 303 -> /login?error=1, got %d -> %q", rec.Code, rec.Header().Get("Location"))
	}
}

// TestLoginBackoffStaysCapped 连续失败很久也不能把锁定期算成 0、负数或超过 1 分钟。
// 旧实现用 1<<n * time.Second,溢出后封顶比较失效,脚本可以继续速刷。
func TestLoginBackoffStaysCapped(t *testing.T) {
	s := &Server{}
	for i := 0; i < 80; i++ {
		s.loginMarkResult(false)
		if i < 2 {
			if s.loginLocked() {
				t.Fatalf("after %d failures: locked too early", i+1)
			}
			continue
		}
		if !s.loginLocked() {
			t.Fatalf("after %d failures: lock inactive", i+1)
		}
		s.loginMu.Lock()
		until := s.loginLockUntil
		s.loginMu.Unlock()
		if d := time.Until(until); d <= 0 || d > maxLoginBackoff+time.Second {
			t.Fatalf("after %d failures: backoff %s outside (0, %s]", i+1, d, maxLoginBackoff)
		}
	}
	s.loginMarkResult(true)
	if s.loginLocked() {
		t.Fatal("successful login did not clear the lock")
	}
}

func TestSessionInvalidatedByCredentialChange(t *testing.T) {
	before := &Server{username: "u", password: "p", sessionKey: []byte("k")}
	token := before.sessionToken(time.Now().Add(time.Hour).Unix())
	if !before.validSession(token) {
		t.Fatal("token rejected by issuing server")
	}
	for _, after := range []*Server{
		{username: "u", password: "p2", sessionKey: []byte("k")},
		{username: "u2", password: "p", sessionKey: []byte("k")},
	} {
		if after.validSession(token) {
			t.Fatalf("token still valid after credential change to %q/%q", after.username, after.password)
		}
	}
}
