package notify

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendBark(t *testing.T) {
	var gotURI, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer srv.Close()

	if err := Send(t.Context(), TypeBark, map[string]string{
		"server":     srv.URL,
		"device_key": "device/key",
	}, "title", "body"); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if gotURI != "/device%2Fkey" {
		t.Fatalf("request URI = %q, want escaped device key", gotURI)
	}
	if !strings.Contains(gotBody, `"title":"title"`) || !strings.Contains(gotBody, `"body":"body"`) {
		t.Fatalf("request body = %q, missing title or body", gotBody)
	}
}

func TestSendBarkRejectsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusBadGateway)
	}))
	defer srv.Close()

	err := Send(t.Context(), TypeBark, map[string]string{
		"server":     srv.URL,
		"device_key": "device",
	}, "title", "body")
	if err == nil {
		t.Fatal("Send succeeded for HTTP 502 response")
	}
}
