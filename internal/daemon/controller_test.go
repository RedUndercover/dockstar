package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/redundercover/dockstar/internal/config"
	"github.com/redundercover/dockstar/internal/display"
	"github.com/redundercover/dockstar/internal/inhibit"
)

type fakeProtector struct {
	ensured     []string
	releases    int
	releaseFail string
}

func (p *fakeProtector) Ensure(_ context.Context, phase string) inhibit.Status {
	p.ensured = append(p.ensured, phase)
	return inhibit.Status{Enabled: true, Active: true, Phase: phase, Backend: "logind", Mode: "block"}
}

func (p *fakeProtector) Release(context.Context) inhibit.Status {
	p.releases++
	return inhibit.Status{Phase: "inactive", Backend: "none", LastError: p.releaseFail}
}

func (p *fakeProtector) Close() error { return nil }

type fakeDisplayBackend struct {
	outputs       []display.Output
	snapshotErr   error
	applyErr      error
	applyCalls    int
	snapshotCalls int
	snapshotHook  func()
}

func (b *fakeDisplayBackend) Snapshot(context.Context, config.Config) (display.Snapshot, error) {
	b.snapshotCalls++
	if b.snapshotHook != nil {
		b.snapshotHook()
	}
	if b.snapshotErr != nil {
		return display.Snapshot{}, b.snapshotErr
	}
	return display.Snapshot{Outputs: append([]display.Output(nil), b.outputs...)}, nil
}

func (b *fakeDisplayBackend) Apply(_ context.Context, _ config.Config, layout string) (display.ApplyResult, error) {
	b.applyCalls++
	if b.applyErr != nil {
		return display.ApplyResult{}, b.applyErr
	}
	return display.ApplyResult{Layout: layout, Primary: "external"}, nil
}

func (b *fakeDisplayBackend) DPMSOn(context.Context) error { return nil }
func (b *fakeDisplayBackend) Name() string                 { return "fake" }

type fakePhysicalDisplayBackend struct {
	*fakeDisplayBackend
	physical      []display.Output
	physicalErr   error
	physicalCalls int
}

func (b *fakePhysicalDisplayBackend) PhysicalSnapshot(context.Context, config.Config) (display.Snapshot, error) {
	b.physicalCalls++
	if b.physicalErr != nil {
		return display.Snapshot{}, b.physicalErr
	}
	return display.Snapshot{Outputs: append([]display.Output(nil), b.physical...)}, nil
}

type fakePlatformActions struct{}

func (fakePlatformActions) Dock(context.Context, config.Config) error   { return nil }
func (fakePlatformActions) Undock(context.Context, config.Config) error { return nil }

type recordingPlatformActions struct {
	docks      int
	undocks    int
	undockErrs []error
}

func (a *recordingPlatformActions) Dock(context.Context, config.Config) error {
	a.docks++
	return nil
}

func (a *recordingPlatformActions) Undock(context.Context, config.Config) error {
	a.undocks++
	if len(a.undockErrs) == 0 {
		return nil
	}
	err := a.undockErrs[0]
	a.undockErrs = a.undockErrs[1:]
	return err
}

type blockingProtector struct {
	mu            sync.Mutex
	started       chan struct{}
	allowEnsure   chan struct{}
	releaseCalled chan struct{}
	startOnce     sync.Once
	releaseOnce   sync.Once
	operations    []string
}

func (p *blockingProtector) Ensure(_ context.Context, phase string) inhibit.Status {
	p.startOnce.Do(func() { close(p.started) })
	<-p.allowEnsure
	p.mu.Lock()
	p.operations = append(p.operations, "ensure:"+phase)
	p.mu.Unlock()
	return inhibit.Status{Enabled: true, Active: true, Phase: phase, Backend: "logind", Mode: "block"}
}

func (p *blockingProtector) Release(context.Context) inhibit.Status {
	p.releaseOnce.Do(func() { close(p.releaseCalled) })
	p.mu.Lock()
	p.operations = append(p.operations, "release")
	p.mu.Unlock()
	return inhibit.Status{Phase: "inactive", Backend: "none"}
}

func (*blockingProtector) Close() error { return nil }

type manualScheduler struct {
	actions map[time.Duration][]func()
}

func (s *manualScheduler) Schedule(delay time.Duration, action func()) {
	if s.actions == nil {
		s.actions = make(map[time.Duration][]func())
	}
	s.actions[delay] = append(s.actions[delay], action)
}

func (s *manualScheduler) Take(t *testing.T, delay time.Duration) func() {
	t.Helper()
	actions := s.actions[delay]
	if len(actions) == 0 {
		t.Fatalf("no action scheduled after %s", delay)
	}
	action := actions[0]
	s.actions[delay] = actions[1:]
	return action
}

func TestClosedUndockGuardRearmsOnRedockOrOpenLid(t *testing.T) {
	t.Parallel()
	var guard closedUndockGuard

	if !guard.ShouldRun(true, false, true, true) {
		t.Fatal("first closed-lid undock should run")
	}
	if guard.ShouldRun(false, false, true, true) {
		t.Fatal("repeated undocked evaluation should not rerun the safety action")
	}
	if guard.ShouldRun(false, true, true, true) {
		t.Fatal("redocking should only rearm the safety action")
	}
	if !guard.ShouldRun(true, false, true, true) {
		t.Fatal("closed-lid undock after redocking should run")
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

func TestControllerResumeGuardUsesWakeOrPreSleepState(t *testing.T) {
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

	controller.preSleepLidClosed = true
	controller.beginResumeProtection(context.Background())
	if len(protector.ensured) != 2 || protector.ensured[1] != "resume-grace" {
		t.Fatalf("closed-lid resume was not protected: %v", protector.ensured)
	}
}

func TestSuspendNowKeepsBypassOpenUntilResume(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	protector := &fakeProtector{}
	backend := &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), protector)
	controller.actions = fakePlatformActions{}
	controller.lidClosed = func(context.Context) (bool, error) { return false, nil }
	controller.wakePolicyEnabled = func(string) (bool, error) { return false, nil }
	var watchdog func()
	controller.schedule = func(delay time.Duration, action func()) {
		if delay != suspendRequestTimeout {
			t.Fatalf("watchdog delay = %s", delay)
		}
		watchdog = action
	}
	suspendCalls := 0
	controller.suspend = func(context.Context) error {
		suspendCalls++
		return nil
	}
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	ensuresBefore := len(protector.ensured)

	if err := controller.SuspendNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if suspendCalls != 1 || !controller.suspendBypassActive() {
		t.Fatalf("suspend calls=%d pending=%t", suspendCalls, controller.suspendBypassActive())
	}
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if len(protector.ensured) != ensuresBefore {
		t.Fatalf("protection reacquired during bypass: %v", protector.ensured)
	}

	controller.noteSleepStarted()
	controller.beginResumeProtection(context.Background())
	if controller.suspendBypassActive() {
		t.Fatal("suspend bypass remained active after resume")
	}
	if got := protector.ensured[len(protector.ensured)-1]; got != "resume-grace" {
		t.Fatalf("resume protection phase = %q", got)
	}
	watchdog()
}

func TestSuspendNowRestoresProtectionAfterRequestFailure(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	protector := &fakeProtector{}
	backend := &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), protector)
	controller.actions = fakePlatformActions{}
	controller.lidClosed = func(context.Context) (bool, error) { return false, nil }
	controller.suspend = func(context.Context) error { return errors.New("denied") }
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	ensuresBefore := len(protector.ensured)
	backend.snapshotErr = errors.New("KScreen unavailable")

	if err := controller.SuspendNow(context.Background()); err == nil {
		t.Fatal("suspend request unexpectedly succeeded")
	}
	if controller.suspendBypassActive() {
		t.Fatal("failed suspend left bypass active")
	}
	if len(protector.ensured) <= ensuresBefore {
		t.Fatalf("protection was not restored: %v", protector.ensured)
	}
}

func TestSuspendNowRefusesToSleepWhenProtectionReleaseIsUnconfirmed(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	protector := &fakeProtector{releaseFail: "confirmation timed out"}
	backend := &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), protector)
	controller.actions = fakePlatformActions{}
	controller.lidClosed = func(context.Context) (bool, error) { return false, nil }
	suspendCalls := 0
	controller.suspend = func(context.Context) error {
		suspendCalls++
		return nil
	}
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	ensuresBefore := len(protector.ensured)

	if err := controller.SuspendNow(context.Background()); err == nil {
		t.Fatal("suspend unexpectedly continued after an unconfirmed release")
	}
	if suspendCalls != 0 {
		t.Fatalf("suspend calls = %d, want 0", suspendCalls)
	}
	if controller.suspendBypassActive() {
		t.Fatal("release failure left suspend bypass active")
	}
	if len(protector.ensured) <= ensuresBefore {
		t.Fatalf("release failure did not restore protection: %v", protector.ensured)
	}
}

func TestSuspendWatchdogRestoresProtectionWhenSleepDoesNotStart(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	protector := &fakeProtector{}
	backend := &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), protector)
	controller.actions = fakePlatformActions{}
	controller.lidClosed = func(context.Context) (bool, error) { return false, nil }
	controller.suspend = func(context.Context) error { return nil }
	var watchdog func()
	controller.schedule = func(_ time.Duration, action func()) { watchdog = action }
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	ensuresBefore := len(protector.ensured)
	if err := controller.SuspendNow(context.Background()); err != nil {
		t.Fatal(err)
	}

	watchdog()
	if controller.suspendBypassActive() {
		t.Fatal("watchdog left bypass active")
	}
	if len(protector.ensured) <= ensuresBefore {
		t.Fatalf("watchdog did not restore protection: %v", protector.ensured)
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
	if controller.state.SleepProtection.Active || controller.state.SleepProtection.Enabled {
		t.Fatalf("published protection status was not disabled: %+v", controller.state.SleepProtection)
	}
}

func TestPhysicalSnapshotControlsProtectionOnEveryEvaluation(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	protector := &fakeProtector{}
	session := &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}}
	backend := &fakePhysicalDisplayBackend{
		fakeDisplayBackend: session,
		physical:           []display.Output{{DeviceID: "external", Connected: false, Approved: true}},
	}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), protector)
	controller.actions = fakePlatformActions{}
	controller.lidClosed = func(context.Context) (bool, error) { return false, nil }

	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if backend.physicalCalls != 1 {
		t.Fatalf("physical snapshot calls = %d, want 1", backend.physicalCalls)
	}
	if controller.state.Docked || session.applyCalls != 0 {
		t.Fatalf("state docked=%t apply calls=%d, want physically undocked without apply", controller.state.Docked, session.applyCalls)
	}
	if protector.releases == 0 {
		t.Fatal("physically undocked state did not release protection")
	}
}

func TestPhysicalUnplugReconcilesProtectionWhenSessionSnapshotFails(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	protector := &fakeProtector{}
	session := &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}}
	backend := &fakePhysicalDisplayBackend{
		fakeDisplayBackend: session,
		physical:           []display.Output{{DeviceID: "external", Connected: true, Approved: true}},
	}
	actions := &recordingPlatformActions{}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), protector)
	controller.actions = actions
	controller.lidClosed = func(context.Context) (bool, error) { return false, nil }
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}

	session.snapshotErr = errors.New("KScreen unavailable")
	backend.physical[0].Connected = false
	if err := controller.Evaluate(context.Background(), false); err == nil {
		t.Fatal("failed session snapshot unexpectedly succeeded")
	}
	if backend.physicalCalls != 2 {
		t.Fatalf("physical snapshot calls = %d, want 2", backend.physicalCalls)
	}
	if controller.state.Docked {
		t.Fatal("physical unplug was not reflected while the session backend failed")
	}
	if controller.state.SleepProtection.Active || protector.releases == 0 {
		t.Fatalf("sleep protection remained active: %+v", controller.state.SleepProtection)
	}
	if actions.undocks != 1 {
		t.Fatalf("physical unplug undock actions = %d, want 1", actions.undocks)
	}

	session.snapshotErr = nil
	session.outputs[0].Connected = false
	if err := controller.Evaluate(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if actions.undocks != 1 {
		t.Fatalf("undock actions reran after backend recovery: %d", actions.undocks)
	}
}

func TestSnapshotFailureProtectionExpiresWithoutPhysicalSnapshot(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	protector := &fakeProtector{}
	backend := &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), protector)
	controller.actions = fakePlatformActions{}
	controller.now = func() time.Time { return now }
	controller.lidClosed = func(context.Context) (bool, error) { return true, nil }
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}

	backend.snapshotErr = errors.New("KScreen unavailable")
	if err := controller.Evaluate(context.Background(), false); err == nil {
		t.Fatal("failed snapshot unexpectedly succeeded")
	}
	if got := protector.ensured[len(protector.ensured)-1]; got != "snapshot-grace" {
		t.Fatalf("failure protection phase = %q, want snapshot-grace", got)
	}
	ensuresAtFailure := len(protector.ensured)
	now = now.Add(snapshotFailureWindow + time.Second)
	if err := controller.Evaluate(context.Background(), false); err == nil {
		t.Fatal("expired failed snapshot unexpectedly succeeded")
	}
	releasesAfterExpiry := protector.releases
	if releasesAfterExpiry == 0 {
		t.Fatal("stale protection did not expire")
	}
	if err := controller.Evaluate(context.Background(), false); err == nil {
		t.Fatal("repeated failed snapshot unexpectedly succeeded")
	}
	if len(protector.ensured) != ensuresAtFailure {
		t.Fatalf("expired failure grace was rearmed: %v", protector.ensured)
	}
}

func TestOpenLidCancelsUndockGraceImmediately(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	lidClosed := true
	protector := &fakeProtector{}
	backend := &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), protector)
	controller.actions = fakePlatformActions{}
	controller.lidClosed = func(context.Context) (bool, error) { return lidClosed, nil }
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	backend.outputs[0].Connected = false
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if got := protector.ensured[len(protector.ensured)-1]; got != "undock-grace" {
		t.Fatalf("undock phase = %q, want undock-grace", got)
	}

	lidClosed = false
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if controller.state.SleepProtection.Active || protector.releases == 0 {
		t.Fatalf("open lid did not cancel grace: %+v", controller.state.SleepProtection)
	}
}

func TestFailedDisplayApplyIsRetriedForUnchangedSignature(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeDisplayBackend{
		outputs:  []display.Output{{DeviceID: "external", Connected: true, Approved: true}},
		applyErr: errors.New("apply failed"),
	}
	actions := &recordingPlatformActions{}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeProtector{})
	controller.actions = actions
	controller.lidClosed = func(context.Context) (bool, error) { return false, nil }

	if err := controller.Evaluate(context.Background(), false); err == nil {
		t.Fatal("first apply unexpectedly succeeded")
	}
	if err := controller.Evaluate(context.Background(), false); err == nil {
		t.Fatal("unchanged failed apply unexpectedly succeeded")
	}
	if backend.applyCalls != 2 {
		t.Fatalf("failed apply calls = %d, want 2", backend.applyCalls)
	}
	if actions.docks != 0 {
		t.Fatalf("dock actions ran before apply succeeded: %d", actions.docks)
	}
	backend.applyErr = nil
	if err := controller.Evaluate(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if backend.applyCalls != 3 {
		t.Fatalf("successful retry calls = %d, want 3", backend.applyCalls)
	}
	if actions.docks != 1 {
		t.Fatalf("dock actions after successful retry = %d, want 1", actions.docks)
	}
	if err := controller.Evaluate(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if backend.applyCalls != 3 {
		t.Fatalf("successful signature was not cached; apply calls = %d", backend.applyCalls)
	}
}

func TestDetectionDisableReleasesProtectionDuringBackendFailure(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	protector := &fakeProtector{}
	backend := &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), protector)
	controller.actions = fakePlatformActions{}
	controller.lidClosed = func(context.Context) (bool, error) { return true, nil }
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	backend.outputs[0].Connected = false
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if got := protector.ensured[len(protector.ensured)-1]; got != "undock-grace" {
		t.Fatalf("undock phase = %q, want undock-grace", got)
	}
	backend.snapshotErr = errors.New("KScreen unavailable")

	releasesBefore := protector.releases
	if err := controller.SetTrustPolicy("off"); err != nil {
		t.Fatal(err)
	}
	if protector.releases != releasesBefore+1 {
		t.Fatalf("detection disable releases = %d, want %d", protector.releases, releasesBefore+1)
	}
	if controller.state.SleepProtection.Active {
		t.Fatalf("detection disable did not publish immediate release: %+v", controller.state.SleepProtection)
	}
	if err := controller.Evaluate(context.Background(), false); err == nil {
		t.Fatal("failed snapshot unexpectedly succeeded")
	}
	if controller.state.Docked || controller.state.SleepProtection.Active {
		t.Fatalf("disabled detection retained dock protection: %+v", controller.state)
	}
}

func TestSuspendWaitsForInFlightEvaluationBeforeRelease(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	protector := &blockingProtector{
		started:       make(chan struct{}),
		allowEnsure:   make(chan struct{}),
		releaseCalled: make(chan struct{}),
	}
	backend := &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), protector)
	controller.actions = fakePlatformActions{}
	controller.lidClosed = func(context.Context) (bool, error) { return false, nil }
	controller.suspend = func(context.Context) error { return nil }
	controller.schedule = func(time.Duration, func()) {}

	evaluateDone := make(chan error, 1)
	go func() { evaluateDone <- controller.Evaluate(context.Background(), true) }()
	<-protector.started
	suspendDone := make(chan error, 1)
	suspendStarted := make(chan struct{})
	go func() {
		close(suspendStarted)
		suspendDone <- controller.SuspendNow(context.Background())
	}()
	<-suspendStarted
	select {
	case <-protector.releaseCalled:
		t.Fatal("suspend release raced ahead of the in-flight evaluation")
	case <-time.After(25 * time.Millisecond):
	}
	close(protector.allowEnsure)
	if err := <-evaluateDone; err != nil {
		t.Fatal(err)
	}
	if err := <-suspendDone; err != nil {
		t.Fatal(err)
	}

	protector.mu.Lock()
	defer protector.mu.Unlock()
	if len(protector.operations) != 2 || protector.operations[0] != "ensure:docked" || protector.operations[1] != "release" {
		t.Fatalf("protection operation order = %v", protector.operations)
	}
}

func TestInvalidConfigDoesNotReleaseActiveProtection(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	protector := &fakeProtector{}
	backend := &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), protector)
	controller.actions = fakePlatformActions{}
	controller.lidClosed = func(context.Context) (bool, error) { return false, nil }
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	releasesBefore := protector.releases
	next := store.Get()
	next.Detection = "invalid"
	raw, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.UpdateConfig(string(raw), next.Revision); err == nil {
		t.Fatal("invalid config unexpectedly succeeded")
	}
	if protector.releases != releasesBefore || !controller.state.SleepProtection.Active {
		t.Fatalf("invalid config changed active protection: releases=%d status=%+v", protector.releases, controller.state.SleepProtection)
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
	controller.rememberSignature("snapshot", id)
	currentID := controller.currentSignatureID()
	if !controller.shouldEvaluateSignature("snapshot", currentID, false) {
		t.Fatal("the first evaluation after invalidation must run")
	}
	controller.rememberSignature("snapshot", currentID)
	if controller.shouldEvaluateSignature("snapshot", currentID, false) {
		t.Fatal("an unchanged successful signature should be skipped once cached")
	}
}

type securityHarness struct {
	controller   *Controller
	store        *config.Store
	backend      *fakeDisplayBackend
	scheduler    *manualScheduler
	lidClosed    bool
	lockCalls    int
	suspendCalls int
}

func newSecurityHarness(t *testing.T) *securityHarness {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(func(cfg *config.Config) error {
		cfg.Detection = "all"
		cfg.Security.LockOnClosedUndock = true
		cfg.Security.SuspendOnClosedUndock = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	harness := &securityHarness{
		store:     store,
		backend:   &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}},
		scheduler: &manualScheduler{},
		lidClosed: true,
	}
	harness.controller = newController(store, harness.backend, slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeProtector{})
	harness.controller.actions = fakePlatformActions{}
	harness.controller.lidClosed = func(context.Context) (bool, error) { return harness.lidClosed, nil }
	harness.controller.lockScreen = func(context.Context) error {
		harness.lockCalls++
		return nil
	}
	harness.controller.suspend = func(context.Context) error {
		harness.suspendCalls++
		return nil
	}
	harness.controller.schedule = harness.scheduler.Schedule
	if err := harness.controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	harness.backend.outputs[0].Connected = false
	if err := harness.controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if harness.lockCalls != 1 {
		t.Fatalf("closed undock lock calls = %d, want 1", harness.lockCalls)
	}
	return harness
}

func TestAutomaticSuspendCancellationBoundaries(t *testing.T) {
	tests := []struct {
		name         string
		cancel       func(*testing.T, *securityHarness)
		wantSuspends int
	}{
		{
			name: "redock",
			cancel: func(t *testing.T, harness *securityHarness) {
				harness.backend.outputs[0].Connected = true
				if err := harness.controller.Evaluate(context.Background(), true); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "open lid",
			cancel: func(t *testing.T, harness *securityHarness) {
				harness.lidClosed = false
				if err := harness.controller.Evaluate(context.Background(), true); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "config update",
			cancel: func(t *testing.T, harness *securityHarness) {
				next := harness.store.Get()
				next.DefaultLayout = "mirror"
				raw, err := json.Marshal(next)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := harness.controller.UpdateConfig(string(raw), next.Revision); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "policy update",
			cancel: func(t *testing.T, harness *securityHarness) {
				if err := harness.controller.SetTrustPolicy("off"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "manual suspend",
			cancel: func(t *testing.T, harness *securityHarness) {
				if err := harness.controller.SuspendNow(context.Background()); err != nil {
					t.Fatal(err)
				}
			},
			wantSuspends: 1,
		},
		{
			name: "sleep start",
			cancel: func(_ *testing.T, harness *securityHarness) {
				harness.controller.noteSleepStarted()
			},
		},
		{
			name: "resume",
			cancel: func(_ *testing.T, harness *securityHarness) {
				harness.controller.wakePolicyEnabled = func(string) (bool, error) { return false, nil }
				harness.controller.beginResumeProtection(context.Background())
			},
		},
		{
			name: "shutdown",
			cancel: func(t *testing.T, harness *securityHarness) {
				harness.controller.sleepWatcher = func(ctx context.Context, _ chan<- struct{}) { <-ctx.Done() }
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if err := harness.controller.Run(ctx); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newSecurityHarness(t)
			automatic := harness.scheduler.Take(t, 500*time.Millisecond)
			test.cancel(t, harness)
			automatic()
			if harness.suspendCalls != test.wantSuspends {
				t.Fatalf("suspend calls = %d, want %d", harness.suspendCalls, test.wantSuspends)
			}
		})
	}
}

func TestAutomaticSuspendRevalidatesCurrentState(t *testing.T) {
	tests := []struct {
		name   string
		change func(*securityHarness)
	}{
		{
			name: "reconnect observed only by callback",
			change: func(harness *securityHarness) {
				harness.backend.outputs[0].Connected = true
			},
		},
		{
			name: "lid state unknown",
			change: func(harness *securityHarness) {
				harness.controller.lidClosed = func(context.Context) (bool, error) {
					return false, errors.New("lid unavailable")
				}
			},
		},
		{
			name: "display state unknown",
			change: func(harness *securityHarness) {
				harness.backend.snapshotErr = errors.New("backend unavailable")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newSecurityHarness(t)
			automatic := harness.scheduler.Take(t, 500*time.Millisecond)
			test.change(harness)
			automatic()
			if harness.suspendCalls != 0 {
				t.Fatalf("stale automatic suspend calls = %d, want 0", harness.suspendCalls)
			}
		})
	}
}

func TestAutomaticSuspendRechecksAfterCompositorDiscovery(t *testing.T) {
	tests := []struct {
		name   string
		change func(*securityHarness, *fakePhysicalDisplayBackend)
	}{
		{
			name: "hardware reconnect",
			change: func(_ *securityHarness, backend *fakePhysicalDisplayBackend) {
				backend.physical[0].Connected = true
			},
		},
		{
			name: "lid opens",
			change: func(harness *securityHarness, _ *fakePhysicalDisplayBackend) {
				harness.lidClosed = false
			},
		},
		{
			name: "sleep starts",
			change: func(harness *securityHarness, _ *fakePhysicalDisplayBackend) {
				harness.controller.noteSleepStarted()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newSecurityHarness(t)
			backend := &fakePhysicalDisplayBackend{
				fakeDisplayBackend: harness.backend,
				physical:           []display.Output{{DeviceID: "external", Approved: true}},
			}
			harness.controller.displays = backend
			harness.backend.snapshotHook = func() { test.change(harness, backend) }
			harness.scheduler.Take(t, 500*time.Millisecond)()
			if harness.suspendCalls != 0 {
				t.Fatalf("suspend after state changed during discovery: %d requests", harness.suspendCalls)
			}
		})
	}
}

func TestAutomaticSuspendRunsAfterGraceWhenStillUndocked(t *testing.T) {
	harness := newSecurityHarness(t)
	harness.scheduler.Take(t, 500*time.Millisecond)()
	if harness.suspendCalls != 1 {
		t.Fatalf("automatic suspend calls = %d, want 1", harness.suspendCalls)
	}
}

func TestClosedUndockSecuritySurvivesFailedLayout(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(func(cfg *config.Config) error {
		cfg.Detection = "all"
		cfg.Security.LockOnClosedUndock = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeDisplayBackend{
		outputs:  []display.Output{{DeviceID: "external", Connected: true, Approved: true}},
		applyErr: errors.New("layout failed"),
	}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeProtector{})
	controller.actions = fakePlatformActions{}
	controller.lidClosed = func(context.Context) (bool, error) { return true, nil }
	lockCalls := 0
	controller.lockScreen = func(context.Context) error {
		lockCalls++
		return nil
	}
	if err := controller.Evaluate(context.Background(), true); err == nil {
		t.Fatal("failed layout unexpectedly succeeded")
	}
	backend.outputs[0].Connected = false
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if lockCalls != 1 {
		t.Fatalf("lock calls after failed layout and unplug = %d, want 1", lockCalls)
	}
}

func TestClosedUndockSecurityUsesPhysicalUnplugDuringSnapshotFailure(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(func(cfg *config.Config) error {
		cfg.Detection = "all"
		cfg.Security.LockOnClosedUndock = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	session := &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}}
	backend := &fakePhysicalDisplayBackend{
		fakeDisplayBackend: session,
		physical:           []display.Output{{DeviceID: "external", Connected: true, Approved: true}},
	}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeProtector{})
	controller.actions = fakePlatformActions{}
	controller.lidClosed = func(context.Context) (bool, error) { return true, nil }
	lockCalls := 0
	controller.lockScreen = func(context.Context) error {
		lockCalls++
		return nil
	}
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	session.snapshotErr = errors.New("KScreen unavailable")
	backend.physical[0].Connected = false
	if err := controller.Evaluate(context.Background(), false); err == nil {
		t.Fatal("failed session snapshot unexpectedly succeeded")
	}
	if lockCalls != 1 {
		t.Fatalf("lock calls during physical unplug = %d, want 1", lockCalls)
	}
}

func TestFailedRestorationRetriesAndClearsStatus(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(func(cfg *config.Config) error {
		cfg.Detection = "all"
		cfg.Security.LockOnClosedUndock = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	restoreErr := errors.New("restore failed")
	actions := &recordingPlatformActions{undockErrs: []error{restoreErr, nil, restoreErr}}
	backend := &fakeDisplayBackend{outputs: []display.Output{{DeviceID: "external", Connected: true, Approved: true}}}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeProtector{})
	controller.actions = actions
	controller.lidClosed = func(context.Context) (bool, error) { return true, nil }
	lockCalls := 0
	controller.lockScreen = func(context.Context) error {
		lockCalls++
		return nil
	}
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	backend.outputs[0].Connected = false
	if err := controller.Evaluate(context.Background(), false); !errors.Is(err, restoreErr) {
		t.Fatalf("first restoration error = %v, want %v", err, restoreErr)
	}
	if !controller.actionsDocked || !controller.actionsUndockPending {
		t.Fatal("failed restoration did not remain pending")
	}
	if controller.state.LastError != restoreErr.Error() {
		t.Fatalf("restoration status error = %q, want %q", controller.state.LastError, restoreErr)
	}
	if lockCalls != 1 {
		t.Fatalf("security lock calls despite restoration failure = %d, want 1", lockCalls)
	}
	if err := controller.Evaluate(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if actions.undocks != 2 || controller.actionsDocked || controller.actionsUndockPending {
		t.Fatalf("restoration retry state: calls=%d docked=%t pending=%t", actions.undocks, controller.actionsDocked, controller.actionsUndockPending)
	}
	if controller.state.LastError != "" {
		t.Fatalf("successful restoration retained status error %q", controller.state.LastError)
	}

	backend.outputs[0].Connected = true
	if err := controller.Evaluate(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	backend.outputs[0].Connected = false
	if err := controller.Evaluate(context.Background(), false); !errors.Is(err, restoreErr) {
		t.Fatalf("second restoration error = %v, want %v", err, restoreErr)
	}
	backend.outputs[0].Connected = true
	if err := controller.Evaluate(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if actions.docks != 3 {
		t.Fatalf("dock actions after redocking with pending restoration = %d, want 3", actions.docks)
	}
}

func TestControllerResolvesAndCachesApprovedSessionFallback(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	const fallbackID = "kde:fallback-display"
	_, err = store.Update(func(cfg *config.Config) error {
		cfg.Displays = append(cfg.Displays, config.DisplayRule{ID: fallbackID, Label: "Fallback", Allowed: true, Scale: 1, Mode: "preferred", Position: "right"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	session := &fakeDisplayBackend{outputs: []display.Output{{
		DeviceID:  fallbackID,
		Connector: "DP-1",
		Connected: true,
		Approved:  true,
	}}}
	backend := &fakePhysicalDisplayBackend{
		fakeDisplayBackend: session,
		physical: []display.Output{{
			Connector: "DP-1",
			Connected: true,
		}},
	}
	controller := newController(store, backend, slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeProtector{})
	controller.actions = fakePlatformActions{}
	controller.lidClosed = func(context.Context) (bool, error) { return false, nil }
	if err := controller.Evaluate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !controller.state.Docked || len(controller.state.Outputs) != 1 || !controller.state.Outputs[0].Approved {
		t.Fatalf("live fallback state = %+v", controller.state)
	}

	session.snapshotErr = errors.New("KScreen unavailable")
	if err := controller.Evaluate(context.Background(), false); err == nil {
		t.Fatal("failed session snapshot unexpectedly succeeded")
	}
	if !controller.state.Docked || len(controller.state.Outputs) != 1 {
		t.Fatalf("cached fallback lost dock state: %+v", controller.state)
	}
	output := controller.state.Outputs[0]
	if output.DeviceID != fallbackID || !output.Approved {
		t.Fatalf("cached fallback output = %+v", output)
	}
}
