package weread

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRefreshCoalescesConcurrentRequests(t *testing.T) {
	var calls atomic.Int32
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		time.Sleep(25 * time.Millisecond)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"vid":"v1","accessToken":"new-access","refreshToken":"new-refresh"}`)),
			Header:     make(http.Header),
		}, nil
	})
	creds := &Credentials{Vid: "v1", AccessToken: "old-access", RefreshToken: "old-refresh", DeviceID: "device"}
	const workers = 12
	start := make(chan struct{})
	results := make(chan *Credentials, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			next, err := client.Refresh(context.Background(), creds)
			results <- next
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	if got := calls.Load(); got != 1 {
		t.Fatalf("refresh requests = %d, want 1", got)
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("Refresh returned error: %v", err)
		}
	}
	for next := range results {
		if next == nil || next.AccessToken != "new-access" || next.RefreshToken != "new-refresh" {
			t.Fatalf("unexpected refreshed credentials: %#v", next)
		}
	}
}

func TestReadEndpointsMapUnauthorizedToSessionExpired(t *testing.T) {
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})
	ctx := context.Background()
	if _, err := client.readHeartbeat(ctx, "cookie", &ReaderSession{Token: "reader-token"}, "book", 1, 0, 0, 30); err != ErrSessionExpired {
		t.Fatalf("readHeartbeat error = %v, want ErrSessionExpired", err)
	}
	if _, err := client.ChapterUIDs(ctx, "cookie", "book"); err != ErrSessionExpired {
		t.Fatalf("ChapterUIDs error = %v, want ErrSessionExpired", err)
	}
}

func TestRenewWebCookieMergesRotatedCookie(t *testing.T) {
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/web/login/renewal" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Cookie"); got != "wr_vid=v1; wr_skey=old; wr_rt=rt1" {
			t.Fatalf("cookie = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"succ":1}`)),
			Header:     http.Header{"Set-Cookie": []string{"wr_skey=new; Path=/"}},
		}, nil
	})
	cookie, err := client.RenewWebCookie(context.Background(), "wr_vid=v1; wr_skey=old; wr_rt=rt1")
	if err != nil {
		t.Fatalf("RenewWebCookie: %v", err)
	}
	if cookie != "wr_vid=v1; wr_skey=new; wr_rt=rt1" {
		t.Fatalf("cookie = %q", cookie)
	}
}

// TestRefreshReusesResultForStaleCredentials 涵蓋「上一次刷新已結束,另一個呼叫方
// 仍持有舊憑據」的情形:應直接沿用結果,而不是再用已被輪換掉的 refreshToken 請求一次。
func TestRefreshReusesResultForStaleCredentials(t *testing.T) {
	var calls atomic.Int32
	var persisted []*Credentials
	client := NewClient()
	client.OnRefresh = func(next *Credentials) { persisted = append(persisted, next) }
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"vid":"v1","accessToken":"new-access","refreshToken":"new-refresh"}`)),
			Header:     make(http.Header),
		}, nil
	})
	stale := &Credentials{Vid: "v1", AccessToken: "old-access", RefreshToken: "old-refresh", DeviceID: "device"}
	for range 2 {
		next, err := client.Refresh(context.Background(), stale)
		if err != nil || next.RefreshToken != "new-refresh" {
			t.Fatalf("Refresh = %#v, %v", next, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("refresh requests = %d, want 1", got)
	}
	if len(persisted) != 1 || persisted[0].RefreshToken != "new-refresh" {
		t.Fatalf("OnRefresh calls = %#v, want exactly one with rotated token", persisted)
	}
}

// TestRefreshPersistsEvenIfCallerCancels 驗證呼叫方中途取消時,已送出的刷新仍會完成並落庫:
// 服務端可能已輪換 refreshToken,丟掉結果就只能重新掃碼。
func TestRefreshPersistsEvenIfCallerCancels(t *testing.T) {
	persisted := make(chan *Credentials, 1)
	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient()
	client.OnRefresh = func(next *Credentials) { persisted <- next }
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		cancel() // 請求已到達服務端後,呼叫方斷開
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"vid":"v1","accessToken":"new-access","refreshToken":"new-refresh"}`)),
			Header:     make(http.Header),
		}, nil
	})
	if _, err := client.Refresh(ctx, &Credentials{Vid: "v1", RefreshToken: "old-refresh", DeviceID: "device"}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	select {
	case next := <-persisted:
		if next.RefreshToken != "new-refresh" {
			t.Fatalf("persisted = %#v", next)
		}
	default:
		t.Fatal("OnRefresh not called after caller cancellation")
	}
}
