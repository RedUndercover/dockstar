package display

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redundercover/dockstar/internal/config"
)

type fakeRunner struct {
	json  string
	calls [][]string
}

func (f *fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	if len(args) == 1 && args[0] == "-j" {
		return []byte(f.json), nil
	}
	return nil, nil
}

func TestSnapshotUsesEDIDIdentity(t *testing.T) {
	drm := t.TempDir()
	dir := filepath.Join(drm, "card1-DP-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "edid"), make([]byte, 128), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte("connected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{json: testDocument()}
	backend := &KScreen{Runner: runner, Doctor: "kscreen-doctor", DRMRoot: drm}
	snapshot, err := backend.Snapshot(context.Background(), config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Outputs[1].DeviceID; !strings.HasPrefix(got, "edid:") || len(got) != len("edid:")+64 {
		t.Fatalf("device ID = %q", got)
	}
}

func TestInternalOnlyTransaction(t *testing.T) {
	runner := &fakeRunner{json: testDocument()}
	backend := &KScreen{Runner: runner, Doctor: "kscreen-doctor", DRMRoot: t.TempDir()}
	cfg := config.Default()
	cfg.Detection = "off"
	result, err := backend.Apply(context.Background(), cfg, "internal-only")
	if err != nil {
		t.Fatal(err)
	}
	if result.Primary != "eDP-1" {
		t.Fatalf("primary = %q", result.Primary)
	}
	args := strings.Join(runner.calls[len(runner.calls)-1], " ")
	for _, expected := range []string{"output.DP-1.disable", "output.eDP-1.enable", "output.eDP-1.priority.1"} {
		if !strings.Contains(args, expected) {
			t.Errorf("transaction %q does not contain %q", args, expected)
		}
	}
}

func TestCommonModePrefersLargestCappedMode(t *testing.T) {
	a := Output{Modes: []Mode{{Width: 2560, Height: 1440, Refresh: 144}, {Width: 1920, Height: 1080, Refresh: 60}}}
	b := Output{Modes: []Mode{{Width: 2560, Height: 1440, Refresh: 120}, {Width: 1920, Height: 1080, Refresh: 59.94}}}
	mode, ok := commonMode(a, b, 60)
	if !ok || mode.Width != 1920 || mode.Height != 1080 {
		t.Fatalf("commonMode = %#v, %t", mode, ok)
	}
}

func testDocument() string {
	mode := func(id string, width, height int, refresh float64) string {
		return fmt.Sprintf(`{"id":%q,"name":%q,"refreshRate":%f,"size":{"width":%d,"height":%d}}`, id, fmt.Sprintf("%dx%d@%.0f", width, height, refresh), refresh, width, height)
	}
	return fmt.Sprintf(`{"outputs":[
{"id":1,"uuid":"panel","name":"eDP-1","connected":true,"enabled":true,"type":7,"priority":1,"scale":1.5,"currentModeId":"p","preferredModes":["p"],"modes":[%s],"replicationSource":0,"pos":{"x":0,"y":0},"size":{"width":2560,"height":1600}},
{"id":2,"uuid":"external","name":"DP-1","connected":true,"enabled":false,"type":1,"priority":2,"scale":1,"currentModeId":"e","preferredModes":["e"],"modes":[%s],"replicationSource":0,"pos":{"x":0,"y":0},"size":{"width":2560,"height":1440}}
]}`, mode("p", 2560, 1600, 60), mode("e", 2560, 1440, 60))
}
