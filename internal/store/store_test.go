package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadRoundtrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if err := Save(db, &Credential{
		Vid: "123", RefreshToken: "rt1", DeviceID: "dev", AccessToken: "at1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(db, "123")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Vid != "123" || got.RefreshToken != "rt1" || got.DeviceID != "dev" || got.AccessToken != "at1" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if time.Since(got.RotatedAt) > time.Minute {
		t.Fatalf("rotated_at not recent: %v", got.RotatedAt)
	}

	// 轮换后 UPSERT 覆盖同一行,不产生第二条记录。
	if err := Save(db, &Credential{
		Vid: "123", RefreshToken: "rt2", DeviceID: "dev", AccessToken: "at2",
	}); err != nil {
		t.Fatalf("Save rotate: %v", err)
	}
	got, err = Load(db, "123")
	if err != nil {
		t.Fatalf("Load after rotate: %v", err)
	}
	if got.RefreshToken != "rt2" || got.AccessToken != "at2" {
		t.Fatalf("rotation not persisted: %+v", got)
	}

	if _, err := Load(db, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing vid: want ErrNotFound, got %v", err)
	}
}

func TestSaveRejectsIncomplete(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if err := Save(db, &Credential{Vid: "x", RefreshToken: "", DeviceID: "dev"}); err == nil {
		t.Fatal("Save with empty refresh_token: want error, got nil")
	}
}

// TestDeletePurgesLogs 回归:删除账号须连带清理其历史日志,其他账号日志不受影响。
func TestDeletePurgesLogs(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	for _, vid := range []string{"v1", "v2"} {
		if err := Save(db, &Credential{Vid: vid, RefreshToken: "rt", DeviceID: "dev"}); err != nil {
			t.Fatalf("Save %s: %v", vid, err)
		}
		AddLog(db, "info", "farm", vid, "log of "+vid)
	}

	if err := Delete(db, "v1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	logs, err := ListLogs(db, "v1", "", 100)
	if err != nil {
		t.Fatalf("ListLogs v1: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("logs of deleted account not purged: got %d entries", len(logs))
	}
	logs, err = ListLogs(db, "v2", "", 100)
	if err != nil {
		t.Fatalf("ListLogs v2: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs of other account affected: got %d entries, want 1", len(logs))
	}
}

// TestOpenRelativePath 回归:相对路径经 url.URL 构造 DSN 时,
// 首段会被当成 URI authority(file://data/...),驱动报 invalid uri authority。
func TestOpenRelativePath(t *testing.T) {
	dir := "weread-kit-testdata-relative"
	defer os.RemoveAll(dir)
	db, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open relative path: %v", err)
	}
	defer db.Close()
}
