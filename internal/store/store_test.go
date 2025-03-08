package store

import (
	"errors"
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
