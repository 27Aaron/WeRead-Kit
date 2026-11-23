package web

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/27Aaron/weread-kit/internal/weread"
)

// TestLoginLimitIgnoresFinishedSessions 在途扫码达到上限时拒绝新会话;
// 这些会话一旦结束(取消),名额必须释放,不能把已结束的会话占满 15 分钟。
func TestLoginLimitIgnoresFinishedSessions(t *testing.T) {
	client := weread.NewClient()
	client.HTTP.Transport = stubTransport(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	m := newLoginManager(nil, client)
	t.Cleanup(func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, sess := range m.sessions {
			if sess.cancel != nil {
				sess.cancel()
			}
		}
	})

	var mu sync.Mutex
	var started []string
	var rejected int
	var wg sync.WaitGroup
	for i := 0; i < maxLoginSessions+5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := m.start()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				rejected++
				return
			}
			started = append(started, id)
		}()
	}
	wg.Wait()
	if len(started) != maxLoginSessions || rejected != 5 {
		t.Fatalf("started %d rejected %d, want %d and 5", len(started), rejected, maxLoginSessions)
	}

	for _, id := range started {
		m.cancel(id)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		inFlight := 0
		m.mu.Lock()
		for _, sess := range m.sessions {
			if sessionInFlight(sess.status) {
				inFlight++
			}
		}
		m.mu.Unlock()
		if inFlight == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	id, err := m.start()
	if err != nil {
		t.Fatalf("start after previous sessions finished: %v", err)
	}
	m.cancel(id)
}
