package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/redundercover/dockstar/internal/config"
	"github.com/redundercover/dockstar/internal/display"
	"github.com/redundercover/dockstar/internal/inhibit"
	"github.com/redundercover/dockstar/internal/platform"
	"github.com/redundercover/dockstar/internal/wake"
)

type EventEmitter interface {
	StateChanged(string) error
	PendingDisplay(string, string) error
	ApplyFailed(string) error
	WakeCapabilitiesChanged(string) error
}

type State struct {
	Docked          bool             `json:"docked"`
	LidClosed       bool             `json:"lidClosed"`
	Layout          string           `json:"layout"`
	Primary         string           `json:"primary"`
	LastError       string           `json:"lastError,omitempty"`
	Backend         string           `json:"backend"`
	Outputs         []display.Output `json:"outputs"`
	SleepProtection inhibit.Status   `json:"sleepProtection"`
	Updated         time.Time        `json:"updated"`
}

type sleepProtector interface {
	Ensure(context.Context, string) inhibit.Status
	Release(context.Context) inhibit.Status
	Close() error
}

type platformActions interface {
	Dock(context.Context, config.Config) error
	Undock(context.Context, config.Config) error
}

type physicalSnapshotter interface {
	PhysicalSnapshot(context.Context, config.Config) (display.Snapshot, error)
}

type Controller struct {
	store             *config.Store
	displays          display.Backend
	actions           platformActions
	log               *slog.Logger
	lidClosed         func(context.Context) (bool, error)
	wakePolicyEnabled func(string) (bool, error)
	lockScreen        func(context.Context) error
	suspend           func(context.Context) error
	sleepWatcher      func(context.Context, chan<- struct{})

	mu                    sync.RWMutex
	state                 State
	emitter               EventEmitter
	signatureMu           sync.Mutex
	signature             string
	signatureID           uint64
	evaluate              sync.Mutex
	actionsDocked         bool
	actionsUndockPending  bool
	securityDockKnown     bool
	securityDocked        bool
	undock                closedUndockGuard
	protector             sleepProtector
	timingMu              sync.Mutex
	resumeUntil           time.Time
	undockUntil           time.Time
	snapshotFailureSince  time.Time
	now                   func() time.Time
	suspendMu             sync.Mutex
	suspendPending        bool
	suspendStarted        bool
	suspendGeneration     uint64
	preSleepDocked        bool
	preSleepLidClosed     bool
	autoSuspendMu         sync.Mutex
	autoSuspendPending    bool
	autoSuspendGeneration uint64
	schedule              func(time.Duration, func())
}

const (
	resumeProtectionWindow = 30 * time.Second
	undockProtectionWindow = 5 * time.Second
	snapshotFailureWindow  = 30 * time.Second
	suspendRequestTimeout  = 15 * time.Second
)

type closedUndockGuard struct {
	handled bool
}

func (g *closedUndockGuard) ShouldRun(wasDocked, docked, lidClosed, enabled bool) bool {
	if !lidClosed || docked {
		g.handled = false
	}
	if !enabled || !wasDocked || docked || !lidClosed || g.handled {
		return false
	}
	g.handled = true
	return true
}

func New(store *config.Store, displays display.Backend, logger *slog.Logger) *Controller {
	return newController(store, displays, logger, inhibit.New())
}

func newController(store *config.Store, displays display.Backend, logger *slog.Logger, protector sleepProtector) *Controller {
	controller := &Controller{
		store:             store,
		displays:          displays,
		actions:           &platform.Manager{},
		log:               logger,
		protector:         protector,
		now:               time.Now,
		lidClosed:         platform.LidClosed,
		wakePolicyEnabled: wake.PolicyEnabled,
		lockScreen:        platform.Lock,
		suspend:           platform.Suspend,
		schedule:          func(delay time.Duration, action func()) { time.AfterFunc(delay, action) },
		state: State{
			Backend:         displays.Name(),
			SleepProtection: inhibit.Status{Phase: "inactive", Backend: "none"},
			Updated:         time.Now().UTC(),
		},
	}
	controller.sleepWatcher = controller.watchSleep
	return controller
}

func (c *Controller) SetEmitter(emitter EventEmitter) { c.emitter = emitter }

func (c *Controller) Run(ctx context.Context) error {
	sleepWatcherDone := make(chan struct{})
	defer func() {
		c.evaluate.Lock()
		c.cancelAutoSuspendLocked()
		c.evaluate.Unlock()
		<-sleepWatcherDone
		c.evaluate.Lock()
		defer c.evaluate.Unlock()
		if err := c.protector.Close(); err != nil {
			c.log.Debug("release sleep protection", "error", err)
		}
	}()
	resume := make(chan struct{}, 1)
	go func() {
		defer close(sleepWatcherDone)
		c.sleepWatcher(ctx, resume)
	}()
	if err := c.Evaluate(ctx, false); err != nil {
		c.log.Warn("initial display evaluation failed", "error", err)
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-resume:
			c.pollResume(ctx)
		case <-ticker.C:
			if err := c.Evaluate(ctx, false); err != nil {
				c.log.Debug("display evaluation failed", "error", err)
			}
		}
	}
}

func (c *Controller) Evaluate(ctx context.Context, force bool) error {
	c.evaluate.Lock()
	defer c.evaluate.Unlock()

	signatureID := c.currentSignatureID()
	cfg := c.store.Get()
	var physical display.Snapshot
	var physicalErr error
	physicalAvailable := false
	physicalBackend := false
	if backend, ok := c.displays.(physicalSnapshotter); ok {
		physicalBackend = true
		physical, physicalErr = backend.PhysicalSnapshot(ctx, cfg)
		physicalAvailable = physicalErr == nil
	}
	snapshot, snapshotErr := c.displays.Snapshot(ctx, cfg)
	lidClosed, lidErr := c.lidClosed(ctx)
	lidKnown := lidErr == nil
	if lidErr != nil {
		c.log.Debug("lid state unavailable", "error", lidErr)
	}
	if snapshotErr != nil {
		if physicalAvailable {
			c.mu.RLock()
			cachedSession := display.Snapshot{Outputs: append([]display.Output(nil), c.state.Outputs...)}
			c.mu.RUnlock()
			physical = display.ResolvePhysical(physical, cachedSession, cfg)
		}
		return c.handleSnapshotFailure(ctx, cfg, physical, physicalAvailable, errors.Join(snapshotErr, physicalErr), lidClosed, lidKnown)
	}
	if physicalErr != nil {
		c.log.Debug("physical display snapshot unavailable; using compositor state", "error", physicalErr)
	}

	detectionSnapshot := snapshot
	connectivityKnown := !physicalBackend
	if physicalAvailable {
		physical = display.ResolvePhysical(physical, snapshot, cfg)
		detectionSnapshot = physical
		connectivityKnown = true
	}
	c.clearSnapshotFailure()
	c.mu.RLock()
	wasDocked := c.state.Docked
	c.mu.RUnlock()
	wasActionsDocked := c.actionsDocked
	docked := cfg.Detection != "off" && hasApprovedExternal(detectionSnapshot)
	if connectivityKnown {
		c.observeSecurityStateLocked(ctx, cfg, docked, lidClosed, lidKnown)
	} else if docked || (lidKnown && !lidClosed) {
		c.cancelAutoSuspendLocked()
		if lidKnown {
			c.undock.ShouldRun(false, docked, lidClosed, false)
		}
	}
	protection := c.reconcileProtection(ctx, cfg, wasDocked, docked, lidClosed)

	sig := display.Signature(snapshot) + ":physical=" + display.Signature(detectionSnapshot) + fmt.Sprintf(":lid=%t", lidClosed)
	needsActionRetry := (wasActionsDocked && !docked) || (c.actionsUndockPending && docked)
	if !c.shouldEvaluateSignature(sig, signatureID, force) && !needsActionRetry {
		c.storeProtection(protection)
		return nil
	}
	for _, output := range snapshot.Outputs {
		if output.Connected && !output.Panel && !output.Approved && c.emitter != nil {
			_ = c.emitter.PendingDisplay(output.DeviceID, output.Label)
		}
	}

	state := State{Docked: docked, LidClosed: lidClosed, Backend: c.displays.Name(), Outputs: snapshot.Outputs, SleepProtection: protection, Updated: time.Now().UTC()}
	if docked {
		layout := cfg.DefaultLayout
		if lidClosed {
			layout = cfg.LidClosedLayout
		}
		_ = c.displays.DPMSOn(ctx)
		result, applyErr := c.displays.Apply(ctx, cfg, layout)
		if applyErr != nil {
			state.LastError = applyErr.Error()
			c.storeState(state)
			if c.emitter != nil {
				_ = c.emitter.ApplyFailed(applyErr.Error())
			}
			return applyErr
		}
		state.Layout, state.Primary = result.Layout, result.Primary
		if !wasActionsDocked || force || c.actionsUndockPending {
			if err := c.actions.Dock(ctx, cfg); err != nil {
				c.log.Warn("one or more dock actions failed", "error", err)
			}
		}
		c.actionsDocked = true
		c.actionsUndockPending = false
	} else if wasActionsDocked {
		if err := c.actions.Undock(ctx, cfg); err != nil {
			c.actionsUndockPending = true
			state.LastError = err.Error()
			c.storeState(state)
			if c.emitter != nil {
				_ = c.emitter.ApplyFailed(err.Error())
			}
			return err
		}
		c.actionsDocked = false
		c.actionsUndockPending = false
	}
	c.rememberSignature(sig, signatureID)
	c.storeState(state)
	return nil
}

func (c *Controller) handleSnapshotFailure(ctx context.Context, cfg config.Config, physical display.Snapshot, physicalAvailable bool, snapshotErr error, lidClosed, lidKnown bool) error {
	c.mu.RLock()
	state := c.state
	c.mu.RUnlock()
	wasDocked := state.Docked
	combinedErr := snapshotErr
	if physicalAvailable {
		c.clearSnapshotFailure()
		state.Docked = cfg.Detection != "off" && hasApprovedExternal(physical)
		state.Outputs = physical.Outputs
		c.observeSecurityStateLocked(ctx, cfg, state.Docked, lidClosed, lidKnown)
		state.SleepProtection = c.reconcileProtection(ctx, cfg, wasDocked, state.Docked, lidClosed)
		if !state.Docked && c.actionsDocked {
			if err := c.actions.Undock(ctx, cfg); err != nil {
				c.actionsUndockPending = true
				combinedErr = errors.Join(combinedErr, err)
			} else {
				c.actionsDocked = false
				c.actionsUndockPending = false
			}
		}
	} else {
		if lidKnown && !lidClosed {
			c.cancelAutoSuspendLocked()
			c.undock.ShouldRun(false, false, false, false)
		}
		if cfg.Detection == "off" {
			state.Docked = false
		}
		state.SleepProtection = c.reconcileUnknownProtection(ctx, cfg, wasDocked)
	}
	state.LidClosed = lidClosed
	state.LastError = combinedErr.Error()
	state.Backend = c.displays.Name()
	state.Updated = c.now().UTC()
	c.invalidateSignature()
	c.storeState(state)
	if c.emitter != nil {
		_ = c.emitter.ApplyFailed(combinedErr.Error())
	}
	return combinedErr
}

func hasApprovedExternal(snapshot display.Snapshot) bool {
	for _, output := range snapshot.Outputs {
		if output.Connected && !output.Panel && output.Approved {
			return true
		}
	}
	return false
}

func (c *Controller) StatusJSON() (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return marshal(c.state)
}

func (c *Controller) ConfigJSON() (string, uint64, error) {
	cfg := c.store.Get()
	data, err := marshal(cfg)
	return data, cfg.Revision, err
}

func (c *Controller) UpdateConfig(raw string, revision uint64) (uint64, error) {
	var next config.Config
	if err := json.Unmarshal([]byte(raw), &next); err != nil {
		return 0, err
	}
	c.evaluate.Lock()
	defer c.evaluate.Unlock()
	updated, err := c.store.Replace(next, revision)
	if err != nil {
		return 0, err
	}
	c.cancelAutoSuspendLocked()
	c.resetSecurityStateLocked()
	c.invalidateSignature()
	if !updated.Power.KeepAwakeWhileDocked || updated.Detection == "off" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		c.storeProtection(c.releaseForInactivePolicy(ctx, updated))
	}
	return updated.Revision, nil
}

func (c *Controller) SetLayout(ctx context.Context, layout string) error {
	c.evaluate.Lock()
	_, err := c.store.Update(func(cfg *config.Config) error {
		cfg.DefaultLayout = layout
		return nil
	})
	if err == nil {
		c.cancelAutoSuspendLocked()
		c.resetSecurityStateLocked()
		c.invalidateSignature()
	}
	c.evaluate.Unlock()
	if err != nil {
		return err
	}
	return c.Evaluate(ctx, true)
}

func (c *Controller) SuspendNow(ctx context.Context) error {
	c.evaluate.Lock()
	defer c.evaluate.Unlock()
	c.cancelAutoSuspendLocked()
	return c.suspendNowLocked(ctx)
}

func (c *Controller) suspendNowLocked(ctx context.Context) error {
	c.suspendMu.Lock()
	if c.suspendPending {
		c.suspendMu.Unlock()
		return errors.New("a Dockstar suspend request is already pending")
	}
	c.suspendGeneration++
	generation := c.suspendGeneration
	c.suspendPending = true
	c.suspendStarted = false
	c.preSleepDocked = false
	c.preSleepLidClosed = false
	c.suspendMu.Unlock()

	status := c.protector.Release(ctx)
	if status.LastError != "" {
		c.cancelSuspendBypass(generation)
		guardCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		restoreErr := c.restoreProtectionAfterSuspendAttempt(guardCtx)
		return errors.Join(fmt.Errorf("release sleep protection: %s", status.LastError), restoreErr)
	}
	status.Enabled = c.store.Get().Power.KeepAwakeWhileDocked
	status.Phase = "manual-suspend"
	c.storeProtection(status)
	c.log.Info("sleep protection released for intentional suspend")

	if err := c.suspend(ctx); err != nil {
		c.cancelSuspendBypass(generation)
		guardCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		restoreErr := c.restoreProtectionAfterSuspendAttempt(guardCtx)
		return errors.Join(fmt.Errorf("request suspend: %w", err), restoreErr)
	}
	c.schedule(suspendRequestTimeout, func() { c.expireSuspendBypass(generation) })
	return nil
}

func (c *Controller) observeSecurityStateLocked(ctx context.Context, cfg config.Config, docked, lidClosed, lidKnown bool) {
	wasDocked := c.securityDockKnown && c.securityDocked
	c.securityDockKnown = true
	c.securityDocked = docked
	if docked {
		c.cancelAutoSuspendLocked()
		c.undock.ShouldRun(false, true, lidClosed, false)
	} else if lidKnown && !lidClosed {
		c.cancelAutoSuspendLocked()
		c.undock.ShouldRun(false, false, false, false)
	}
	if !lidKnown {
		return
	}
	enabled := cfg.Detection != "off" && cfg.Security.LockOnClosedUndock
	if !c.undock.ShouldRun(wasDocked, docked, lidClosed, enabled) {
		return
	}
	if cfg.Security.LockOnClosedUndock {
		if err := c.lockScreen(ctx); err != nil {
			c.log.Warn("screen lock failed", "error", err)
		}
	}
	if cfg.Security.SuspendOnClosedUndock {
		c.scheduleAutoSuspendLocked()
	}
}

func (c *Controller) scheduleAutoSuspendLocked() {
	c.autoSuspendMu.Lock()
	c.autoSuspendGeneration++
	generation := c.autoSuspendGeneration
	c.autoSuspendPending = true
	c.autoSuspendMu.Unlock()
	c.schedule(500*time.Millisecond, func() { c.runAutoSuspend(generation) })
}

func (c *Controller) cancelAutoSuspendLocked() {
	c.autoSuspendMu.Lock()
	c.autoSuspendPending = false
	c.autoSuspendGeneration++
	c.autoSuspendMu.Unlock()
}

func (c *Controller) resetSecurityStateLocked() {
	c.securityDockKnown = false
	c.securityDocked = false
	c.undock = closedUndockGuard{}
}

func (c *Controller) runAutoSuspend(generation uint64) {
	c.evaluate.Lock()
	defer c.evaluate.Unlock()
	c.autoSuspendMu.Lock()
	if !c.autoSuspendPending || c.autoSuspendGeneration != generation {
		c.autoSuspendMu.Unlock()
		return
	}
	c.autoSuspendPending = false
	c.autoSuspendGeneration++
	c.autoSuspendMu.Unlock()
	cfg := c.store.Get()
	if cfg.Detection == "off" || !cfg.Security.LockOnClosedUndock || !cfg.Security.SuspendOnClosedUndock {
		return
	}
	child, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	docked, known := c.currentDockedLocked(child, cfg)
	if !known {
		c.log.Debug("cancel automatic suspend; display connectivity unavailable")
		return
	}
	if docked {
		c.securityDockKnown = true
		c.securityDocked = true
		c.undock.ShouldRun(false, true, true, false)
		return
	}
	// Query the lid after compositor discovery, which may block until its
	// timeout. An earlier lid sample must not authorize a later suspend.
	lidClosed, err := c.lidClosed(child)
	if err != nil || !lidClosed {
		if err != nil {
			c.log.Debug("cancel automatic suspend; lid state unavailable", "error", err)
		} else {
			c.undock.ShouldRun(false, false, false, false)
		}
		return
	}
	c.autoSuspendMu.Lock()
	cancelled := c.autoSuspendGeneration != generation+1
	c.autoSuspendMu.Unlock()
	if cancelled {
		return
	}
	if err := c.suspendNowLocked(child); err != nil {
		c.log.Warn("suspend failed", "error", err)
	}
}

func (c *Controller) currentDockedLocked(ctx context.Context, cfg config.Config) (bool, bool) {
	session, sessionErr := c.displays.Snapshot(ctx, cfg)
	if backend, ok := c.displays.(physicalSnapshotter); ok {
		// Read hardware after the potentially slow compositor query, so a
		// reconnect during that query cannot be mistaken for an undocked state.
		physical, err := backend.PhysicalSnapshot(ctx, cfg)
		if err != nil {
			return false, false
		}
		if sessionErr != nil {
			c.mu.RLock()
			session = display.Snapshot{Outputs: c.state.Outputs}
			c.mu.RUnlock()
		}
		physical = display.ResolvePhysical(physical, session, cfg)
		return cfg.Detection != "off" && hasApprovedExternal(physical), true
	}
	if sessionErr != nil {
		return false, false
	}
	return cfg.Detection != "off" && hasApprovedExternal(session), true
}

func (c *Controller) ApproveDisplay(id string, allowed bool) error {
	c.evaluate.Lock()
	defer c.evaluate.Unlock()
	_, err := c.store.Update(func(cfg *config.Config) error {
		for index := range cfg.Displays {
			if cfg.Displays[index].ID == id {
				cfg.Displays[index].Allowed = allowed
				return nil
			}
		}
		label := id
		c.mu.RLock()
		for _, output := range c.state.Outputs {
			if output.DeviceID == id {
				label = output.Label
				break
			}
		}
		c.mu.RUnlock()
		cfg.Displays = append(cfg.Displays, config.DisplayRule{
			ID: id, Label: label, Allowed: allowed, Scale: 1, Mode: "preferred", Position: "right",
		})
		return nil
	})
	if err == nil {
		c.cancelAutoSuspendLocked()
		c.resetSecurityStateLocked()
		c.invalidateSignature()
	}
	return err
}

func (c *Controller) SetTrustPolicy(policy string) error {
	c.evaluate.Lock()
	defer c.evaluate.Unlock()
	updated, err := c.store.Update(func(cfg *config.Config) error {
		cfg.Detection = policy
		return nil
	})
	if err != nil {
		return err
	}
	c.cancelAutoSuspendLocked()
	c.resetSecurityStateLocked()
	c.invalidateSignature()
	if updated.Detection == "off" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		c.storeProtection(c.releaseForInactivePolicy(ctx, updated))
	}
	return nil
}

func (c *Controller) Reload() error {
	c.evaluate.Lock()
	defer c.evaluate.Unlock()
	updated, err := c.store.Reload()
	if err != nil {
		return err
	}
	c.cancelAutoSuspendLocked()
	c.resetSecurityStateLocked()
	c.invalidateSignature()
	if !updated.Power.KeepAwakeWhileDocked || updated.Detection == "off" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		c.storeProtection(c.releaseForInactivePolicy(ctx, updated))
	}
	return nil
}

func (c *Controller) WakeStatusJSON() (string, error) {
	status, err := wake.GetStatus("")
	if err != nil {
		return "", err
	}
	return marshal(status)
}

func (c *Controller) DiagnosticsJSON(ctx context.Context) (string, error) {
	status, statusErr := c.StatusJSON()
	wakeStatus, wakeErr := c.WakeStatusJSON()
	result := map[string]any{
		"configPath": c.store.Path(), "backend": c.displays.Name(), "status": json.RawMessage(status),
		"waylandDisplay": os.Getenv("WAYLAND_DISPLAY"), "desktop": os.Getenv("XDG_CURRENT_DESKTOP"),
		"sleepControl": c.sleepControlDiagnostics(),
	}
	if statusErr != nil {
		result["statusError"] = statusErr.Error()
	}
	if wakeErr != nil {
		result["wakeError"] = wakeErr.Error()
	} else {
		result["wake"] = json.RawMessage(wakeStatus)
	}
	return marshal(result)
}

func (c *Controller) storeState(state State) {
	c.mu.Lock()
	previous := c.state.SleepProtection
	c.state = state
	c.mu.Unlock()
	c.logProtectionChange(previous, state.SleepProtection)
	c.publishState(state)
}

func (c *Controller) publishState(state State) {
	raw, _ := marshal(state)
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		dir := filepath.Join(runtimeDir, "dockstar")
		if err := os.MkdirAll(dir, 0o700); err == nil {
			_ = os.WriteFile(filepath.Join(dir, "state.json"), []byte(raw+"\n"), 0o600)
		}
	}
	if c.emitter != nil {
		_ = c.emitter.StateChanged(raw)
	}
}

func (c *Controller) storeProtection(status inhibit.Status) {
	c.mu.Lock()
	if c.state.SleepProtection == status {
		c.mu.Unlock()
		return
	}
	previous := c.state.SleepProtection
	c.state.SleepProtection = status
	c.state.Updated = c.now().UTC()
	state := c.state
	c.mu.Unlock()
	c.logProtectionChange(previous, status)
	c.publishState(state)
}

func (c *Controller) logProtectionChange(previous, next inhibit.Status) {
	if previous == next {
		return
	}
	attributes := []any{"phase", next.Phase, "active", next.Active, "backend", next.Backend, "mode", next.Mode}
	if next.LastError != "" {
		attributes = append(attributes, "error", next.LastError)
		c.log.Warn("sleep protection changed", attributes...)
		return
	}
	c.log.Info("sleep protection changed", attributes...)
}

func (c *Controller) watchSleep(ctx context.Context, resumed chan<- struct{}) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		c.log.Debug("system bus unavailable", "error", err)
		return
	}
	defer conn.Close()
	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath("/org/freedesktop/login1"),
		dbus.WithMatchInterface("org.freedesktop.login1.Manager"),
		dbus.WithMatchMember("PrepareForSleep"),
	); err != nil {
		c.log.Debug("sleep signal unavailable", "error", err)
		return
	}
	signals := make(chan *dbus.Signal, 4)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	for {
		select {
		case <-ctx.Done():
			return
		case signal, ok := <-signals:
			if !ok {
				return
			}
			if signal == nil {
				continue
			}
			if len(signal.Body) == 1 {
				if sleeping, ok := signal.Body[0].(bool); ok {
					if sleeping {
						c.noteSleepStarted()
						continue
					}
					c.beginResumeProtection(ctx)
					select {
					case resumed <- struct{}{}:
					default:
					}
				}
			}
		}
	}
}

func (c *Controller) beginResumeProtection(ctx context.Context) {
	c.evaluate.Lock()
	defer c.evaluate.Unlock()
	c.cancelAutoSuspendLocked()

	cfg := c.store.Get()
	if !cfg.Power.KeepAwakeWhileDocked || cfg.Detection == "off" {
		c.finishSuspendBypass()
		c.storeProtection(c.releaseForInactivePolicy(ctx, cfg))
		return
	}
	c.suspendMu.Lock()
	preSleepDocked := c.preSleepDocked
	preSleepLidClosed := c.preSleepLidClosed
	c.preSleepDocked = false
	c.preSleepLidClosed = false
	c.suspendPending = false
	c.suspendStarted = false
	c.suspendGeneration++
	c.suspendMu.Unlock()
	enabled, err := c.wakePolicyEnabled("")
	if !preSleepDocked && !preSleepLidClosed && (err != nil || !enabled) {
		c.clearProtectionWindows()
		status := c.protector.Release(ctx)
		status.Enabled = true
		c.storeProtection(status)
		return
	}
	c.log.Info("system resumed; starting display discovery guard", "previouslyDocked", preSleepDocked, "lidClosed", preSleepLidClosed)
	c.timingMu.Lock()
	c.resumeUntil = c.now().Add(resumeProtectionWindow)
	c.undockUntil = time.Time{}
	c.snapshotFailureSince = time.Time{}
	c.timingMu.Unlock()
	guardCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c.storeProtection(c.protector.Ensure(guardCtx, "resume-grace"))
}

func (c *Controller) pollResume(ctx context.Context) {
	started := c.now()
	delay := 250 * time.Millisecond
	force := true
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := c.Evaluate(ctx, force); err != nil {
			c.log.Debug("resume display evaluation failed", "error", err)
		}
		force = false

		c.evaluate.Lock()
		c.mu.RLock()
		docked := c.state.Docked
		lidClosed := c.state.LidClosed
		c.mu.RUnlock()
		timedOut := c.now().Sub(started) >= resumeProtectionWindow
		if docked {
			c.evaluate.Unlock()
			return
		}
		if !lidClosed || timedOut {
			c.timingMu.Lock()
			c.resumeUntil = time.Time{}
			c.timingMu.Unlock()
			status := c.protector.Release(ctx)
			status.Enabled = c.store.Get().Power.KeepAwakeWhileDocked
			c.storeProtection(status)
			c.evaluate.Unlock()
			return
		}
		c.evaluate.Unlock()
		if c.now().Sub(started) < 5*time.Second {
			delay = 500 * time.Millisecond
		} else {
			delay = time.Second
		}
	}
}

func (c *Controller) reconcileProtection(ctx context.Context, cfg config.Config, wasDocked, docked, lidClosed bool) inhibit.Status {
	if !cfg.Power.KeepAwakeWhileDocked || cfg.Detection == "off" {
		return c.releaseForInactivePolicy(ctx, cfg)
	}
	if c.suspendBypassActive() {
		return inhibit.Status{Enabled: true, Phase: "manual-suspend", Backend: "none"}
	}

	now := c.now()
	c.timingMu.Lock()
	if docked {
		c.resumeUntil = time.Time{}
		c.undockUntil = time.Time{}
	} else if !lidClosed {
		c.resumeUntil = time.Time{}
		c.undockUntil = time.Time{}
	} else if wasDocked {
		c.undockUntil = now.Add(undockProtectionWindow)
	}
	resumeActive := now.Before(c.resumeUntil)
	undockActive := now.Before(c.undockUntil)
	if !resumeActive {
		c.resumeUntil = time.Time{}
	}
	if !undockActive {
		c.undockUntil = time.Time{}
	}
	c.timingMu.Unlock()

	switch {
	case docked:
		return c.protector.Ensure(ctx, "docked")
	case resumeActive:
		return c.protector.Ensure(ctx, "resume-grace")
	case undockActive:
		return c.protector.Ensure(ctx, "undock-grace")
	default:
		status := c.protector.Release(ctx)
		status.Enabled = true
		return status
	}
}

func (c *Controller) reconcileUnknownProtection(ctx context.Context, cfg config.Config, wasDocked bool) inhibit.Status {
	if !cfg.Power.KeepAwakeWhileDocked || cfg.Detection == "off" {
		return c.releaseForInactivePolicy(ctx, cfg)
	}
	if c.suspendBypassActive() {
		return inhibit.Status{Enabled: true, Phase: "manual-suspend", Backend: "none"}
	}
	now := c.now()
	c.timingMu.Lock()
	if c.snapshotFailureSince.IsZero() && wasDocked {
		c.snapshotFailureSince = now
	}
	resumeActive := now.Before(c.resumeUntil)
	undockActive := now.Before(c.undockUntil)
	failureActive := !c.snapshotFailureSince.IsZero() && now.Sub(c.snapshotFailureSince) < snapshotFailureWindow
	if !resumeActive {
		c.resumeUntil = time.Time{}
	}
	if !undockActive {
		c.undockUntil = time.Time{}
	}
	c.timingMu.Unlock()
	switch {
	case resumeActive:
		return c.protector.Ensure(ctx, "resume-grace")
	case undockActive:
		return c.protector.Ensure(ctx, "undock-grace")
	case failureActive:
		return c.protector.Ensure(ctx, "snapshot-grace")
	default:
		status := c.protector.Release(ctx)
		status.Enabled = true
		return status
	}
}

func (c *Controller) releaseForInactivePolicy(ctx context.Context, cfg config.Config) inhibit.Status {
	c.clearProtectionWindows()
	status := c.protector.Release(ctx)
	status.Enabled = cfg.Power.KeepAwakeWhileDocked
	return status
}

func (c *Controller) noteSleepStarted() {
	c.cancelAutoSuspendLocked()
	c.mu.RLock()
	docked := c.state.Docked
	lidClosed := c.state.LidClosed
	c.mu.RUnlock()
	c.suspendMu.Lock()
	c.preSleepDocked = docked
	c.preSleepLidClosed = lidClosed
	if c.suspendPending {
		c.suspendStarted = true
	}
	c.suspendMu.Unlock()
	c.log.Info("system sleep started", "docked", docked, "lidClosed", lidClosed)
}

func (c *Controller) suspendBypassActive() bool {
	c.suspendMu.Lock()
	defer c.suspendMu.Unlock()
	return c.suspendPending
}

func (c *Controller) cancelSuspendBypass(generation uint64) bool {
	c.suspendMu.Lock()
	defer c.suspendMu.Unlock()
	if !c.suspendPending || c.suspendGeneration != generation {
		return false
	}
	c.suspendPending = false
	c.suspendStarted = false
	c.preSleepDocked = false
	c.preSleepLidClosed = false
	c.suspendGeneration++
	return true
}

func (c *Controller) finishSuspendBypass() {
	c.suspendMu.Lock()
	c.suspendPending = false
	c.suspendStarted = false
	c.preSleepDocked = false
	c.preSleepLidClosed = false
	c.suspendGeneration++
	c.suspendMu.Unlock()
}

func (c *Controller) expireSuspendBypass(generation uint64) {
	c.evaluate.Lock()
	defer c.evaluate.Unlock()

	c.suspendMu.Lock()
	if !c.suspendPending || c.suspendStarted || c.suspendGeneration != generation {
		c.suspendMu.Unlock()
		return
	}
	c.suspendPending = false
	c.suspendStarted = false
	c.preSleepDocked = false
	c.preSleepLidClosed = false
	c.suspendGeneration++
	c.suspendMu.Unlock()
	c.log.Warn("intentional suspend did not start; restoring sleep protection")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.restoreProtectionAfterSuspendAttempt(ctx); err != nil {
		c.log.Warn("restore sleep protection after suspend timeout", "error", err)
	}
}

func (c *Controller) restoreProtectionAfterSuspendAttempt(ctx context.Context) error {
	cfg := c.store.Get()
	c.mu.RLock()
	docked := c.state.Docked
	lidClosed := c.state.LidClosed
	c.mu.RUnlock()
	status := c.reconcileProtection(ctx, cfg, docked, docked, lidClosed)
	c.storeProtection(status)
	if status.LastError != "" {
		return errors.New(status.LastError)
	}
	return nil
}

func (c *Controller) sleepControlDiagnostics() map[string]any {
	c.suspendMu.Lock()
	defer c.suspendMu.Unlock()
	return map[string]any{
		"intentionalSuspendPending": c.suspendPending,
		"sleepStarted":              c.suspendStarted,
	}
}

func (c *Controller) clearProtectionWindows() {
	c.timingMu.Lock()
	c.resumeUntil = time.Time{}
	c.undockUntil = time.Time{}
	c.snapshotFailureSince = time.Time{}
	c.timingMu.Unlock()
}

func (c *Controller) clearSnapshotFailure() {
	c.timingMu.Lock()
	c.snapshotFailureSince = time.Time{}
	c.timingMu.Unlock()
}

func (c *Controller) currentSignatureID() uint64 {
	c.signatureMu.Lock()
	defer c.signatureMu.Unlock()
	return c.signatureID
}

func (c *Controller) shouldEvaluateSignature(signature string, id uint64, force bool) bool {
	c.signatureMu.Lock()
	defer c.signatureMu.Unlock()
	return id != c.signatureID || force || c.signature != signature
}

func (c *Controller) rememberSignature(signature string, id uint64) {
	c.signatureMu.Lock()
	defer c.signatureMu.Unlock()
	if id == c.signatureID {
		c.signature = signature
	}
}

func (c *Controller) invalidateSignature() {
	c.signatureMu.Lock()
	c.signatureID++
	c.signature = ""
	c.signatureMu.Unlock()
}

func marshal(value any) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	return string(data), err
}
