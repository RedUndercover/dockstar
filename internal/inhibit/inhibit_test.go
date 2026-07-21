package inhibit

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestRealCoordinatorIntegration(t *testing.T) {
	if os.Getenv("DOCKSTAR_INHIBIT_INTEGRATION") != "1" {
		t.Skip("set DOCKSTAR_INHIBIT_INTEGRATION=1 to exercise live D-Bus inhibitors")
	}
	coordinator := New()
	t.Cleanup(func() { _ = coordinator.Close() })
	status := coordinator.Ensure(context.Background(), "docked")
	if !status.Active || status.Backend != "logind" {
		t.Fatalf("warmup status = %+v", status)
	}
	time.Sleep(nativeWarmup + 500*time.Millisecond)
	status = coordinator.Ensure(context.Background(), "docked")
	if !status.Active || status.Backend != "powerdevil" || status.Degraded {
		t.Fatalf("enforced status = %+v", status)
	}
}

type fakeLease struct {
	valid     bool
	active    bool
	activeErr error
	released  int
}

func (l *fakeLease) Valid(context.Context) bool           { return l.valid }
func (l *fakeLease) Active(context.Context) (bool, error) { return l.active, l.activeErr }
func (l *fakeLease) Release(context.Context) error        { l.released++; return nil }

type fakeFactory struct {
	native        *fakeLease
	fallback      *fakeLease
	nativeErr     error
	fallbackErr   error
	nativeCalls   int
	fallbackCalls int
}

func (f *fakeFactory) Native(context.Context, string) (nativeLease, error) {
	f.nativeCalls++
	if f.nativeErr != nil {
		return nil, f.nativeErr
	}
	return f.native, nil
}

func (f *fakeFactory) Fallback(context.Context, string) (lease, error) {
	f.fallbackCalls++
	if f.fallbackErr != nil {
		return nil, f.fallbackErr
	}
	return f.fallback, nil
}

func TestCoordinatorBridgesPowerDevilWarmupWithLogind(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	native := &fakeLease{valid: true, active: true}
	fallback := &fakeLease{valid: true}
	factory := &fakeFactory{native: native, fallback: fallback}
	coordinator := newCoordinator(factory, func() time.Time { return now })

	status := coordinator.Ensure(context.Background(), "docked")
	if !status.Active || status.Backend != "logind" || status.Degraded {
		t.Fatalf("warmup status = %+v", status)
	}
	now = now.Add(nativeWarmup)
	status = coordinator.Ensure(context.Background(), "docked")
	if !status.Active || status.Backend != "powerdevil" || status.Degraded {
		t.Fatalf("native status = %+v", status)
	}
	if fallback.released != 1 {
		t.Fatalf("fallback releases = %d, want 1", fallback.released)
	}
}

func TestCoordinatorUsesDegradedFallbackWithoutPowerDevil(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	fallback := &fakeLease{valid: true}
	factory := &fakeFactory{nativeErr: errors.New("missing"), fallback: fallback}
	coordinator := newCoordinator(factory, func() time.Time { return now })

	status := coordinator.Ensure(context.Background(), "resume-grace")
	if !status.Active || status.Backend != "logind" || !status.Degraded {
		t.Fatalf("fallback status = %+v", status)
	}
	coordinator.Ensure(context.Background(), "resume-grace")
	if factory.nativeCalls != 1 {
		t.Fatalf("native calls before retry = %d, want 1", factory.nativeCalls)
	}
	now = now.Add(nativeRetry)
	coordinator.Ensure(context.Background(), "resume-grace")
	if factory.nativeCalls != 2 {
		t.Fatalf("native calls after retry = %d, want 2", factory.nativeCalls)
	}
}

func TestCoordinatorRespectsPowerDevilBlockedInhibition(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	native := &fakeLease{valid: true, active: false}
	fallback := &fakeLease{valid: true}
	coordinator := newCoordinator(&fakeFactory{native: native, fallback: fallback}, func() time.Time { return now })

	coordinator.Ensure(context.Background(), "docked")
	now = now.Add(nativeWarmup)
	status := coordinator.Ensure(context.Background(), "docked")
	if status.Active || status.Backend != "none" || !status.Degraded {
		t.Fatalf("blocked status = %+v", status)
	}
	if fallback.released != 1 {
		t.Fatalf("fallback releases = %d, want 1", fallback.released)
	}

	native.active = true
	status = coordinator.Ensure(context.Background(), "docked")
	if !status.Active || status.Backend != "powerdevil" {
		t.Fatalf("unblocked status = %+v", status)
	}
}

func TestCoordinatorReleasesAllLeases(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	native := &fakeLease{valid: true, active: true}
	fallback := &fakeLease{valid: true}
	coordinator := newCoordinator(&fakeFactory{native: native, fallback: fallback}, func() time.Time { return now })
	coordinator.Ensure(context.Background(), "docked")

	status := coordinator.Release(context.Background())
	if status.Active || status.Enabled || status.Phase != "inactive" || status.Backend != "none" {
		t.Fatalf("released status = %+v", status)
	}
	if native.released != 1 || fallback.released != 1 {
		t.Fatalf("release counts native=%d fallback=%d", native.released, fallback.released)
	}
}

func TestContainsStringsFindsTupleEntry(t *testing.T) {
	t.Parallel()
	type tuple struct {
		What, App, Reason, Mode string
		ID                      uint32
	}
	value := []tuple{{What: "sleep", App: applicationName, Reason: "Approved external dock is active", Mode: "block", ID: 3}}
	if !containsStrings(reflectValue(value), applicationName, "Approved external dock is active") {
		t.Fatal("containsStrings did not match inhibition tuple")
	}
}

func reflectValue(value any) reflect.Value { return reflect.ValueOf(value) }
