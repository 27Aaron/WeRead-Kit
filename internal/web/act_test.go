package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/27Aaron/weread-kit/internal/store"
	"github.com/27Aaron/weread-kit/internal/weread"
)

// newActServer 构造带活动接口桩的最小 Server,记录每次请求的 cmd/source。
func newActServer(t *testing.T, respond func(cmd, source string) (int, string)) (*Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	transport := func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		mu.Lock()
		calls = append(calls, q.Get("cmd")+"/"+q.Get("source"))
		mu.Unlock()
		status, body := respond(q.Get("cmd"), q.Get("source"))
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Save(db, &store.Credential{Vid: "v1", RefreshToken: "rt", DeviceID: "dev", AccessToken: "tok"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	client := weread.NewClient()
	client.HTTP.Transport = stubTransport(transport)
	return &Server{db: db, client: client}, &calls
}

func enableActAutoClaim(t *testing.T, s *Server) {
	t.Helper()
	if err := store.SetSetting(s.db, actAutoClaimKey+"v1", "1"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
}

// TestActAutoClaimSetting 覆盖开关 API 的参数校验与落库。
func TestActAutoClaimSetting(t *testing.T) {
	s, _ := newActServer(t, func(cmd, source string) (int, string) {
		t.Fatal("开关保存不应触达官方接口")
		return 0, ""
	})
	post := func(vid, body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+vid+"/act/auto-claim", strings.NewReader(body))
		req.Header.Set("content-type", "application/json")
		req.SetPathValue("vid", vid)
		rec := httptest.NewRecorder()
		s.handleActAutoClaim(rec, req)
		return rec.Code
	}
	if post("v1", `not-json`) != http.StatusBadRequest {
		t.Fatal("非 JSON: want 400")
	}
	if post("ghost", `{"enabled":true}`) != http.StatusNotFound {
		t.Fatal("未知账号: want 404")
	}
	if post("v1", `{"enabled":true}`) != http.StatusOK {
		t.Fatal("保存开关: want 200")
	}
	if !s.actAutoClaimOn("v1") {
		t.Fatal("开关未落库")
	}
}

// TestClaimSendInfiniteOnceClaimsWhenAvailable 热门书单可领时自动领取,
// 飙升榜不可领时只查询不领取。
func TestClaimSendInfiniteOnceClaimsWhenAvailable(t *testing.T) {
	s, calls := newActServer(t, func(cmd, source string) (int, string) {
		switch source {
		case "hotlist_infinite":
			if cmd == "query" {
				return http.StatusOK, `{"received":0,"day":1}`
			}
			return http.StatusOK, `{"day":1}`
		case "soaring":
			return http.StatusOK, `{"canReceive":0,"hadReceived":0,"sendDays":3}`
		}
		return http.StatusOK, `{}`
	})
	enableActAutoClaim(t, s)

	s.claimSendInfiniteOnce(context.Background())

	want := []string{
		"query/hotlist_infinite",
		"receive/hotlist_infinite",
		"query/soaring", // 不可领,无 receive
	}
	if len(*calls) != len(want) {
		t.Fatalf("calls = %v, want %v", *calls, want)
	}
	for i, w := range want {
		if (*calls)[i] != w {
			t.Fatalf("calls[%d] = %q, want %q (all: %v)", i, (*calls)[i], w, *calls)
		}
	}
	var msg string
	if err := s.db.QueryRow(`SELECT message FROM weread_log WHERE vid='v1' AND message LIKE '自动领取体验卡%' ORDER BY id DESC LIMIT 1`).Scan(&msg); err != nil {
		t.Fatalf("查询领取日志: %v", err)
	}
	if !strings.Contains(msg, "热门书单") || !strings.Contains(msg, "1") {
		t.Fatalf("领取日志 = %q", msg)
	}
}

// TestClaimSendInfiniteSkipsWhenDisabled 开关关闭时不发起任何请求。
func TestClaimSendInfiniteSkipsWhenDisabled(t *testing.T) {
	s, calls := newActServer(t, func(cmd, source string) (int, string) {
		return http.StatusOK, `{}`
	})
	s.claimSendInfiniteOnce(context.Background())
	if len(*calls) != 0 {
		t.Fatalf("开关关闭仍发起请求: %v", *calls)
	}
}

// TestClaimSendInfiniteAlreadyReceived 已领后不再重复领取。
func TestClaimSendInfiniteAlreadyReceived(t *testing.T) {
	s, calls := newActServer(t, func(cmd, source string) (int, string) {
		return http.StatusOK, `{"received":1,"day":1}`
	})
	enableActAutoClaim(t, s)
	s.claimSendInfiniteOnce(context.Background())
	for _, c := range *calls {
		if strings.HasPrefix(c, "receive/") {
			t.Fatalf("已领仍领取: %v", *calls)
		}
	}
	if _, err := url.Parse("x"); err != nil {
		t.Fatal(err)
	}
}
