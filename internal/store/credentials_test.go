package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestSaveDetailsCacheKeepsOldOnPartialFailure 保證某一路採集失敗(傳空)時保留舊快取。
func TestSaveDetailsCacheKeepsOldOnPartialFailure(t *testing.T) {
	db := openTestDB(t)
	if err := Save(db, &Credential{Vid: "v1", RefreshToken: "rt", DeviceID: "dev"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := SaveDetailsCache(db, "v1", []byte(`{"name":"n"}`), []byte(`{"expired":0}`), []byte(`[{"bookId":"b1"}]`)); err != nil {
		t.Fatalf("SaveDetailsCache: %v", err)
	}
	if err := SaveDetailsCache(db, "v1", nil, nil, nil); err != nil {
		t.Fatalf("SaveDetailsCache(empty): %v", err)
	}
	got, err := Load(db, "v1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Profile != `{"name":"n"}` || got.Card != `{"expired":0}` || got.Shelf != `[{"bookId":"b1"}]` {
		t.Fatalf("cache overwritten by empty fetch: profile=%q card=%q shelf=%q", got.Profile, got.Card, got.Shelf)
	}
}
