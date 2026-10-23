package store

import (
	"database/sql"
	"errors"
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

// TestUpdateTokensDoesNotResurrectDeletedAccount 保證續期落庫只更新現有帳號,
// 與刪除並行時不會把帳號插回來。
func TestUpdateTokensDoesNotResurrectDeletedAccount(t *testing.T) {
	db := openTestDB(t)
	if err := UpdateTokens(db, "gone", "at", "rt", "dev"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateTokens on missing account = %v, want ErrNotFound", err)
	}
	if _, err := Load(db, "gone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("account was created: %v", err)
	}

	if err := Save(db, &Credential{Vid: "v1", RefreshToken: "rt1", DeviceID: "dev", AccessToken: "at1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := UpdateTokens(db, "v1", "at2", "rt2", "dev"); err != nil {
		t.Fatalf("UpdateTokens: %v", err)
	}
	got, err := Load(db, "v1")
	if err != nil || got.AccessToken != "at2" || got.RefreshToken != "rt2" {
		t.Fatalf("Load = %#v, %v", got, err)
	}
}

// TestDeleteRemovesAccountSettings 保證刪除帳號時清掉以 ":<vid>" 結尾的帳號級設定,
// 且不誤刪其他帳號或全域設定。
func TestDeleteRemovesAccountSettings(t *testing.T) {
	db := openTestDB(t)
	for _, vid := range []string{"1", "11"} {
		if err := Save(db, &Credential{Vid: vid, RefreshToken: "rt", DeviceID: "dev"}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if err := SetSetting(db, "weekly_claim_pref:"+vid, `{"1":1}`); err != nil {
			t.Fatalf("SetSetting: %v", err)
		}
	}
	if err := SetSetting(db, "web_session_key", "k"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := Delete(db, "1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := GetSetting(db, "weekly_claim_pref:1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pref of deleted account survived: %v", err)
	}
	for _, key := range []string{"weekly_claim_pref:11", "web_session_key"} {
		if _, err := GetSetting(db, key); err != nil {
			t.Fatalf("%s wrongly deleted: %v", key, err)
		}
	}
}
