package inhibit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	powerDevilService = "org.freedesktop.PowerManagement.Inhibit"
	powerDevilPath    = dbus.ObjectPath("/org/freedesktop/PowerManagement/Inhibit")
	powerDevilAPI     = "org.freedesktop.PowerManagement.Inhibit"
	policyService     = "org.kde.Solid.PowerManagement"
	policyPath        = dbus.ObjectPath("/org/kde/Solid/PowerManagement/PolicyAgent")
	policyAPI         = "org.kde.Solid.PowerManagement.PolicyAgent"
	loginService      = "org.freedesktop.login1"
	loginPath         = dbus.ObjectPath("/org/freedesktop/login1")
	loginAPI          = "org.freedesktop.login1.Manager"
	applicationName   = "Dockstar"
	fallbackName      = "Dockstar compatibility guard"
	nativeWarmup      = 6 * time.Second
	nativeRetry       = 5 * time.Second
)

type Status struct {
	Enabled   bool   `json:"enabled"`
	Active    bool   `json:"active"`
	Phase     string `json:"phase"`
	Backend   string `json:"backend"`
	Degraded  bool   `json:"degraded"`
	LastError string `json:"lastError,omitempty"`
}

type lease interface {
	Valid(context.Context) bool
	Release(context.Context) error
}

type nativeLease interface {
	lease
	Active(context.Context) (bool, error)
}

type factory interface {
	Native(context.Context, string) (nativeLease, error)
	Fallback(context.Context, string) (lease, error)
}

type Coordinator struct {
	mu sync.Mutex

	factory  factory
	now      func() time.Time
	native   nativeLease
	fallback lease
	phase    string
	reason   string
	warmAt   time.Time
	retryAt  time.Time
	blocked  bool
	status   Status
}

func New() *Coordinator {
	return newCoordinator(realFactory{}, time.Now)
}

func newCoordinator(factory factory, now func() time.Time) *Coordinator {
	return &Coordinator{
		factory: factory,
		now:     now,
		status:  Status{Phase: "inactive", Backend: "none"},
	}
}

func (c *Coordinator) Ensure(ctx context.Context, phase string) Status {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.phase = phase
	c.reason = reasonFor(phase)
	c.status.Enabled = true
	c.status.Phase = phase
	c.status.LastError = ""
	now := c.now()

	if c.native != nil && !c.native.Valid(ctx) {
		_ = c.native.Release(ctx)
		c.native = nil
		c.warmAt = time.Time{}
		c.retryAt = now
	}
	if c.fallback != nil && !c.fallback.Valid(ctx) {
		_ = c.fallback.Release(ctx)
		c.fallback = nil
	}

	if c.native == nil && !now.Before(c.retryAt) {
		native, err := c.factory.Native(ctx, c.reason)
		if err == nil {
			c.native = native
			c.warmAt = now.Add(nativeWarmup)
			c.blocked = false
		} else {
			c.retryAt = now.Add(nativeRetry)
			c.status.LastError = fmt.Sprintf("PowerDevil inhibition unavailable: %v", err)
		}
	}

	if c.native != nil && !now.Before(c.warmAt) {
		active, err := c.native.Active(ctx)
		switch {
		case err != nil:
			_ = c.native.Release(ctx)
			c.native = nil
			c.retryAt = now.Add(nativeRetry)
			c.status.LastError = fmt.Sprintf("verify PowerDevil inhibition: %v", err)
		case active:
			c.blocked = false
			if c.fallback != nil {
				_ = c.fallback.Release(ctx)
				c.fallback = nil
			}
			c.status.Active = true
			c.status.Backend = "powerdevil"
			c.status.Degraded = false
			return c.status
		default:
			c.blocked = true
			if c.fallback != nil {
				_ = c.fallback.Release(ctx)
				c.fallback = nil
			}
			c.status.Active = false
			c.status.Backend = "none"
			c.status.Degraded = true
			c.status.LastError = "Dockstar sleep protection was blocked in Plasma"
			return c.status
		}
	}

	if c.blocked {
		c.status.Active = false
		c.status.Backend = "none"
		c.status.Degraded = true
		c.status.LastError = "Dockstar sleep protection was blocked in Plasma"
		return c.status
	}

	if c.fallback == nil {
		fallback, err := c.factory.Fallback(ctx, c.reason)
		if err == nil {
			c.fallback = fallback
		} else {
			c.status.Active = false
			c.status.Backend = "none"
			c.status.Degraded = true
			fallbackError := fmt.Sprintf("logind inhibition unavailable: %v", err)
			if c.status.LastError == "" {
				c.status.LastError = fallbackError
			} else {
				c.status.LastError += "; " + fallbackError
			}
			return c.status
		}
	}

	c.status.Active = true
	c.status.Backend = "logind"
	c.status.Degraded = c.native == nil
	return c.status
}

func (c *Coordinator) Release(ctx context.Context) Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	var failures []error
	if c.native != nil {
		failures = appendIf(failures, c.native.Release(ctx))
		c.native = nil
	}
	if c.fallback != nil {
		failures = appendIf(failures, c.fallback.Release(ctx))
		c.fallback = nil
	}
	c.phase, c.reason = "", ""
	c.warmAt, c.retryAt = time.Time{}, time.Time{}
	c.blocked = false
	c.status = Status{Phase: "inactive", Backend: "none"}
	if err := errors.Join(failures...); err != nil {
		c.status.LastError = err.Error()
	}
	return c.status
}

func (c *Coordinator) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	status := c.Release(ctx)
	if status.LastError != "" {
		return errors.New(status.LastError)
	}
	return nil
}

func reasonFor(phase string) string {
	switch phase {
	case "resume-grace":
		return "Waiting for an approved dock after resume"
	case "undock-grace":
		return "Waiting for a disconnected dock to settle"
	default:
		return "Approved external dock is active"
	}
}

func appendIf(values []error, err error) []error {
	if err != nil {
		return append(values, err)
	}
	return values
}

type realFactory struct{}

func (realFactory) Native(ctx context.Context, reason string) (nativeLease, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	owner, err := getOwner(ctx, conn, policyService)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("PowerDevil is not running: %w", err)
	}
	var cookie uint32
	call := conn.Object(powerDevilService, powerDevilPath).CallWithContext(
		ctx, powerDevilAPI+".Inhibit", 0, applicationName, reason,
	)
	if err := call.Store(&cookie); err != nil {
		conn.Close()
		return nil, err
	}
	return &powerDevilLease{conn: conn, cookie: cookie, owner: owner, reason: reason}, nil
}

func (realFactory) Fallback(ctx context.Context, reason string) (lease, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	if !conn.SupportsUnixFDs() {
		conn.Close()
		return nil, errors.New("system bus does not support Unix file descriptors")
	}
	owner, err := getOwner(ctx, conn, loginService)
	if err != nil {
		conn.Close()
		return nil, err
	}
	var fd dbus.UnixFD
	call := conn.Object(loginService, loginPath).CallWithContext(
		ctx, loginAPI+".Inhibit", 0, "sleep", fallbackName, reason, "block",
	)
	if err := call.Store(&fd); err != nil {
		conn.Close()
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "dockstar-sleep-inhibitor")
	if file == nil {
		conn.Close()
		return nil, errors.New("logind returned an invalid inhibitor descriptor")
	}
	return &logindLease{conn: conn, file: file, owner: owner}, nil
}

type powerDevilLease struct {
	conn   *dbus.Conn
	cookie uint32
	owner  string
	reason string
}

func (l *powerDevilLease) Valid(ctx context.Context) bool {
	owner, err := getOwner(ctx, l.conn, policyService)
	return err == nil && owner == l.owner
}

func (l *powerDevilLease) Active(ctx context.Context) (bool, error) {
	var variant dbus.Variant
	err := l.conn.Object(policyService, policyPath).CallWithContext(
		ctx, "org.freedesktop.DBus.Properties.Get", 0, policyAPI, "ActiveInhibitions",
	).Store(&variant)
	if err != nil {
		return false, err
	}
	return containsStrings(reflect.ValueOf(variant.Value()), applicationName, l.reason), nil
}

func (l *powerDevilLease) Release(ctx context.Context) error {
	if l.conn == nil {
		return nil
	}
	err := l.conn.Object(powerDevilService, powerDevilPath).CallWithContext(
		ctx, powerDevilAPI+".UnInhibit", 0, l.cookie,
	).Err
	l.conn.Close()
	l.conn = nil
	return err
}

type logindLease struct {
	conn  *dbus.Conn
	file  *os.File
	owner string
}

func (l *logindLease) Valid(ctx context.Context) bool {
	owner, err := getOwner(ctx, l.conn, loginService)
	return err == nil && owner == l.owner && l.file != nil
}

func (l *logindLease) Release(context.Context) error {
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	if l.conn != nil {
		l.conn.Close()
		l.conn = nil
	}
	return nil
}

func getOwner(ctx context.Context, conn *dbus.Conn, service string) (string, error) {
	var owner string
	err := conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus").CallWithContext(
		ctx, "org.freedesktop.DBus.GetNameOwner", 0, service,
	).Store(&owner)
	return owner, err
}

func containsStrings(value reflect.Value, wanted ...string) bool {
	if !value.IsValid() {
		return false
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		return containsStrings(value.Elem(), wanted...)
	}
	if value.Kind() != reflect.Array && value.Kind() != reflect.Slice {
		return false
	}
	for i := 0; i < value.Len(); i++ {
		item := value.Index(i)
		values := collectStrings(item)
		matched := true
		for _, candidate := range wanted {
			found := false
			for _, actual := range values {
				if actual == candidate {
					found = true
					break
				}
			}
			if !found {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func collectStrings(value reflect.Value) []string {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		return collectStrings(value.Elem())
	}
	switch value.Kind() {
	case reflect.String:
		return []string{value.String()}
	case reflect.Array, reflect.Slice:
		var values []string
		for i := 0; i < value.Len(); i++ {
			values = append(values, collectStrings(value.Index(i))...)
		}
		return values
	case reflect.Struct:
		var values []string
		for i := 0; i < value.NumField(); i++ {
			values = append(values, collectStrings(value.Field(i))...)
		}
		return values
	default:
		return nil
	}
}
