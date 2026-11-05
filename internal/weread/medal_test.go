package weread

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestMedalListRequest(t *testing.T) {
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/medal/list" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		return jsonResponse(http.StatusOK, `{"available":[],"notAvailable":[],"totalCount":0}`), nil
	})
	if _, err := client.MedalList(context.Background(), &Credentials{Vid: "v1"}); err != nil {
		t.Fatalf("MedalList: %v", err)
	}
}

func TestMedalListUnauthorized(t *testing.T) {
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusUnauthorized, `{}`), nil
	})
	if _, err := client.MedalList(context.Background(), &Credentials{Vid: "v1"}); err != ErrSessionExpired {
		t.Fatalf("error = %v, want ErrSessionExpired", err)
	}
}

func TestChallengeHistoryRequest(t *testing.T) {
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(r.URL.Path, "/challenge/history") {
			t.Fatalf("path = %s", r.URL.Path)
		}
		return jsonResponse(http.StatusOK, `{"challenges":[],"hasMore":0,"totalCount":0}`), nil
	})
	if _, err := client.ChallengeHistory(context.Background(), &Credentials{Vid: "v1"}); err != nil {
		t.Fatalf("ChallengeHistory: %v", err)
	}
}
