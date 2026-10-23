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

// TestSendErrorDoesNotLeakSecret 保證網路錯誤不把帶密鑰的 URL 寫進錯誤文字(進而寫進日誌)。
func TestSendErrorDoesNotLeakSecret(t *testing.T) {
	err := Send(t.Context(), TypeBark, map[string]string{
		"server":     "http://127.0.0.1:1",
		"device_key": "super-secret-key",
	}, "title", "body")
	if err == nil {
		t.Fatal("expected connection error")
	}
	if strings.Contains(err.Error(), "super-secret-key") {
		t.Fatalf("error leaks device key: %v", err)
	}
}
