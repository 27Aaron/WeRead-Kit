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
		Alias: "default", Vid: "123", RefreshToken: "rt1", DeviceID: "dev", AccessToken: "at1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(db, "default")
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
		Alias: "default", Vid: "123", RefreshToken: "rt2", DeviceID: "dev", AccessToken: "at2",
	}); err != nil {
		t.Fatalf("Save rotate: %v", err)
	}
	got, err = Load(db, "default")
	if err != nil {
		t.Fatalf("Load after rotate: %v", err)
	}
	if got.RefreshToken != "rt2" || got.AccessToken != "at2" {
		t.Fatalf("rotation not persisted: %+v", got)
	}

	if _, err := Load(db, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing alias: want ErrNotFound, got %v", err)
	}
}

func TestFindAliasByVid(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if alias, err := FindAliasByVid(db, "999"); err != nil || alias != "" {
		t.Fatalf("empty db: want \"\", nil; got %q, %v", alias, err)
	}

	if err := Save(db, &Credential{Alias: "default", Vid: "111", RefreshToken: "rt", DeviceID: "dev"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	alias, err := FindAliasByVid(db, "111")
	if err != nil || alias != "default" {
		t.Fatalf("FindAliasByVid: want default, nil; got %q, %v", alias, err)
	}
}

func TestSaveRejectsIncomplete(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if err := Save(db, &Credential{Alias: "x", Vid: "", RefreshToken: "rt", DeviceID: "dev"}); err == nil {
		t.Fatal("Save with empty vid: want error, got nil")
	}
}
