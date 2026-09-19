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
	if _, err := client.readHeartbeat(ctx, "cookie", "book", 1, 0, 0, 30); err != ErrSessionExpired {
		t.Fatalf("readHeartbeat error = %v, want ErrSessionExpired", err)
	}
	if _, err := client.ChapterUIDs(ctx, "cookie", "book"); err != ErrSessionExpired {
		t.Fatalf("ChapterUIDs error = %v, want ErrSessionExpired", err)
	}
}
