package display

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/redundercover/dockstar/internal/config"
)

func drmConnector(t *testing.T, root, name, status string, edid []byte) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"status": []byte(status), "edid": edid} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPhysicalApprovalFollowsEDIDNotConnector(t *testing.T) {
	root := t.TempDir()
	edid := make([]byte, 128)
	cfg := config.Default()
	cfg.Displays = []config.DisplayRule{{ID: edidID(edid), Allowed: true}}
	drmConnector(t, root, "card1-DP-1", "connected\n", edid)
	backend := &KScreen{Runner: &fakeRunner{json: testDocument()}, DRMRoot: root}
	initial, err := backend.Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !initial.Outputs[1].Approved {
		t.Fatal("connected EDID approval not shared with KScreen")
	}
	for _, step := range []struct {
		name      string
		status    string
		edid      []byte
		connected bool
		approved  bool
	}{
		{"unplug with cached EDID", "disconnected", edid, false, false},
		{"unidentified replacement", "connected", nil, true, false},
		{"different monitor same connector", "connected", append([]byte{1}, edid[1:]...), true, false},
		{"approved monitor returns", "connected", edid, true, true},
	} {
		t.Run(step.name, func(t *testing.T) {
			drmConnector(t, root, "card1-DP-1", step.status, step.edid)
			snapshot, err := DRMSnapshot(root, cfg)
			if err != nil {
				t.Fatal(err)
			}
			output := snapshot.Outputs[0]
			if output.Connected != step.connected || output.Approved != step.approved {
				t.Fatalf("physical approval = connected %t, approved %t; want %t, %t", output.Connected, output.Approved, step.connected, step.approved)
			}
		})
	}
}

func TestSnapshotOverridesStaleCompositorConnection(t *testing.T) {
	root := t.TempDir()
	drmConnector(t, root, "card1-DP-1", "disconnected", make([]byte, 128))
	backend := &KScreen{Runner: &fakeRunner{json: testDocument()}, DRMRoot: root}
	cfg := config.Default()
	cfg.Detection = "all"
	snapshot, err := backend.Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Outputs[1].Connected {
		t.Fatal("cached KScreen connection survived a physical disconnect")
	}
	if _, err := backend.Apply(context.Background(), cfg, "external-only"); err != ErrNoApprovedDisplay {
		t.Fatalf("layout applied to disconnected external display: %v", err)
	}
}

func TestPhysicalSnapshotRejectsUnreadableConnection(t *testing.T) {
	root := t.TempDir()
	drmConnector(t, root, "card1-DP-1", "connected", make([]byte, 128))
	if err := os.Remove(filepath.Join(root, "card1-DP-1", "status")); err != nil {
		t.Fatal(err)
	}
	if _, err := DRMSnapshot(root, config.Default()); err == nil {
		t.Fatal("unreadable hardware was reported as a confirmed undock")
	}
}

func TestAmbiguousGPUConnectorsDoNotBorrowApproval(t *testing.T) {
	root := t.TempDir()
	edid := make([]byte, 128)
	drmConnector(t, root, "card1-DP-1", "connected", edid)
	drmConnector(t, root, "card2-DP-1", "connected", append([]byte{1}, edid[1:]...))
	cfg := config.Default()
	cfg.Displays = []config.DisplayRule{{ID: edidID(edid), Allowed: true}}
	backend := &KScreen{Runner: &fakeRunner{json: testDocument()}, DRMRoot: root}
	snapshot, err := backend.Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Outputs[1].Approved {
		t.Fatal("ambiguous KScreen connector inherited another GPU's approved EDID")
	}
}

func TestResolvePhysicalPreservesExplicitFallbackApprovals(t *testing.T) {
	for _, id := range []string{"kde:external", fallbackDeviceID("DP-1", "")} {
		t.Run(id, func(t *testing.T) {
			cfg := config.Default()
			cfg.Displays = []config.DisplayRule{{ID: id, Allowed: true}}
			session := Snapshot{Outputs: []Output{{DeviceID: id, Connector: "DP-1", Connected: true, Approved: true}}}
			physical := Snapshot{Outputs: []Output{{Connector: "DP-1", Connected: true}}}
			resolved := ResolvePhysical(physical, session, cfg)
			if !resolved.Outputs[0].Approved || resolved.Outputs[0].DeviceID != id {
				t.Fatal("explicit session approval was not preserved for a connected EDID-less display")
			}
			// Cached approval must not survive a saved revocation.
			cfg.Displays[0].Allowed = false
			if revoked := ResolvePhysical(physical, session, cfg); revoked.Outputs[0].Approved {
				t.Fatal("cached session approval survived revocation")
			}
			physical.Outputs[0].Connected = false
			cfg.Displays[0].Allowed = true
			if unplugged := ResolvePhysical(physical, session, cfg); unplugged.Outputs[0].Approved {
				t.Fatal("cached session connection approved an unplugged display")
			}
		})
	}
}

func TestResolvePhysicalDoesNotBorrowUncertainIdentity(t *testing.T) {
	cfg := config.Default()
	cfg.Displays = []config.DisplayRule{
		{ID: "kde:external", Allowed: true},
		{ID: edidID(make([]byte, 128)), Allowed: true},
	}
	for _, test := range []struct {
		name     string
		physical []Output
		session  []Output
	}{
		{
			name:     "known physical identity takes precedence",
			physical: []Output{{Connector: "DP-1", DeviceID: edidID(append([]byte{1}, make([]byte, 127)...)), Connected: true}},
			session:  []Output{{Connector: "DP-1", DeviceID: "kde:external", Connected: true, Approved: true}},
		},
		{
			name:     "cached EDID cannot identify an unknown replacement",
			physical: []Output{{Connector: "DP-1", Connected: true}},
			session:  []Output{{Connector: "DP-1", DeviceID: cfg.Displays[1].ID, Connected: true, Approved: true}},
		},
		{
			name:     "ambiguous GPU connectors",
			physical: []Output{{Connector: "DP-1", Connected: true}, {Connector: "DP-1", Connected: true}},
			session:  []Output{{Connector: "DP-1", DeviceID: "kde:external", Connected: true, Approved: true}},
		},
		{
			name:     "ambiguous session connectors",
			physical: []Output{{Connector: "DP-1", Connected: true}},
			session:  []Output{{Connector: "DP-1", DeviceID: "kde:external", Connected: true}, {Connector: "DP-1", DeviceID: "kde:other", Connected: true}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved := ResolvePhysical(Snapshot{Outputs: test.physical}, Snapshot{Outputs: test.session}, cfg)
			for _, output := range resolved.Outputs {
				if output.Approved {
					t.Fatal("uncertain session identity granted physical approval")
				}
			}
		})
	}
}
