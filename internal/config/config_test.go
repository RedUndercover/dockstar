package config

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default().Validate() = %v", err)
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
