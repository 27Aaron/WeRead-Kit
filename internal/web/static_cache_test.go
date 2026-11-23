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

func TestVersionedStaticCache(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	s := New(db)
	s.username, s.password = "u", "p"
	h := s.Handler()
	wantCache := "public, max-age=31536000, immutable"
	authed := func(path string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: "weread_session", Value: s.sessionToken(time.Now().Add(time.Hour).Unix())})
		return req
	}

	for _, name := range []string{"font.css", "style.css", "app.js", "favicon.png"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authed("/"+name+"?v="+Version))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: want 200, got %d", name, rec.Code)
		}
		if got := rec.Header().Get("cache-control"); got != wantCache {
			t.Fatalf("%s cache-control: want %q, got %q", name, wantCache, got)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s: empty body", name)
		}
	}

	for _, path := range []string{"/font.css", "/font.css?v=0.0.0", "/style.css", "/app.js", "/favicon.png"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authed(path))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: want 200, got %d", path, rec.Code)
		}
		if got := rec.Header().Get("cache-control"); got != "no-store" {
			t.Fatalf("%s cache-control: want no-store, got %q", path, got)
		}
	}

	// 样式和脚本仍要登录。字库样式要给登录页用，不能被挡回去。
	for _, path := range []string{"/style.css?v=" + Version, "/app.js?v=" + Version} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusFound {
			t.Fatalf("%s without cookie: want 302, got %d", path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/font.css?v="+Version, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("font.css without cookie: want 200, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("home without cookie: want 302, got %d", rec.Code)
	}

	home := httptest.NewRecorder()
	h.ServeHTTP(home, authed("/"))
	if home.Code != http.StatusOK {
		t.Fatalf("home: want 200, got %d", home.Code)
	}
	if got := home.Header().Get("cache-control"); got != "no-store" {
		t.Fatalf("home cache-control: want no-store, got %q", got)
	}
	for _, name := range []string{"font.css", "style.css", "app.js", "favicon.png"} {
		if !strings.Contains(home.Body.String(), `"/`+name+`?v=`+Version+`"`) {
			t.Fatalf("home: missing versioned %s", name)
		}
	}

	login := httptest.NewRecorder()
	h.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/login", nil))
	if login.Code != http.StatusOK {
		t.Fatalf("login: want 200, got %d", login.Code)
	}
	if got := login.Header().Get("cache-control"); got != "no-store" {
		t.Fatalf("login cache-control: want no-store, got %q", got)
	}
	for _, name := range []string{"font.css", "favicon.png"} {
		if !strings.Contains(login.Body.String(), `"/`+name+`?v=`+Version+`"`) {
			t.Fatalf("login: missing versioned %s", name)
		}
	}
}
