package weread

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// TestChallengeDetailUsesV3 确保 detail 走 version=v3 而不是旧的无数据 scene=1 签到接口。
func TestChallengeDetailUsesV3(t *testing.T) {
	client := NewClient()
	var query string
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/challenge/detail" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		query = r.URL.RawQuery
		if got := r.Header.Get("vid"); got != "v1" {
			t.Fatalf("vid header = %q", got)
		}
		if got := r.Header.Get("accessToken"); got != "tok" {
			t.Fatalf("accessToken header = %q", got)
		}
		return jsonResponse(http.StatusOK, `{"challengeList":[]}`), nil
	})
	if _, err := client.ChallengeDetail(context.Background(), &Credentials{Vid: "v1", AccessToken: "tok"}); err != nil {
		t.Fatalf("ChallengeDetail error: %v", err)
	}
	if !strings.Contains(query, "version=v3") || strings.Contains(query, "scene=1") {
		t.Fatalf("query = %q, want version=v3 without scene=1", query)
	}
}

// TestChallengeListUsesV3 同样校验列表接口的查询参数与鉴权头。
func TestChallengeListUsesV3(t *testing.T) {
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/challenge/list" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if !strings.Contains(r.URL.RawQuery, "version=v3") {
			t.Fatalf("query = %q", r.URL.RawQuery)
		}
		return jsonResponse(http.StatusOK, `{"challengeList":[]}`), nil
	})
	if _, err := client.ChallengeList(context.Background(), &Credentials{Vid: "v1", AccessToken: "tok"}); err != nil {
		t.Fatalf("ChallengeList error: %v", err)
	}
}

// TestChallengeJoinRequestShape 断言 join 的方法、路径与请求体。
func TestChallengeJoinRequestShape(t *testing.T) {
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/challenge/join" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if got, want := string(body), `{"challengeId":"ChallengeLongTermFree"}`; got != want {
			t.Fatalf("body = %s, want %s", got, want)
		}
		return jsonResponse(http.StatusOK, `{"succ":1}`), nil
	})
	if _, err := client.ChallengeJoin(context.Background(), &Credentials{Vid: "v1", AccessToken: "tok"}, ChallengeLongTermFree); err != nil {
		t.Fatalf("ChallengeJoin error: %v", err)
	}
}

// TestChallengeJoinAlreadyJoined 覆盖 -2904 的两种到达路径:HTTP 200 带错误信封、
// 以及非 200 响应(两种形态服务端都存在)。
func TestChallengeJoinAlreadyJoined(t *testing.T) {
	for name, status := range map[string]int{"ok-status": http.StatusOK, "error-status": http.StatusBadRequest} {
		t.Run(name, func(t *testing.T) {
			client := NewClient()
			client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return jsonResponse(status, `{"errcode":-2904,"errmsg":"-2904"}`), nil
			})
			_, err := client.ChallengeJoin(context.Background(), &Credentials{Vid: "v1"}, ChallengeLongTermFree)
			if err != ErrChallengeAlreadyJoined {
				t.Fatalf("error = %v, want ErrChallengeAlreadyJoined", err)
			}
		})
	}
}

// TestChallengeJoinOtherBusinessError 验证其余 errcode 原样带回错误信息。
func TestChallengeJoinOtherBusinessError(t *testing.T) {
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusBadRequest, `{"errcode":-2003,"errmsg":"参数格式错误"}`), nil
	})
	_, err := client.ChallengeJoin(context.Background(), &Credentials{Vid: "v1"}, "readchallenge4")
	if err == nil || !strings.Contains(err.Error(), "-2003") || !strings.Contains(err.Error(), "参数格式错误") {
		t.Fatalf("error = %v, want errcode -2003 with errmsg", err)
	}
}

// TestChallengeJoinUnauthorized 映射为会话过期供上层续期重试。
func TestChallengeJoinUnauthorized(t *testing.T) {
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusUnauthorized, `{}`), nil
	})
	if _, err := client.ChallengeJoin(context.Background(), &Credentials{Vid: "v1"}, ChallengeLongTermFree); err != ErrSessionExpired {
		t.Fatalf("error = %v, want ErrSessionExpired", err)
	}
}
