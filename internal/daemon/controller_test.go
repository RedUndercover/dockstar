package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/redundercover/dockstar/internal/config"
	"github.com/redundercover/dockstar/internal/display"
	"github.com/redundercover/dockstar/internal/inhibit"
)

type fakeProtector struct {
	ensured  []string
	releases int
}

func (p *fakeProtector) Ensure(_ context.Context, phase string) inhibit.Status {
	p.ensured = append(p.ensured, phase)
	return inhibit.Status{Enabled: true, Active: true, Phase: phase, Backend: "powerdevil"}
}

func (p *fakeProtector) Release(context.Context) inhibit.Status {
	p.releases++
	return inhibit.Status{Phase: "inactive", Backend: "none"}
}

func (p *fakeProtector) Close() error { return nil }

type fakeDisplayBackend struct {
	outputs []display.Output
}

func (b *fakeDisplayBackend) Snapshot(context.Context, config.Config) (display.Snapshot, error) {
	return display.Snapshot{Outputs: append([]display.Output(nil), b.outputs...)}, nil
}

func (b *fakeDisplayBackend) Apply(_ context.Context, _ config.Config, layout string) (display.ApplyResult, error) {
	return display.ApplyResult{Layout: layout, Primary: "external"}, nil
}

func (b *fakeDisplayBackend) DPMSOn(context.Context) error { return nil }
func (b *fakeDisplayBackend) Name() string                 { return "fake" }

type fakePlatformActions struct{}

func (fakePlatformActions) Dock(context.Context, config.Config) error   { return nil }
func (fakePlatformActions) Undock(context.Context, config.Config) error { return nil }

func TestClosedUndockGuardRunsOnceUntilLidOpens(t *testing.T) {
	t.Parallel()
	var guard closedUndockGuard

	if !guard.ShouldRun(true, false, true, true) {
		t.Fatal("first closed-lid undock should run")
	}
	if guard.ShouldRun(false, true, true, true) {
		t.Fatal("redocking should not run the safety action")
	}
	if guard.ShouldRun(true, false, true, true) {
		t.Fatal("dock flapping must not retrigger the safety action")
	}
	if guard.ShouldRun(false, false, false, true) {
		t.Fatal("opening the lid should only rearm the guard")
	}
	if !guard.ShouldRun(true, false, true, true) {
		t.Fatal("a later closed-lid undock should run after the lid was opened")
	}
}

func TestClosedUndockGuardHonorsDisabledPolicy(t *testing.T) {
	t.Parallel()
	var guard closedUndockGuard
	if guard.ShouldRun(true, false, true, false) {
		t.Fatal("disabled policy should not run")
	}
}

func TestControllerProtectsDockAndDebouncesClosedLidUndock(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(func(cfg *config.Config) error {
		cfg.Detection = "all"
		cfg.Audio.Policy = "unchanged"
		cfg.Network.Policy = "unchanged"
		cfg.Power.OnACProfile = "unchanged"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1_700_000_000, 0)
	lidClosed := false
	protector := &fakeProtector{}
	backend := &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), protector)
	controller.actions = fakePlatformActions{}
	controller.now = func() time.Time { return now }
	controller.lidClosed = func(context.Context) (bool, error) { return lidClosed, nil }

	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if got := protector.ensured[len(protector.ensured)-1]; got != "docked" {
		t.Fatalf("dock protection phase = %q", got)
	}

	lidClosed = true
	backend.outputs[0].Connected = false
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if got := protector.ensured[len(protector.ensured)-1]; got != "undock-grace" {
		t.Fatalf("undock protection phase = %q", got)
	}

	now = now.Add(undockProtectionWindow + time.Second)
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if protector.releases == 0 {
		t.Fatal("expired undock protection was not released")
	}
}

func TestControllerResumeGuardRequiresWakePolicy(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	protector := &fakeProtector{}
	controller := newController(store, &fakeDisplayBackend{}, slog.New(slog.NewTextHandler(io.Discard, nil)), protector)
	controller.wakePolicyEnabled = func(string) (bool, error) { return true, nil }
	controller.beginResumeProtection(context.Background())
	if len(protector.ensured) != 1 || protector.ensured[0] != "resume-grace" {
		t.Fatalf("resume phases = %v", protector.ensured)
	}

	controller.wakePolicyEnabled = func(string) (bool, error) { return false, nil }
	controller.clearProtectionWindows()
	controller.beginResumeProtection(context.Background())
	if len(protector.ensured) != 1 {
		t.Fatalf("disabled wake policy acquired protection: %v", protector.ensured)
	}
}

func TestUpdateConfigDisablesProtectionImmediately(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	protector := &fakeProtector{}
	controller := newController(store, &fakeDisplayBackend{}, slog.New(slog.NewTextHandler(io.Discard, nil)), protector)
	next := store.Get()
	next.Power.KeepAwakeWhileDocked = false
	raw, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.UpdateConfig(string(raw), next.Revision); err != nil {
		t.Fatal(err)
	}
	if protector.releases != 1 {
		t.Fatalf("protection releases = %d, want 1", protector.releases)
	}
}

func TestSignatureInvalidationIsNotLostByInFlightEvaluation(t *testing.T) {
	t.Parallel()
	controller := &Controller{}
	id := controller.currentSignatureID()
	controller.invalidateSignature()

	if !controller.shouldEvaluateSignature("snapshot", id, false) {
		t.Fatal("an evaluation started before invalidation must not be skipped")
	}
	currentID := controller.currentSignatureID()
	if !controller.shouldEvaluateSignature("snapshot", currentID, false) {
		t.Fatal("the first evaluation after invalidation must run")
	}
	if controller.shouldEvaluateSignature("snapshot", currentID, false) {
		t.Fatal("an unchanged signature should be skipped once cached")
	}
}
