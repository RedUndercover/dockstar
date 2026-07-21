package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default().Validate() = %v", err)
	}
}

func TestDecodeMigratesSchemaOneWithDockProtectionEnabled(t *testing.T) {
	t.Parallel()
	legacy := Default()
	legacy.SchemaVersion = 1
	legacy.Revision = 7
	legacy.Power.KeepAwakeWhileDocked = false
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}

	got, migrated, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated || got.SchemaVersion != 2 || got.Revision != 8 || !got.Power.KeepAwakeWhileDocked {
		t.Fatalf("Decode() = %+v, migrated=%t", got, migrated)
	}
}

func TestOpenPersistsMigratedConfiguration(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := Default()
	legacy.SchemaVersion = 1
	legacy.Revision = 3
	legacy.Power.KeepAwakeWhileDocked = false
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Get(); got.SchemaVersion != 2 || got.Revision != 4 || !got.Power.KeepAwakeWhileDocked {
		t.Fatalf("migrated config = %+v", got)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, migrated, err := Decode(persisted); err != nil || migrated || got.SchemaVersion != 2 {
		t.Fatalf("persisted Decode() = %+v, migrated=%t, err=%v", got, migrated, err)
	}
}

func TestStoreRejectsStaleRevision(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	current := store.Get()
	next := current
	next.Detection = "all"
	updated, err := store.Replace(next, current.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != current.Revision+1 {
		t.Fatalf("revision = %d, want %d", updated.Revision, current.Revision+1)
	}
	if _, err := store.Replace(current, current.Revision); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale Replace error = %v, want ErrRevisionConflict", err)
	}
}

func TestValidateDisplayRules(t *testing.T) {
	cfg := Default()
	cfg.Displays = []DisplayRule{{ID: "edid:one", Allowed: true, Scale: 1, Mode: "preferred", Position: "right"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Displays = append(cfg.Displays, cfg.Displays[0])
	if err := cfg.Validate(); err == nil {
		t.Fatal("duplicate device IDs should be rejected")
	}
}

func FuzzConfigValidation(f *testing.F) {
	f.Add("allowlist", "extend", 1.0)
	f.Add("all", "external-only", 2.0)
	f.Fuzz(func(t *testing.T, detection, layout string, scale float64) {
		cfg := Default()
		cfg.Detection = detection
		cfg.DefaultLayout = layout
		cfg.Displays = []DisplayRule{{ID: "fuzz", Allowed: true, Scale: scale, Mode: "preferred", Position: "right"}}
		_ = cfg.Validate()
	})
}
