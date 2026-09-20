package weread

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// 期望值由参考实现(Python)直接计算得出,用于校验 Go 移植与原始算法的一致性。
const webUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func TestCalcHashMatchesReference(t *testing.T) {
	cases := map[string]string{
		"3300060341":                     "81032840813ab7e12g0111d4",
		"ce032b305a9bc1ce0b0dd2a":        "a5b42b72e63653033326233303561396263316365306230646432613eb",
		"0":                              "cfc32da010cfcd208495488",
		"123456789012345678901234567890": "a46321d0775bcd15g06bc614eg0835b7bf87g0337aa1c",
	}
	for input, want := range cases {
		if got := calcHash(input); got != want {
			t.Errorf("calcHash(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGetWebAppIDMatchesReference(t *testing.T) {
	want := "wb182564874663h1256619504"
	if got := getWebAppID(webUA); got != want {
		t.Errorf("getWebAppID = %q, want %q", got, want)
	}
}

func TestSignPayloadMatchesReference(t *testing.T) {
	payload := map[string]any{
		"appId": getWebAppID(webUA),
		"b":     calcHash("3300060341"),
		"c":     calcHash("1"),
		"ci":    1,
		"co":    389,
		"ct":    1744264311,
		"dy":    0,
		"fm":    "epub",
		"pc":    calcHash("0"),
		"pr":    74,
		"ps":    calcHash("0"),
		"sm":    "",
		"rt":    30,
		"ts":    1744264311434,
		"rn":    466,
	}
	if got := signPayload(payload); got != "7406cc1f" {
		t.Errorf("signPayload = %q, want %q", got, "7406cc1f")
	}
}

func TestReadSessionUsesReaderTokenForHeartbeat(t *testing.T) {
	client := NewClient()
	var requests []map[string]any
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("request JSON: %v", err)
		}
		requests = append(requests, payload)
		response := `{"readerToken":"token-from-init"}`
		if len(requests) == 2 {
			response = `{"succ":1,"synckey":123}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
	})

	session, err := client.readInit(context.Background(), "wr_skey=skey", "3300060341", 1, 0, 0)
	if err != nil {
		t.Fatalf("readInit: %v", err)
	}
	if session.Token != "token-from-init" {
		t.Fatalf("token = %q", session.Token)
	}
	if _, ok := requests[0]["sg"]; ok {
		t.Fatal("read-init must not include sg")
	}

	if ok, err := client.readHeartbeat(context.Background(), "wr_skey=skey", session, "3300060341", 1, 10, 2, 30); err != nil || !ok {
		t.Fatalf("readHeartbeat = %v, %v", ok, err)
	}
	rt, ok := requests[1]["rt"].(float64)
	if !ok || rt != 30 {
		t.Fatalf("rt = %#v", requests[1]["rt"])
	}
	if requests[1]["sg"] == "" {
		t.Fatal("heartbeat missing sg")
	}
	if requests[1]["s"] == "" {
		t.Fatal("heartbeat missing s")
	}
}

func TestReadInitAcceptsSuccWithoutReaderToken(t *testing.T) {
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"succ":1,"synckey":123}`)),
			Header:     make(http.Header),
		}, nil
	})
	session, err := client.readInit(context.Background(), "wr_skey=skey", "book", 1, 0, 0)
	if err != nil {
		t.Fatalf("readInit: %v", err)
	}
	if session.Token != readSignatureKey {
		t.Fatalf("fallback token = %q, want fixed compatibility key", session.Token)
	}
}
