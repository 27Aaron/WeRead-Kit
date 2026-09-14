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

// TestSessionKeyPersistsAcrossRestart 验证密钥落库:同一数据库重新"启动"后,重启前签发的 cookie 仍有效。
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
