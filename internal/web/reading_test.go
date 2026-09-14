package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/27Aaron/weread-kit/internal/store"
)

// TestReadingConfigValidation 验证 RunAt 补零校验、书 ID 分隔符拒绝与自动去重。
func TestReadingConfigValidation(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := store.Save(db, &store.Credential{Vid: "v1", RefreshToken: "rt", DeviceID: "dev"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s := &Server{db: db, farms: map[string]*farmSessionHandle{}}

	post := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/accounts/v1/reading", strings.NewReader(body))
		req.Header.Set("content-type", "application/json")
		req.SetPathValue("vid", "v1")
		rec := httptest.NewRecorder()
		s.handleReadingConfig(rec, req)
		return rec.Code
	}

	if post(`{"enabled":true,"book_ids":["b1"],"minutes":30,"run_at":"3:00"}`) != http.StatusBadRequest {
		t.Fatal("non-padded run_at: want 400")
	}
	if post(`{"enabled":true,"book_ids":["a,b"],"minutes":30,"run_at":"03:00"}`) != http.StatusBadRequest {
		t.Fatal("book id containing comma: want 400")
	}
	if post(`{"enabled":true,"book_ids":["b1","b1","b2"],"minutes":30,"run_at":"03:00"}`) != http.StatusOK {
		t.Fatal("valid config: want 200")
	}
	cfg, err := store.GetReadingConfig(db, "v1")
	if err != nil {
		t.Fatalf("GetReadingConfig: %v", err)
	}
	if want := []string{"b1", "b2"}; !reflect.DeepEqual(cfg.BookIDs, want) {
		t.Fatalf("book_ids = %v, want %v", cfg.BookIDs, want)
	}
}
