package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/redundercover/dockstar/internal/config"
	"github.com/redundercover/dockstar/internal/startup"
)

func TestDisplayLabel(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"extend":        "Extend",
		"mirror":        "Mirror",
		"external-only": "External only",
		"internal-only": "Internal only",
		"custom":        "custom",
	}
	for input, want := range tests {
		input, want := input, want
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			if got := displayLabel(input); got != want {
				t.Fatalf("displayLabel(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func TestWakeActionRejectsUnknownAction(t *testing.T) {
	t.Parallel()
	if _, err := (&App{}).WakeAction("apply"); err == nil {
		t.Fatal("WakeAction accepted an unsupported action")
	}
}

func TestBootProtectionStatusReportsSavedAndInstalledDivergence(t *testing.T) {
	t.Parallel()
	added := "edid:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	removed := "edid:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	cfg := config.Default()
	cfg.Displays = []config.DisplayRule{{ID: added, Allowed: true}}
	current := startup.Status{
		Installed: true, Enabled: true, Detection: "allowlist", KeepAwakeWhileDocked: true,
		AllowedEDIDs: []string{removed},
	}

	got := bootProtectionStatus(cfg, current)
	if got.PolicyMismatch {
		t.Fatalf("PolicyMismatch = true for matching enabled policy metadata")
	}
	if got.InSync {
		t.Fatal("InSync = true with approval additions and removals")
	}
	if len(got.PendingAdditions) != 1 || got.PendingAdditions[0] != added {
		t.Fatalf("PendingAdditions = %v, want %q", got.PendingAdditions, added)
	}
	if len(got.PendingRemovals) != 1 || got.PendingRemovals[0] != removed {
		t.Fatalf("PendingRemovals = %v, want %q", got.PendingRemovals, removed)
	}
}

func TestBootProtectionStatusFinalRevocationRequiresDisabledPolicy(t *testing.T) {
	t.Parallel()
	revoked := "edid:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	cfg := config.Default()
	cfg.Displays = []config.DisplayRule{{ID: revoked, Allowed: false}}

	pending := bootProtectionStatus(cfg, startup.Status{
		Installed: true, Enabled: true, Detection: "allowlist", KeepAwakeWhileDocked: true,
		AllowedEDIDs: []string{revoked},
	})
	if !pending.PolicyMismatch || pending.InSync {
		t.Fatalf("enabled status after final revocation = %+v, want disabled-policy mismatch", pending)
	}
	if len(pending.PendingRemovals) != 1 || pending.PendingRemovals[0] != revoked {
		t.Fatalf("PendingRemovals = %v, want final revoked EDID", pending.PendingRemovals)
	}

	synced := bootProtectionStatus(cfg, startup.Status{Installed: true, Detection: "off", AllowedEDIDs: []string{}})
	if !synced.InSync || synced.PolicyMismatch || len(synced.PendingRemovals) != 0 {
		t.Fatalf("disabled status after final revocation = %+v, want synchronized disabled policy", synced)
	}
}

func TestDetectWakeSupport(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, component := range wakeSupportPaths {
		path := filepath.Join(root, filepath.Clean(component.path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if component.executable {
			mode = 0o755
		}
		if err := os.WriteFile(path, []byte("fixture"), mode); err != nil {
			t.Fatal(err)
		}
	}

	support := detectWakeSupport(root)
	if !support.Manageable || !support.Persistent || len(support.Missing) != 0 {
		t.Fatalf("detectWakeSupport() = %+v, want complete support", support)
	}
}

func TestDetectWakeSupportReportsMissingComponents(t *testing.T) {
	t.Parallel()
	support := detectWakeSupport(t.TempDir())
	if support.Manageable || support.Persistent {
		t.Fatalf("detectWakeSupport() = %+v, want unavailable support", support)
	}
	if len(support.Missing) != len(wakeSupportPaths) {
		t.Fatalf("missing = %v, want %d components", support.Missing, len(wakeSupportPaths))
	}
}

func TestDesktopEntryHidden(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data string
		want bool
	}{
		{name: "hidden", data: "[Desktop Entry]\nHidden=true\n", want: true},
		{name: "case insensitive", data: "[Desktop Entry]\nHidden=TRUE\n", want: true},
		{name: "visible", data: "[Desktop Entry]\nHidden=false\n", want: false},
		{name: "missing", data: "[Desktop Entry]\nType=Application\n", want: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := desktopEntryHidden([]byte(test.data)); got != test.want {
				t.Fatalf("desktopEntryHidden() = %v, want %v", got, test.want)
			}
		})
	}
}
