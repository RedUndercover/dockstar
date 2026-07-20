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
	Docked    bool             `json:"docked"`
	LidClosed bool             `json:"lidClosed"`
	Layout    string           `json:"layout"`
	Primary   string           `json:"primary"`
	LastError string           `json:"lastError,omitempty"`
	Backend   string           `json:"backend"`
	Outputs   []display.Output `json:"outputs"`
	Updated   time.Time        `json:"updated"`
}

type Controller struct {
	store    *config.Store
	displays display.Backend
	actions  *platform.Manager
	log      *slog.Logger

	mu        sync.RWMutex
	state     State
	emitter   EventEmitter
	signature string
}

func New(store *config.Store, displays display.Backend, logger *slog.Logger) *Controller {
	return &Controller{
		store: store, displays: displays, actions: &platform.Manager{}, log: logger,
		state: State{Backend: displays.Name(), Updated: time.Now().UTC()},
	}
}

func (c *Controller) SetEmitter(emitter EventEmitter) { c.emitter = emitter }

func (c *Controller) Run(ctx context.Context) error {
	resume := make(chan struct{}, 1)
	go c.watchSleep(ctx, resume)
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
			time.Sleep(750 * time.Millisecond)
			if err := c.Evaluate(ctx, true); err != nil {
				c.log.Warn("resume evaluation failed", "error", err)
			}
		case <-ticker.C:
			if err := c.Evaluate(ctx, false); err != nil {
				c.log.Debug("display evaluation failed", "error", err)
			}
		}
	}
}

func (c *Controller) Evaluate(ctx context.Context, force bool) error {
	cfg := c.store.Get()
	snapshot, err := c.displays.Snapshot(ctx, cfg)
	if err != nil {
		c.setError(err)
		return err
	}
	lidClosed, lidErr := platform.LidClosed(ctx)
	if lidErr != nil {
		c.log.Debug("lid state unavailable", "error", lidErr)
	}
	sig := display.Signature(snapshot) + fmt.Sprintf(":lid=%t", lidClosed)
	if !force && sig == c.signature {
		return nil
	}
	c.signature = sig

	var approvedExternal, pending []display.Output
	for _, output := range snapshot.Outputs {
		if !output.Connected || output.Panel {
			continue
		}
		if output.Approved {
			approvedExternal = append(approvedExternal, output)
		} else {
			pending = append(pending, output)
		}
	}
	for _, output := range pending {
		if c.emitter != nil {
			_ = c.emitter.PendingDisplay(output.DeviceID, output.Label)
		}
	}

	c.mu.RLock()
	wasDocked := c.state.Docked
	c.mu.RUnlock()
	docked := cfg.Detection != "off" && len(approvedExternal) > 0
	state := State{Docked: docked, LidClosed: lidClosed, Backend: c.displays.Name(), Outputs: snapshot.Outputs, Updated: time.Now().UTC()}
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
		if !wasDocked || force {
			if err := c.actions.Dock(ctx, cfg); err != nil {
				c.log.Warn("one or more dock actions failed", "error", err)
			}
		}
	} else if wasDocked {
		if err := c.actions.Undock(ctx, cfg); err != nil {
			c.log.Warn("one or more undock actions failed", "error", err)
		}
		if lidClosed && cfg.Security.LockOnClosedUndock {
			if err := platform.Lock(ctx); err != nil {
				c.log.Warn("screen lock failed", "error", err)
			}
			if cfg.Security.SuspendOnClosedUndock {
				time.AfterFunc(500*time.Millisecond, func() {
					child, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					if err := platform.Suspend(child); err != nil {
						c.log.Warn("suspend failed", "error", err)
					}
				})
			}
		}
	}
	c.storeState(state)
	return nil
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
	updated, err := c.store.Replace(next, revision)
	if err != nil {
		return 0, err
	}
	c.signature = ""
	return updated.Revision, nil
}

func (c *Controller) SetLayout(ctx context.Context, layout string) error {
	_, err := c.store.Update(func(cfg *config.Config) error {
		cfg.DefaultLayout = layout
		return nil
	})
	if err != nil {
		return err
	}
	return c.Evaluate(ctx, true)
}

func (c *Controller) ApproveDisplay(id string, allowed bool) error {
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
		c.signature = ""
	}
	return err
}

func (c *Controller) SetTrustPolicy(policy string) error {
	_, err := c.store.Update(func(cfg *config.Config) error {
		cfg.Detection = policy
		return nil
	})
	if err == nil {
		c.signature = ""
	}
	return err
}

func (c *Controller) Reload() error {
	_, err := c.store.Reload()
	if err == nil {
		c.signature = ""
	}
	return err
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
	c.state = state
	c.mu.Unlock()
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

func (c *Controller) setError(err error) {
	c.mu.Lock()
	c.state.LastError = err.Error()
	c.state.Updated = time.Now().UTC()
	c.mu.Unlock()
	if c.emitter != nil {
		_ = c.emitter.ApplyFailed(err.Error())
	}
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
		case signal := <-signals:
			if len(signal.Body) == 1 {
				if sleeping, ok := signal.Body[0].(bool); ok && !sleeping {
					select {
					case resumed <- struct{}{}:
					default:
					}
				}
			}
		}
	}
}

func marshal(value any) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	return string(data), err
}

var _ = errors.Is
