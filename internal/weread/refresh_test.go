package weread

import (
	"context"
	"fmt"
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

// TestRefreshHandsLatestGenerationToStaleCaller 校验单飞缓存的代际语义:
// 持有已被消费的更旧代际的调用方直接拿回最新一代,不再打 /login
// (旧 refreshToken 重复消费可能被服务端拒绝甚至整链作废);
// 手里就是最新一代的调用方属于正常到期续期,照常发起真实刷新。
func TestRefreshHandsLatestGenerationToStaleCaller(t *testing.T) {
	var calls atomic.Int32
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		n := calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(fmt.Sprintf(`{"vid":"v1","accessToken":"access-%d","refreshToken":"refresh-%d"}`, n, n))),
			Header:     make(http.Header),
		}, nil
	})
	ctx := context.Background()
	stale := &Credentials{Vid: "v1", AccessToken: "old-access", RefreshToken: "old-refresh", DeviceID: "device"}

	first, err := client.Refresh(ctx, stale)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if first.AccessToken != "access-1" || first.RefreshToken != "refresh-1" {
		t.Fatalf("first = %#v", first)
	}

	// 同一份已被消费的旧凭据再次刷新:交还最新一代,不发请求。
	again, err := client.Refresh(ctx, stale)
	if err != nil {
		t.Fatalf("Refresh(stale): %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("stale re-refresh issued a /login request (calls = %d)", calls.Load())
	}
	if !sameCredentials(again, first) {
		t.Fatalf("stale caller got %#v, want latest generation %#v", again, first)
	}

	// 手里是最新一代:正常续期路径,发起真实刷新。
	next, err := client.Refresh(ctx, first)
	if err != nil {
		t.Fatalf("Refresh(latest): %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("latest-generation refresh did not issue a request (calls = %d)", calls.Load())
	}
	if next.AccessToken != "access-2" || next.RefreshToken != "refresh-2" {
		t.Fatalf("next = %#v", next)
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

// TestRefreshReusesResultForStaleCredentials 涵盖「上一次刷新已结束,另一个调用方
// 仍持有旧凭据」的情形:应直接沿用结果,而不是再用已被轮换掉的 refreshToken 请求一次。
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

// TestRefreshPersistsEvenIfCallerCancels 验证调用方中途取消时,已送出的刷新仍会完成并落库:
// 服务端可能已轮换 refreshToken,丢掉结果就只能重新扫码。
func TestRefreshPersistsEvenIfCallerCancels(t *testing.T) {
	persisted := make(chan *Credentials, 1)
	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient()
	client.OnRefresh = func(next *Credentials) { persisted <- next }
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		cancel() // 请求已到达服务端后,调用方断开
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
