package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSettingRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if _, err := GetSetting(db, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key: want ErrNotFound, got %v", err)
	}
	if err := SetSetting(db, "k", "v1"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := SetSetting(db, "k", "v2"); err != nil {
		t.Fatalf("SetSetting upsert: %v", err)
	}
	if v, err := GetSetting(db, "k"); err != nil || v != "v2" {
		t.Fatalf("GetSetting = %q, %v; want v2, nil", v, err)
	}
}
