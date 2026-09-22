package startup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redundercover/dockstar/internal/config"
	"github.com/redundercover/dockstar/internal/display"
	"github.com/redundercover/dockstar/internal/inhibit"
)

const (
	approvedEDID = "edid:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	secondEDID   = "edid:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

type fakeProtector struct {
	active       bool
	ensureCalls  int
	releaseCalls int
}

func (f *fakeProtector) Ensure(context.Context, string) inhibit.Status {
	f.ensureCalls++
	f.active = true
	return inhibit.Status{Enabled: true, Active: true, Backend: "fake"}
}

func (f *fakeProtector) Release(context.Context) inhibit.Status {
	f.releaseCalls++
	f.active = false
	return inhibit.Status{}
}

func TestHelperPersistsOnlyExplicitEDIDApprovals(t *testing.T) {
	cfg := config.Default()
	cfg.Detection = "all"
	cfg.Displays = []config.DisplayRule{
		displayRule(secondEDID, true),
		displayRule("kde:session-local-identity", true),
		displayRule(approvedEDID, true),
		displayRule("connector:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", true),
		displayRule("edid:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", false),
	}
	input, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}

	policyPath := filepath.Join(t.TempDir(), "dockstar", "startup.json")
	systemctlCalls := 0
	err = helper("enable", bytes.NewReader(input), helperDeps{
		policyPath: policyPath,
		geteuid:    func() int { return 0 },
		chown:      func(string, int, int) error { return nil },
		systemctl:  func() error { systemctlCalls++; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	configured, installed, err := loadPolicy(policyPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if !installed || !configured.Enabled || configured.Detection != "allowlist" || !configured.KeepAwakeWhileDocked {
		t.Fatalf("persisted policy = %#v", configured)
	}
	if got, want := strings.Join(configured.AllowedEDIDs, ","), approvedEDID+","+secondEDID; got != want {
		t.Fatalf("allowed EDIDs = %q, want %q", got, want)
	}
	if systemctlCalls != 1 {
		t.Fatalf("systemctl calls = %d, want 1", systemctlCalls)
	}
	info, err := os.Stat(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("policy mode = %o, want 644", got)
	}
}

func TestHelperRejectsUnsafeEnrollment(t *testing.T) {
	base := config.Default()
	base.Displays = []config.DisplayRule{displayRule(approvedEDID, true)}

	tests := []struct {
		name   string
		change func(*config.Config)
	}{
		{name: "detection off", change: func(cfg *config.Config) { cfg.Detection = "off" }},
		{name: "keep awake off", change: func(cfg *config.Config) { cfg.Power.KeepAwakeWhileDocked = false }},
		{name: "no EDID approvals", change: func(cfg *config.Config) { cfg.Displays = []config.DisplayRule{displayRule("kde:temporary", true)} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := base
			test.change(&cfg)
			input, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			err = helper("enable", bytes.NewReader(input), helperDeps{
				policyPath: filepath.Join(t.TempDir(), "startup.json"),
				geteuid:    func() int { return 0 },
				chown:      func(string, int, int) error { return nil },
				systemctl:  func() error { t.Fatal("systemctl must not run for rejected enrollment"); return nil },
			})
			if err == nil {
				t.Fatal("unsafe enrollment was accepted")
			}
		})
	}
}

func TestHelperSyncDisablesPolicyAfterFinalApprovalIsRevoked(t *testing.T) {
	cfg := config.Default()
	cfg.Detection = "allowlist"
	cfg.Displays = []config.DisplayRule{displayRule(approvedEDID, true)}
	desired := DesiredStatus(cfg)
	if !desired.Enabled || desired.Installed || desired.Detection != "allowlist" ||
		len(desired.AllowedEDIDs) != 1 || desired.AllowedEDIDs[0] != approvedEDID {
		t.Fatalf("desired enabled status = %#v", desired)
	}
	policyPath := filepath.Join(t.TempDir(), "startup.json")
	systemctlCalls := 0
	deps := helperDeps{
		policyPath: policyPath,
		geteuid:    func() int { return 0 },
		chown:      func(string, int, int) error { return nil },
		systemctl:  func() error { systemctlCalls++; return nil },
	}
	encode := func(cfg config.Config) io.Reader {
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return bytes.NewReader(data)
	}

	if err := helper("enable", encode(cfg), deps); err != nil {
		t.Fatal(err)
	}
	cfg.Displays = nil
	desired = DesiredStatus(cfg)
	if desired.Enabled || desired.Installed || desired.Detection != "off" || len(desired.AllowedEDIDs) != 0 {
		t.Fatalf("desired revoked status = %#v", desired)
	}
	if err := helper("sync", encode(cfg), deps); err != nil {
		t.Fatal(err)
	}

	configured, installed, err := loadPolicy(policyPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if !installed || configured.Enabled || configured.Detection != "off" || len(configured.AllowedEDIDs) != 0 {
		t.Fatalf("synced policy = %#v, want installed disabled policy", configured)
	}
	if systemctlCalls != 1 {
		t.Fatalf("systemctl calls = %d, want only the initial enable", systemctlCalls)
	}
}

func TestHelperRejectsNonRootAndOversizedInput(t *testing.T) {
	if err := helper("disable", nil, helperDeps{
		policyPath: filepath.Join(t.TempDir(), "startup.json"),
		geteuid:    func() int { return 1000 },
		chown:      func(string, int, int) error { return nil },
		systemctl:  func() error { return nil },
	}); err == nil {
		t.Fatal("non-root helper invocation was accepted")
	}

	err := helper("enable", io.LimitReader(strings.NewReader(strings.Repeat("x", maxConfigBytes+2)), maxConfigBytes+2), helperDeps{
		policyPath: filepath.Join(t.TempDir(), "startup.json"),
		geteuid:    func() int { return 0 },
		chown:      func(string, int, int) error { return nil },
		systemctl:  func() error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized input error = %v", err)
	}
}

func TestGuardProtectsOnlyApprovedConnectedExternal(t *testing.T) {
	configured, err := policyFromConfig(config.Config{
		SchemaVersion: config.SchemaVersion,
		Revision:      1,
		Detection:     "allowlist",
		Power:         config.Power{KeepAwakeWhileDocked: true},
		Displays:      []config.DisplayRule{displayRule(approvedEDID, true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	protection := &fakeProtector{}
	runner := newTestGuard(protection)
	runner.deps.readPolicy = func() (policy, bool, error) { return configured, true, nil }
	runner.deps.snapshot = func(_ context.Context, cfg config.Config) (display.Snapshot, error) {
		if cfg.Detection != "allowlist" {
			t.Fatalf("snapshot detection = %q", cfg.Detection)
		}
		rule, ok := cfg.Rule(approvedEDID)
		if !ok || !rule.Allowed || len(cfg.Displays) != 1 {
			t.Fatalf("snapshot allowlist = %#v", cfg.Displays)
		}
		return display.Snapshot{Outputs: []display.Output{
			{DeviceID: approvedEDID, Connected: true, Approved: true},
			{DeviceID: secondEDID, Connected: true, Approved: false},
		}}, nil
	}

	runner.reconcile(context.Background())
	if !protection.active || protection.ensureCalls != 1 {
		t.Fatalf("protection = %#v, want active approved-device inhibitor", protection)
	}
}

func TestGuardDoesNotProtectUnknownDock(t *testing.T) {
	protection := &fakeProtector{active: true}
	runner := newTestGuard(protection)
	runner.deps.snapshot = func(context.Context, config.Config) (display.Snapshot, error) {
		return display.Snapshot{Outputs: []display.Output{{
			DeviceID: secondEDID, Connected: true, Approved: false,
		}}}, nil
	}

	runner.reconcile(context.Background())
	if protection.active || protection.ensureCalls != 0 || protection.releaseCalls != 1 {
		t.Fatalf("unknown-dock protection = %#v, want released without acquisition", protection)
	}
}

func TestGuardReleasesOnDisconnectAndDetectionError(t *testing.T) {
	configured := testPolicy()
	protection := &fakeProtector{active: true}
	runner := newTestGuard(protection)
	runner.deps.readPolicy = func() (policy, bool, error) { return configured, true, nil }
	runner.deps.snapshot = func(context.Context, config.Config) (display.Snapshot, error) {
		return display.Snapshot{Outputs: []display.Output{{DeviceID: approvedEDID, Connected: false, Approved: true}}}, nil
	}
	runner.reconcile(context.Background())
	if protection.active || protection.releaseCalls != 1 {
		t.Fatalf("disconnect protection = %#v, want released", protection)
	}

	protection.active = true
	runner.deps.snapshot = func(context.Context, config.Config) (display.Snapshot, error) {
		return display.Snapshot{}, errors.New("unreadable EDID")
	}
	runner.reconcile(context.Background())
	if protection.active || protection.releaseCalls != 2 {
		t.Fatalf("detection-error protection = %#v, want released", protection)
	}
}

func TestGuardWaitsForSlowGraphicalSessionReadiness(t *testing.T) {
	now := time.Date(2026, time.September, 22, 10, 0, 0, 0, time.UTC)
	protection := &fakeProtector{}
	runner := newTestGuard(protection)
	runner.deps.now = func() time.Time { return now }
	runner.deps.graphicalSessions = func(context.Context) ([]graphicalSession, error) {
		return []graphicalSession{{ID: "c1", UID: 1000}}, nil
	}

	runner.reconcile(context.Background())
	now = now.Add(14 * time.Second)
	runner.reconcile(context.Background())

	if !protection.active || protection.ensureCalls != 2 || protection.releaseCalls != 0 {
		t.Fatalf("slow-session protection = %#v, want active throughout bounded wait", protection)
	}
	now = now.Add(time.Second)
	runner.reconcile(context.Background())
	if protection.active {
		t.Fatal("startup protection remained active after the original 15-second deadline")
	}
}

func TestGuardHandsOffWhenGraphicalSessionInhibitorIsReady(t *testing.T) {
	protection := &fakeProtector{active: true}
	runner := newTestGuard(protection)
	runner.deps.graphicalSessions = func(context.Context) ([]graphicalSession, error) {
		return []graphicalSession{{ID: "c1", UID: 1000}}, nil
	}
	ready := true
	runner.deps.inhibitors = func(context.Context) ([]logindInhibitor, error) {
		if !ready {
			return nil, nil
		}
		return []logindInhibitor{{
			What: "sleep:idle", Who: "Dockstar", Mode: "block", UID: 1000, PID: 2000,
		}}, nil
	}

	runner.reconcile(context.Background())
	ready = false
	runner.reconcile(context.Background())

	if protection.active || protection.ensureCalls != 0 || protection.releaseCalls != 2 {
		t.Fatalf("ready-session protection = %#v, want permanent handoff for session c1", protection)
	}
}

func TestGuardTimeoutDoesNotRearmWithinSameSession(t *testing.T) {
	now := time.Date(2026, time.September, 22, 10, 0, 0, 0, time.UTC)
	configured := testPolicy()
	protection := &fakeProtector{}
	runner := newTestGuard(protection)
	runner.deps.now = func() time.Time { return now }
	runner.deps.readPolicy = func() (policy, bool, error) { return configured, true, nil }
	runner.deps.graphicalSessions = func(context.Context) ([]graphicalSession, error) {
		return []graphicalSession{{ID: "c1", UID: 1000}}, nil
	}
	runner.deps.inhibitors = func(context.Context) ([]logindInhibitor, error) {
		return nil, errors.New("ListInhibitors unavailable")
	}

	runner.reconcile(context.Background())
	now = now.Add(sessionHandoffTimeout)
	runner.reconcile(context.Background())
	configured = disabledPolicy()
	runner.reconcile(context.Background())
	configured = testPolicy()
	now = now.Add(time.Second)
	runner.reconcile(context.Background())

	if protection.active || protection.ensureCalls != 1 {
		t.Fatalf("timed-out protection = %#v, want no reacquisition for session c1", protection)
	}
}

func TestGuardLogoutResetsHandoffForNewSession(t *testing.T) {
	now := time.Date(2026, time.September, 22, 10, 0, 0, 0, time.UTC)
	current := []graphicalSession{{ID: "c1", UID: 1000}}
	protection := &fakeProtector{}
	runner := newTestGuard(protection)
	runner.deps.now = func() time.Time { return now }
	runner.deps.graphicalSessions = func(context.Context) ([]graphicalSession, error) {
		return current, nil
	}

	runner.reconcile(context.Background())
	now = now.Add(sessionHandoffTimeout)
	runner.reconcile(context.Background())
	current = nil
	now = now.Add(time.Second)
	runner.reconcile(context.Background())
	if !protection.active {
		t.Fatal("startup protection did not reacquire after the handed-off session exited")
	}

	current = []graphicalSession{{ID: "c2", UID: 1001}}
	now = now.Add(14 * time.Second)
	runner.reconcile(context.Background())
	now = now.Add(14 * time.Second)
	runner.reconcile(context.Background())
	if !protection.active {
		t.Fatal("new session inherited the prior session's expired deadline")
	}
	now = now.Add(time.Second)
	runner.reconcile(context.Background())
	if protection.active {
		t.Fatal("new session did not hand off at its own deadline")
	}
}

func TestGuardRejectsWrongUIDAndOwnInhibitor(t *testing.T) {
	protection := &fakeProtector{}
	runner := newTestGuard(protection)
	runner.deps.ownPID = 42
	runner.deps.graphicalSessions = func(context.Context) ([]graphicalSession, error) {
		return []graphicalSession{{ID: "c1", UID: 1000}}, nil
	}
	runner.deps.inhibitors = func(context.Context) ([]logindInhibitor, error) {
		return []logindInhibitor{
			{What: "sleep", Who: "Dockstar", Mode: "block", UID: 1001, PID: 84},
			{What: "sleep", Who: "Dockstar", Mode: "block", UID: 1000, PID: 42},
			{What: "sleep", Who: "Dockstar", Mode: "delay", UID: 1000, PID: 85},
			{What: "idle", Who: "Dockstar", Mode: "block", UID: 1000, PID: 86},
		}, nil
	}

	runner.reconcile(context.Background())
	if !protection.active || protection.ensureCalls != 1 || protection.releaseCalls != 0 {
		t.Fatalf("unrelated inhibitors changed protection: %#v", protection)
	}
}

func TestGuardReleasesOnUnplugWhileWaitingForSession(t *testing.T) {
	protection := &fakeProtector{active: true}
	runner := newTestGuard(protection)
	runner.deps.graphicalSessions = func(context.Context) ([]graphicalSession, error) {
		return []graphicalSession{{ID: "c1", UID: 1000}}, nil
	}
	runner.deps.snapshot = func(context.Context, config.Config) (display.Snapshot, error) {
		return display.Snapshot{Outputs: []display.Output{{
			DeviceID: approvedEDID, Connected: false, Approved: true,
		}}}, nil
	}

	runner.reconcile(context.Background())
	if protection.active || protection.ensureCalls != 0 || protection.releaseCalls != 1 {
		t.Fatalf("unplugged waiting protection = %#v, want immediate release", protection)
	}
}

func TestGuardReleasesImmediatelyWhenPolicyIsDisabledDuringWait(t *testing.T) {
	protection := &fakeProtector{active: true}
	runner := newTestGuard(protection)
	runner.deps.readPolicy = func() (policy, bool, error) { return disabledPolicy(), true, nil }
	runner.deps.graphicalSessions = func(context.Context) ([]graphicalSession, error) {
		return []graphicalSession{{ID: "c1", UID: 1000}}, nil
	}
	runner.deps.inhibitors = func(context.Context) ([]logindInhibitor, error) {
		t.Fatal("disabled policy must release before checking user-session readiness")
		return nil, nil
	}
	runner.deps.snapshot = func(context.Context, config.Config) (display.Snapshot, error) {
		t.Fatal("disabled policy must release before physical detection")
		return display.Snapshot{}, nil
	}

	runner.reconcile(context.Background())
	if protection.active || protection.releaseCalls != 1 {
		t.Fatalf("disabled waiting protection = %#v, want immediate release", protection)
	}
}

func TestGuardReleasesWhenLogindStateIsUnknown(t *testing.T) {
	protection := &fakeProtector{active: true}
	runner := newTestGuard(protection)
	runner.deps.graphicalSessions = func(context.Context) ([]graphicalSession, error) {
		return nil, errors.New("logind unavailable")
	}

	runner.reconcile(context.Background())
	if protection.active || protection.releaseCalls != 1 {
		t.Fatalf("logind-error protection = %#v, want fail-safe release", protection)
	}
}

func displayRule(id string, allowed bool) config.DisplayRule {
	return config.DisplayRule{ID: id, Allowed: allowed, Scale: 1, Mode: "preferred", Position: "right"}
}

func testPolicy() policy {
	return policy{
		Version:              policyVersion,
		Enabled:              true,
		Detection:            "allowlist",
		KeepAwakeWhileDocked: true,
		AllowedEDIDs:         []string{approvedEDID},
	}
}

func newTestGuard(protection protector) *guard {
	now := time.Date(2026, time.September, 22, 10, 0, 0, 0, time.UTC)
	return &guard{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessions: make(map[string]observedSession),
		deps: runDeps{
			readPolicy:        func() (policy, bool, error) { return testPolicy(), true, nil },
			graphicalSessions: func(context.Context) ([]graphicalSession, error) { return nil, nil },
			inhibitors:        func(context.Context) ([]logindInhibitor, error) { return nil, nil },
			snapshot: func(context.Context, config.Config) (display.Snapshot, error) {
				return display.Snapshot{Outputs: []display.Output{{
					DeviceID: approvedEDID, Connected: true, Approved: true,
				}}}, nil
			},
			protector: protection,
			now:       func() time.Time { return now },
			ownPID:    99,
		},
	}
}
