package weread

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// captureSendInfinite 记录请求查询串并按活动返回对应响应。
func captureSendInfinite(t *testing.T, responses map[string]string) (*Client, func() []string) {
	t.Helper()
	var queries []string
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/act/sendinfinite" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("vid"); got != "v1" {
			t.Fatalf("vid header = %q", got)
		}
		queries = append(queries, r.URL.RawQuery)
		key := r.URL.Query().Get("source") + "/" + r.URL.Query().Get("cmd")
		body, ok := responses[key]
		if !ok {
			body = `{}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})
	return client, func() []string { return queries }
}

// TestSendInfiniteQueryHotlist 校验热门书单查询的参数组合与状态解析。
func TestSendInfiniteQueryHotlist(t *testing.T) {
	client, queries := captureSendInfinite(t, map[string]string{
		"hotlist_infinite/query": `{"received":0,"day":1,"coupon":0,"freeReceive":0,"paying":0}`,
	})
	state, err := client.SendInfiniteQuery(context.Background(), &Credentials{Vid: "v1"}, SendInfiniteActivities[0])
	if err != nil {
		t.Fatalf("SendInfiniteQuery: %v", err)
	}
	if !state.Claimable() || state.Days() != 1 {
		t.Fatalf("state = %+v, want claimable 1 day", state)
	}
	q := queries()[0]
	for _, want := range []string{"cmd=query", "type=hotlist", "source=hotlist_infinite", "guestAllow=1", "pf="} {
		if !strings.Contains(q, want) {
			t.Fatalf("query = %q, missing %s", q, want)
		}
	}
}

// TestSendInfiniteQuerySoaring 校验飙升榜查询与已领状态判定。
func TestSendInfiniteQuerySoaring(t *testing.T) {
	client, _ := captureSendInfinite(t, map[string]string{
		"soaring/query": `{"canReceive":0,"hadReceived":0,"sendDays":3}`,
	})
	state, err := client.SendInfiniteQuery(context.Background(), &Credentials{Vid: "v1"}, SendInfiniteActivities[1])
	if err != nil {
		t.Fatalf("SendInfiniteQuery: %v", err)
	}
	if state.Claimable() {
		t.Fatalf("canReceive=0 不应可领: %+v", state)
	}
	if state.Days() != 3 {
		t.Fatalf("days = %d, want 3", state.Days())
	}
}

// TestSendInfiniteReceiveParams 两个活动的领取参数组合不同,逐一校验。
func TestSendInfiniteReceiveParams(t *testing.T) {
	client, queries := captureSendInfinite(t, map[string]string{
		"hotlist_infinite/receive": `{"day":1}`,
		"soaring/receive":          `{"errcode":-2038}`,
	})
	act := SendInfiniteActivities[0]
	if _, err := client.SendInfiniteReceive(context.Background(), &Credentials{Vid: "v1"}, act); err != nil {
		t.Fatalf("hotlist receive: %v", err)
	}
	// 热门书单领取必须带 type,否则服务端 -2003。
	for _, want := range []string{"cmd=receive", "type=hotlist", "source=hotlist_infinite"} {
		if !strings.Contains(queries()[0], want) {
			t.Fatalf("hotlist receive query = %q, missing %s", queries()[0], want)
		}
	}

	act = SendInfiniteActivities[1]
	if _, err := client.SendInfiniteReceive(context.Background(), &Credentials{Vid: "v1"}, act); err == nil {
		t.Fatal("soaring -2038 应返回业务错误")
	}
	// 飙升榜领取不带 type/guestAllow。
	if strings.Contains(queries()[1], "type=") || strings.Contains(queries()[1], "guestAllow=") {
		t.Fatalf("soaring receive query = %q, 不应带 type/guestAllow", queries()[1])
	}
}

// TestSendInfiniteUnauthorized 映射为会话过期。
func TestSendInfiniteUnauthorized(t *testing.T) {
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})
	if _, err := client.SendInfiniteQuery(context.Background(), &Credentials{Vid: "v1"}, SendInfiniteActivities[0]); err != ErrSessionExpired {
		t.Fatalf("error = %v, want ErrSessionExpired", err)
	}
}
