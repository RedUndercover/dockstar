package inhibit

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestRealCoordinatorIntegration(t *testing.T) {
	if os.Getenv("DOCKSTAR_INHIBIT_INTEGRATION") != "1" {
		t.Skip("set DOCKSTAR_INHIBIT_INTEGRATION=1 to exercise the live logind inhibitor")
	}
	coordinator := New()
	t.Cleanup(func() { _ = coordinator.Close() })
	status := coordinator.Ensure(context.Background(), "docked")
	if !status.Active || status.Backend != "logind" || status.Mode != "block" {
		t.Fatalf("live status = %+v", status)
	}
	pid := uint32(os.Getpid())
	if !liveInhibitorPresent(t, pid) {
		t.Fatal("Dockstar logind inhibitor is not listed after acquisition")
	}
	if state := liveCanSuspend(t); !strings.Contains(state, "inhibit") {
		t.Fatalf("CanSuspend with active Dockstar lease = %q, want an inhibited state", state)
	}
	time.Sleep(7 * time.Second)
	status = coordinator.Ensure(context.Background(), "docked")
	if !status.Active || status.Backend != "logind" || status.AcquiredAt == "" {
		t.Fatalf("status after former promotion window = %+v", status)
	}
	if !liveInhibitorPresent(t, pid) {
		t.Fatal("Dockstar logind inhibitor disappeared after the former promotion window")
	}
	coordinator.Release(context.Background())
	if liveInhibitorPresent(t, pid) {
		t.Fatal("Dockstar logind inhibitor remained listed after release")
	}
}

func liveCanSuspend(t *testing.T) string {
	t.Helper()
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var state string
	if err := conn.Object(loginService, loginPath).Call(
		loginAPI+".CanSuspend", 0,
	).Store(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func liveInhibitorPresent(t *testing.T, pid uint32) bool {
	t.Helper()
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var entries []inhibitorRecord
	if err := conn.Object(loginService, loginPath).Call(
		loginAPI+".ListInhibitors", 0,
	).Store(&entries); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.What == inhibitWhat && entry.Who == applicationName && entry.Mode == inhibitMode && entry.PID == pid {
			return true
		}
	}
	return false
}

type fakeLease struct {
	valid    bool
	released int
}

func (l *fakeLease) Valid(context.Context) bool { return l.valid }
func (l *fakeLease) Release(context.Context) error {
	l.released++
	return nil
}

type fakeFactory struct {
	leases  []*fakeLease
	err     error
	acquire int
}

func (f *fakeFactory) Acquire(context.Context) (lease, error) {
	f.acquire++
	if f.err != nil {
		return nil, f.err
	}
	value := &fakeLease{valid: true}
	f.leases = append(f.leases, value)
	return value, nil
}

func TestCoordinatorKeepsAuthoritativeLeaseAcrossPhases(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	factory := &fakeFactory{}
	coordinator := newCoordinator(factory, func() time.Time { return now })

	first := coordinator.Ensure(context.Background(), "docked")
	now = now.Add(30 * time.Second)
	second := coordinator.Ensure(context.Background(), "resume-grace")

	if factory.acquire != 1 {
		t.Fatalf("acquisitions = %d, want 1", factory.acquire)
	}
	if !first.Active || !second.Active || second.Backend != "logind" || second.Phase != "resume-grace" {
		t.Fatalf("statuses = first=%+v second=%+v", first, second)
	}
	if first.AcquiredAt != second.AcquiredAt {
		t.Fatalf("lease changed across phases: %q != %q", first.AcquiredAt, second.AcquiredAt)
	}
}

func TestCoordinatorReacquiresInvalidLease(t *testing.T) {
	t.Parallel()
	factory := &fakeFactory{}
	coordinator := newCoordinator(factory, time.Now)
	coordinator.Ensure(context.Background(), "docked")
	factory.leases[0].valid = false

	status := coordinator.Ensure(context.Background(), "docked")
	if !status.Active || factory.acquire != 2 {
		t.Fatalf("status=%+v acquisitions=%d", status, factory.acquire)
	}
	if factory.leases[0].released != 1 {
		t.Fatalf("invalid lease releases = %d, want 1", factory.leases[0].released)
	}
}

func TestCoordinatorReportsAcquisitionFailureAsInactive(t *testing.T) {
	t.Parallel()
	coordinator := newCoordinator(&fakeFactory{err: errors.New("denied")}, time.Now)
	status := coordinator.Ensure(context.Background(), "docked")
	if status.Active || status.Backend != "none" || !status.Degraded || status.LastError == "" {
		t.Fatalf("failure status = %+v", status)
	}
}

func TestCoordinatorReleasesLease(t *testing.T) {
	t.Parallel()
	factory := &fakeFactory{}
	coordinator := newCoordinator(factory, time.Now)
	coordinator.Ensure(context.Background(), "docked")

	status := coordinator.Release(context.Background())
	if status.Active || status.Enabled || status.Phase != "inactive" || status.Backend != "none" {
		t.Fatalf("released status = %+v", status)
	}
	if factory.leases[0].released != 1 {
		t.Fatalf("lease releases = %d, want 1", factory.leases[0].released)
	}
}

func TestInhibitsSleepRecognizesColonSeparatedMembership(t *testing.T) {
	t.Parallel()
	for _, what := range []string{"sleep", "sleep:idle:handle-lid-switch", "idle:sleep"} {
		if !inhibitsSleep(what) {
			t.Errorf("inhibitsSleep(%q) = false", what)
		}
	}
	for _, what := range []string{"", "idle", "handle-lid-switch"} {
		if inhibitsSleep(what) {
			t.Errorf("inhibitsSleep(%q) = true", what)
		}
	}
}

func TestParseSystemdMajorVersion(t *testing.T) {
	t.Parallel()
	tests := map[string]int{
		"257":        257,
		"261-1-arch": 261,
		"258.2":      258,
	}
	for input, want := range tests {
		input, want := input, want
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			got, err := parseSystemdMajorVersion(input)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("parseSystemdMajorVersion(%q) = %d, want %d", input, got, want)
			}
		})
	}
	if _, err := parseSystemdMajorVersion("unknown"); err == nil {
		t.Fatal("invalid systemd version was accepted")
	}
}
