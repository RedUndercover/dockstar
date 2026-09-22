package inhibit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	loginService    = "org.freedesktop.login1"
	loginPath       = dbus.ObjectPath("/org/freedesktop/login1")
	loginAPI        = "org.freedesktop.login1.Manager"
	systemdService  = "org.freedesktop.systemd1"
	systemdPath     = dbus.ObjectPath("/org/freedesktop/systemd1")
	systemdAPI      = "org.freedesktop.systemd1.Manager"
	applicationName = "Dockstar"
	inhibitReason   = "Protecting an active dock session"
	inhibitWhat     = "sleep:idle:handle-lid-switch"
	inhibitMode     = "block"
	minimumSystemd  = 257

	releaseConfirmationTimeout = time.Second
	releaseConfirmationPoll    = 10 * time.Millisecond
)

type Status struct {
	Enabled    bool   `json:"enabled"`
	Active     bool   `json:"active"`
	Phase      string `json:"phase"`
	Backend    string `json:"backend"`
	Mode       string `json:"mode,omitempty"`
	AcquiredAt string `json:"acquiredAt,omitempty"`
	Degraded   bool   `json:"degraded"`
	LastError  string `json:"lastError,omitempty"`
}

type lease interface {
	Valid(context.Context) bool
	Release(context.Context) error
}

type factory interface {
	Acquire(context.Context) (lease, error)
}

type Coordinator struct {
	mu sync.Mutex

	factory    factory
	now        func() time.Time
	lease      lease
	acquiredAt time.Time
	status     Status
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

	c.status = Status{Enabled: true, Phase: phase, Backend: "none"}
	if c.lease != nil && !c.lease.Valid(ctx) {
		_ = c.lease.Release(ctx)
		c.lease = nil
		c.acquiredAt = time.Time{}
	}

	if c.lease == nil {
		acquired, err := c.factory.Acquire(ctx)
		if err != nil {
			c.status.Degraded = true
			c.status.LastError = "logind sleep protection unavailable: " + err.Error()
			return c.status
		}
		c.lease = acquired
		c.acquiredAt = c.now().UTC()
	}

	c.status.Active = true
	c.status.Backend = "logind"
	c.status.Mode = inhibitMode
	c.status.AcquiredAt = c.acquiredAt.Format(time.RFC3339Nano)
	return c.status
}

func (c *Coordinator) Release(ctx context.Context) Status {
	c.mu.Lock()
	defer c.mu.Unlock()

	status := Status{Phase: "inactive", Backend: "none"}
	if c.lease != nil {
		if err := c.lease.Release(ctx); err != nil {
			status.LastError = err.Error()
		}
		c.lease = nil
	}
	c.acquiredAt = time.Time{}
	c.status = status
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

type realFactory struct{}

func (realFactory) Acquire(ctx context.Context) (lease, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	if !conn.SupportsUnixFDs() {
		conn.Close()
		return nil, errors.New("system bus does not support Unix file descriptors")
	}
	version, err := systemdMajorVersion(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("check systemd sleep-inhibitor support: %w", err)
	}
	if version < minimumSystemd {
		conn.Close()
		return nil, fmt.Errorf("systemd %d is unsupported; strict sleep protection requires systemd %d or newer", version, minimumSystemd)
	}
	owner, err := getOwner(ctx, conn, loginService)
	if err != nil {
		conn.Close()
		return nil, err
	}
	var fd dbus.UnixFD
	call := conn.Object(loginService, loginPath).CallWithContext(
		ctx, loginAPI+".Inhibit", 0, inhibitWhat, applicationName, inhibitReason, inhibitMode,
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
	return &logindLease{conn: conn, file: file, owner: owner, pid: uint32(os.Getpid())}, nil
}

type logindLease struct {
	conn  *dbus.Conn
	file  *os.File
	owner string
	pid   uint32
}

func (l *logindLease) Valid(ctx context.Context) bool {
	owner, err := getOwner(ctx, l.conn, loginService)
	return err == nil && owner == l.owner && l.file != nil
}

func (l *logindLease) Release(ctx context.Context) error {
	var failures []error
	if l.file != nil {
		if err := l.file.Close(); err != nil {
			failures = append(failures, fmt.Errorf("close logind inhibitor descriptor: %w", err))
		}
		l.file = nil
	}
	if l.conn != nil {
		if err := waitForRelease(ctx, l.conn, l.pid); err != nil {
			failures = append(failures, err)
		}
		if err := l.conn.Close(); err != nil {
			failures = append(failures, fmt.Errorf("close logind connection: %w", err))
		}
		l.conn = nil
	}
	return errors.Join(failures...)
}

type inhibitorRecord struct {
	What string
	Who  string
	Why  string
	Mode string
	UID  uint32
	PID  uint32
}

func waitForRelease(ctx context.Context, conn *dbus.Conn, pid uint32) error {
	waitCtx, cancel := context.WithTimeout(ctx, releaseConfirmationTimeout)
	defer cancel()
	ticker := time.NewTicker(releaseConfirmationPoll)
	defer ticker.Stop()

	for {
		present, err := inhibitorPresent(waitCtx, conn, pid)
		if err != nil {
			return fmt.Errorf("confirm logind inhibitor release: %w", err)
		}
		if !present {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("confirm logind inhibitor release: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func inhibitorPresent(ctx context.Context, conn *dbus.Conn, pid uint32) (bool, error) {
	var entries []inhibitorRecord
	if err := conn.Object(loginService, loginPath).CallWithContext(
		ctx, loginAPI+".ListInhibitors", 0,
	).Store(&entries); err != nil {
		return false, err
	}
	for _, entry := range entries {
		if inhibitsSleep(entry.What) && entry.Who == applicationName && entry.Mode == inhibitMode && entry.PID == pid {
			return true, nil
		}
	}
	return false, nil
}

func inhibitsSleep(what string) bool {
	for _, category := range strings.Split(what, ":") {
		if category == "sleep" {
			return true
		}
	}
	return false
}

func systemdMajorVersion(ctx context.Context, conn *dbus.Conn) (int, error) {
	var property dbus.Variant
	if err := conn.Object(systemdService, systemdPath).CallWithContext(
		ctx, "org.freedesktop.DBus.Properties.Get", 0, systemdAPI, "Version",
	).Store(&property); err != nil {
		return 0, err
	}
	value, ok := property.Value().(string)
	if !ok {
		return 0, fmt.Errorf("systemd returned version as %T", property.Value())
	}
	return parseSystemdMajorVersion(value)
}

func parseSystemdMajorVersion(value string) (int, error) {
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, fmt.Errorf("invalid systemd version %q", value)
	}
	version, err := strconv.Atoi(value[:end])
	if err != nil {
		return 0, fmt.Errorf("parse systemd version %q: %w", value, err)
	}
	return version, nil
}

func getOwner(ctx context.Context, conn *dbus.Conn, service string) (string, error) {
	var owner string
	err := conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus").CallWithContext(
		ctx, "org.freedesktop.DBus.GetNameOwner", 0, service,
	).Store(&owner)
	return owner, err
}
