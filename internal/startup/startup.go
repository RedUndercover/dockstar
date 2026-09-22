package startup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/redundercover/dockstar/internal/config"
	"github.com/redundercover/dockstar/internal/display"
	"github.com/redundercover/dockstar/internal/inhibit"
)

const (
	PolicyPath = "/var/lib/dockstar/startup.json"

	policyVersion         = 1
	maxConfigBytes        = 1 << 20
	pollInterval          = time.Second
	operationTimeout      = 2 * time.Second
	helperCommandTimeout  = 10 * time.Second
	sessionHandoffTimeout = 15 * time.Second
	pkexecPath            = "/usr/bin/pkexec"
	helperPath            = "/usr/bin/dockstar-startup-helper"
	systemctlPath         = "/usr/bin/systemctl"
	startupUnit           = "dockstar-startup.service"
	drmRoot               = "/sys/class/drm"
)

var edidID = regexp.MustCompile(`^edid:[0-9a-f]{64}$`)

// Status describes the installed pre-login sleep-protection policy.
type Status struct {
	Installed            bool     `json:"installed"`
	Enabled              bool     `json:"enabled"`
	Detection            string   `json:"detection"`
	KeepAwakeWhileDocked bool     `json:"keepAwakeWhileDocked"`
	AllowedEDIDs         []string `json:"allowedEdids"`
}

type policy struct {
	Version              int      `json:"version"`
	Enabled              bool     `json:"enabled"`
	Detection            string   `json:"detection"`
	KeepAwakeWhileDocked bool     `json:"keepAwakeWhileDocked"`
	AllowedEDIDs         []string `json:"allowedEdids"`
}

// DesiredStatus normalizes a saved configuration to the policy Sync would
// install. Installed is false because this describes desired, not on-disk, state.
func DesiredStatus(cfg config.Config) Status {
	return statusFromPolicy(synchronizedPolicy(cfg), false)
}

// Setup installs or disables the authenticated pre-login protection policy.
func Setup(ctx context.Context, cfg config.Config, enabled bool) error {
	if !enabled {
		return invokeHelper(ctx, "disable", nil)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate startup configuration: %w", err)
	}
	if _, err := policyFromConfig(cfg); err != nil {
		return err
	}
	return invokeHelper(ctx, "enable", &cfg)
}

// Sync replaces the authenticated pre-login policy with the current saved
// configuration, disabling it when startup protection is no longer applicable.
func Sync(ctx context.Context, cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate startup configuration: %w", err)
	}
	if err := validatePolicy(synchronizedPolicy(cfg)); err != nil {
		return fmt.Errorf("derive startup policy: %w", err)
	}
	return invokeHelper(ctx, "sync", &cfg)
}

func invokeHelper(ctx context.Context, action string, cfg *config.Config) error {
	var input io.Reader = bytes.NewReader(nil)
	if cfg != nil {
		data, err := json.Marshal(*cfg)
		if err != nil {
			return fmt.Errorf("encode startup configuration: %w", err)
		}
		input = bytes.NewReader(data)
	}

	cmd := exec.CommandContext(ctx, pkexecPath, helperPath, action)
	cmd.Stdin = input
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("configure startup protection: %w", err)
	}
	return nil
}

// GetStatus reads the root-owned startup policy without requiring elevation.
func GetStatus() (Status, error) {
	configured, installed, err := loadPolicy(PolicyPath, true)
	if err != nil {
		return Status{}, err
	}
	if !installed {
		return Status{}, nil
	}
	return statusFromPolicy(configured, true), nil
}

func statusFromPolicy(configured policy, installed bool) Status {
	return Status{
		Installed:            installed,
		Enabled:              configured.Enabled,
		Detection:            configured.Detection,
		KeepAwakeWhileDocked: configured.KeepAwakeWhileDocked,
		AllowedEDIDs:         append([]string(nil), configured.AllowedEDIDs...),
	}
}

// Helper is the privileged, deliberately narrow policy writer used by pkexec.
func Helper(action string, reader io.Reader) error {
	return helper(action, reader, helperDeps{
		policyPath: PolicyPath,
		geteuid:    os.Geteuid,
		chown:      os.Chown,
		systemctl: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), helperCommandTimeout)
			defer cancel()
			output, err := exec.CommandContext(ctx, systemctlPath, "enable", "--now", startupUnit).CombinedOutput()
			if err != nil {
				return fmt.Errorf("enable %s: %w: %s", startupUnit, err, strings.TrimSpace(string(output)))
			}
			return nil
		},
	})
}

type helperDeps struct {
	policyPath string
	geteuid    func() int
	chown      func(string, int, int) error
	systemctl  func() error
}

func helper(action string, reader io.Reader, deps helperDeps) error {
	if deps.geteuid() != 0 {
		return errors.New("dockstar-startup-helper must run as root")
	}

	var configured policy
	switch action {
	case "enable", "sync":
		cfg, err := decodeConfig(reader)
		if err != nil {
			return err
		}
		if action == "sync" {
			configured = synchronizedPolicy(cfg)
		} else {
			configured, err = policyFromConfig(cfg)
			if err != nil {
				return err
			}
		}
	case "disable":
		configured = disabledPolicy()
	default:
		return fmt.Errorf("unsupported startup-helper action %q", action)
	}

	if err := writePolicy(deps.policyPath, configured, deps.chown); err != nil {
		return err
	}
	if configured.Enabled {
		if err := deps.systemctl(); err != nil {
			return err
		}
	}
	return nil
}

func decodeConfig(reader io.Reader) (config.Config, error) {
	if reader == nil {
		return config.Config{}, errors.New("startup configuration is required")
	}
	limited := io.LimitReader(reader, maxConfigBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return config.Config{}, fmt.Errorf("read startup configuration: %w", err)
	}
	if len(data) > maxConfigBytes {
		return config.Config{}, fmt.Errorf("startup configuration exceeds %d bytes", maxConfigBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var cfg config.Config
	if err := decoder.Decode(&cfg); err != nil {
		return config.Config{}, fmt.Errorf("decode startup configuration: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return config.Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return config.Config{}, fmt.Errorf("validate startup configuration: %w", err)
	}
	return cfg, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("startup configuration contains trailing JSON")
		}
		return fmt.Errorf("decode startup configuration: %w", err)
	}
	return nil
}

func policyFromConfig(cfg config.Config) (policy, error) {
	if cfg.Detection == "off" {
		return policy{}, errors.New("startup protection requires display detection")
	}
	if !cfg.Power.KeepAwakeWhileDocked {
		return policy{}, errors.New("startup protection requires keep-awake while docked")
	}

	allowed := make([]string, 0, len(cfg.Displays))
	for _, rule := range cfg.Displays {
		if !rule.Allowed || !edidID.MatchString(rule.ID) {
			continue
		}
		allowed = append(allowed, rule.ID)
	}
	if len(allowed) == 0 {
		return policy{}, errors.New("startup protection requires at least one explicitly approved EDID display")
	}
	sort.Strings(allowed)
	return policy{
		Version:              policyVersion,
		Enabled:              true,
		Detection:            "allowlist",
		KeepAwakeWhileDocked: true,
		AllowedEDIDs:         allowed,
	}, nil
}

func synchronizedPolicy(cfg config.Config) policy {
	configured, err := policyFromConfig(cfg)
	if err != nil {
		return disabledPolicy()
	}
	return configured
}

func disabledPolicy() policy {
	return policy{Version: policyVersion, Detection: "off", AllowedEDIDs: []string{}}
}

func validatePolicy(configured policy) error {
	if configured.Version != policyVersion {
		return fmt.Errorf("unsupported startup policy version %d", configured.Version)
	}
	if !configured.Enabled {
		if configured.Detection != "off" || configured.KeepAwakeWhileDocked || len(configured.AllowedEDIDs) != 0 {
			return errors.New("disabled startup policy contains active settings")
		}
		return nil
	}
	if configured.Detection != "allowlist" || !configured.KeepAwakeWhileDocked {
		return errors.New("enabled startup policy must use the EDID allowlist and keep-awake")
	}
	if len(configured.AllowedEDIDs) == 0 {
		return errors.New("enabled startup policy has no approved EDIDs")
	}
	previous := ""
	for _, id := range configured.AllowedEDIDs {
		if !edidID.MatchString(id) {
			return fmt.Errorf("invalid approved EDID %q", id)
		}
		if id <= previous {
			return errors.New("approved EDIDs must be unique and sorted")
		}
		previous = id
	}
	return nil
}

func writePolicy(path string, configured policy, chown func(string, int, int) error) error {
	if err := validatePolicy(configured); err != nil {
		return err
	}
	data, err := json.MarshalIndent(configured, "", "  ")
	if err != nil {
		return fmt.Errorf("encode startup policy: %w", err)
	}
	data = append(data, '\n')

	directory := filepath.Dir(path)
	if info, err := os.Lstat(directory); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("startup policy directory %s is not a real directory", directory)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect startup policy directory: %w", err)
	} else if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create startup policy directory: %w", err)
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		return fmt.Errorf("set startup policy directory permissions: %w", err)
	}
	if err := chown(directory, 0, 0); err != nil {
		return fmt.Errorf("own startup policy directory: %w", err)
	}

	temporary, err := os.CreateTemp(directory, ".startup-*")
	if err != nil {
		return fmt.Errorf("create startup policy: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	closeWithError := func(cause error) error {
		if closeErr := temporary.Close(); cause == nil {
			return closeErr
		}
		return cause
	}
	if err := temporary.Chmod(0o644); err != nil {
		return closeWithError(fmt.Errorf("set startup policy permissions: %w", err))
	}
	if err := chown(temporaryPath, 0, 0); err != nil {
		return closeWithError(fmt.Errorf("own startup policy: %w", err))
	}
	if _, err := temporary.Write(data); err != nil {
		return closeWithError(fmt.Errorf("write startup policy: %w", err))
	}
	if err := temporary.Sync(); err != nil {
		return closeWithError(fmt.Errorf("sync startup policy: %w", err))
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close startup policy: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace startup policy: %w", err)
	}
	if directoryFile, err := os.Open(directory); err == nil {
		err = directoryFile.Sync()
		_ = directoryFile.Close()
		if err != nil {
			return fmt.Errorf("sync startup policy directory: %w", err)
		}
	} else {
		return fmt.Errorf("open startup policy directory: %w", err)
	}
	return nil
}

func loadPolicy(path string, checkOwnership bool) (policy, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return policy{}, false, nil
	}
	if err != nil {
		return policy{}, false, fmt.Errorf("inspect startup policy: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return policy{}, false, errors.New("startup policy must be a non-writable regular file")
	}
	if checkOwnership {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return policy{}, false, errors.New("startup policy must be owned by root")
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return policy{}, false, fmt.Errorf("read startup policy: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var configured policy
	if err := decoder.Decode(&configured); err != nil {
		return policy{}, false, fmt.Errorf("decode startup policy: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return policy{}, false, err
	}
	if err := validatePolicy(configured); err != nil {
		return policy{}, false, fmt.Errorf("validate startup policy: %w", err)
	}
	return configured, true, nil
}

func policyConfig(configured policy) config.Config {
	cfg := config.Default()
	cfg.Detection = configured.Detection
	cfg.Power.KeepAwakeWhileDocked = configured.KeepAwakeWhileDocked
	cfg.Displays = make([]config.DisplayRule, 0, len(configured.AllowedEDIDs))
	for _, id := range configured.AllowedEDIDs {
		cfg.Displays = append(cfg.Displays, config.DisplayRule{
			ID: id, Allowed: true, Scale: 1, Mode: "preferred", Position: "right",
		})
	}
	return cfg
}

type protector interface {
	Ensure(context.Context, string) inhibit.Status
	Release(context.Context) inhibit.Status
}

type runDeps struct {
	readPolicy        func() (policy, bool, error)
	graphicalSessions func(context.Context) ([]graphicalSession, error)
	inhibitors        func(context.Context) ([]logindInhibitor, error)
	snapshot          func(context.Context, config.Config) (display.Snapshot, error)
	protector         protector
	now               func() time.Time
	ownPID            uint32
}

type observedSession struct {
	uid       uint32
	deadline  time.Time
	handedOff bool
}

type guard struct {
	deps      runDeps
	logger    *slog.Logger
	sessions  map[string]observedSession
	lastPhase string
	lastError string
}

// Run monitors physical DRM state and protects sleep through the bounded
// handoff to a graphical user's own Dockstar inhibitor.
func Run(ctx context.Context, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	coordinator := inhibit.New()
	runner := guard{
		logger:   logger,
		sessions: make(map[string]observedSession),
		deps: runDeps{
			readPolicy:        func() (policy, bool, error) { return loadPolicy(PolicyPath, true) },
			graphicalSessions: localGraphicalSessions,
			inhibitors:        logindInhibitors,
			snapshot: func(ctx context.Context, cfg config.Config) (display.Snapshot, error) {
				return display.DRMSnapshot(drmRoot, cfg)
			},
			protector: coordinator,
			now:       time.Now,
			ownPID:    uint32(os.Getpid()),
		},
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		cycleCtx, cancel := context.WithTimeout(ctx, operationTimeout)
		runner.reconcile(cycleCtx)
		cancel()

		select {
		case <-ctx.Done():
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), operationTimeout)
			status := coordinator.Release(releaseCtx)
			releaseCancel()
			if status.LastError != "" {
				return fmt.Errorf("release startup sleep protection: %s", status.LastError)
			}
			return nil
		case <-ticker.C:
		}
	}
}

func (g *guard) reconcile(ctx context.Context) {
	configured, installed, err := g.deps.readPolicy()
	if err != nil {
		g.release(ctx, "policy-error", "startup policy unavailable; sleep protection released", err)
		return
	}

	sessions, sessionErr := g.deps.graphicalSessions(ctx)
	if sessionErr == nil {
		g.observeSessions(sessions)
	}
	if !installed || !configured.Enabled {
		g.release(ctx, "disabled", "startup sleep protection is inactive", nil)
		return
	}
	if sessionErr != nil {
		g.release(ctx, "session-error", "cannot determine graphical login state; sleep protection released", sessionErr)
		return
	}

	if g.hasHandedOffSession() {
		g.markSessionsHandedOff()
		g.release(ctx, "user-session", "startup sleep protection remains handed off for the current graphical session", nil)
		return
	}

	var readinessErr error
	if len(g.sessions) != 0 {
		entries, err := g.deps.inhibitors(ctx)
		switch {
		case err != nil:
			readinessErr = fmt.Errorf("list graphical-session inhibitors: %w", err)
		case g.userProtectionReady(entries):
			g.markSessionsHandedOff()
			g.release(ctx, "user-session", "graphical Dockstar inhibitor active; startup sleep protection handed off", nil)
			return
		}
		if g.handoffExpired() {
			g.markSessionsHandedOff()
			g.release(ctx, "handoff-timeout", "graphical session did not become ready within 15 seconds; startup sleep protection released", nil)
			return
		}
	}

	snapshot, err := g.deps.snapshot(ctx, policyConfig(configured))
	if err != nil {
		g.release(ctx, "detection-error", "physical display detection failed; sleep protection released", err)
		return
	}
	approvedConnected := false
	for _, output := range snapshot.Outputs {
		if output.Connected && !output.Panel && output.Approved {
			approvedConnected = true
			break
		}
	}
	if !approvedConnected {
		g.release(ctx, "undocked", "no approved external display is physically connected", nil)
		return
	}

	status := g.deps.protector.Ensure(ctx, "startup-docked")
	if !status.Active {
		err := errors.New(status.LastError)
		if status.LastError == "" {
			err = errors.New("sleep inhibitor did not become active")
		}
		g.release(ctx, "inhibit-error", "startup sleep protection could not be acquired", err)
		return
	}
	if readinessErr != nil {
		g.transition("session-wait-error", "cannot verify graphical-session readiness; startup protection remains bounded by the handoff deadline", readinessErr)
		return
	}
	if len(g.sessions) != 0 {
		g.transition("session-wait", "graphical session detected; waiting for its Dockstar inhibitor", nil)
		return
	}
	g.transition("protected", "startup sleep protection active for approved dock", nil)
}

func (g *guard) observeSessions(current []graphicalSession) {
	now := g.deps.now()
	present := make(map[string]struct{}, len(current))
	for _, session := range current {
		present[session.ID] = struct{}{}
		observed, ok := g.sessions[session.ID]
		if !ok || observed.uid != session.UID {
			g.sessions[session.ID] = observedSession{
				uid:      session.UID,
				deadline: now.Add(sessionHandoffTimeout),
			}
		}
	}
	for id := range g.sessions {
		if _, ok := present[id]; !ok {
			delete(g.sessions, id)
		}
	}
}

func (g *guard) hasHandedOffSession() bool {
	for _, session := range g.sessions {
		if session.handedOff {
			return true
		}
	}
	return false
}

func (g *guard) markSessionsHandedOff() {
	for id, session := range g.sessions {
		session.handedOff = true
		g.sessions[id] = session
	}
}

func (g *guard) handoffExpired() bool {
	now := g.deps.now()
	for _, session := range g.sessions {
		if !now.Before(session.deadline) {
			return true
		}
	}
	return false
}

func (g *guard) userProtectionReady(entries []logindInhibitor) bool {
	for _, entry := range entries {
		if entry.Who != "Dockstar" || entry.Mode != "block" || entry.PID == g.deps.ownPID || !inhibitsSleep(entry.What) {
			continue
		}
		for _, session := range g.sessions {
			if entry.UID == session.uid {
				return true
			}
		}
	}
	return false
}

func (g *guard) release(ctx context.Context, phase, message string, cause error) {
	status := g.deps.protector.Release(ctx)
	if status.LastError != "" {
		cause = errors.Join(cause, errors.New(status.LastError))
	}
	g.transition(phase, message, cause)
}

func (g *guard) transition(phase, message string, err error) {
	errorText := ""
	if err != nil {
		errorText = err.Error()
	}
	if phase == g.lastPhase && errorText == g.lastError {
		return
	}
	g.lastPhase, g.lastError = phase, errorText
	if err != nil {
		g.logger.Error(message, "phase", phase, "error", err)
		return
	}
	if phase == "handoff-timeout" {
		g.logger.Warn(message, "phase", phase)
		return
	}
	g.logger.Info(message, "phase", phase)
}

type loginSession struct {
	ID   string
	UID  uint32
	User string
	Seat string
	Path dbus.ObjectPath
}

type graphicalSession struct {
	ID  string
	UID uint32
}

type logindInhibitor struct {
	What string
	Who  string
	Why  string
	Mode string
	UID  uint32
	PID  uint32
}

func localGraphicalSessions(ctx context.Context) ([]graphicalSession, error) {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("connect to system bus: %w", err)
	}
	defer conn.Close()

	var sessions []loginSession
	if err := conn.Object("org.freedesktop.login1", dbus.ObjectPath("/org/freedesktop/login1")).CallWithContext(
		ctx, "org.freedesktop.login1.Manager.ListSessions", 0,
	).Store(&sessions); err != nil {
		return nil, fmt.Errorf("list logind sessions: %w", err)
	}
	local := make([]graphicalSession, 0, len(sessions))
	for _, session := range sessions {
		properties := conn.Object("org.freedesktop.login1", session.Path)
		class, err := stringProperty(ctx, properties, "Class")
		if err != nil {
			return nil, fmt.Errorf("read session %s class: %w", session.ID, err)
		}
		if class != "user" && class != "user-early" {
			continue
		}
		sessionType, err := stringProperty(ctx, properties, "Type")
		if err != nil {
			return nil, fmt.Errorf("read session %s type: %w", session.ID, err)
		}
		if sessionType != "wayland" && sessionType != "x11" {
			continue
		}
		remote, err := boolProperty(ctx, properties, "Remote")
		if err != nil {
			return nil, fmt.Errorf("read session %s remote state: %w", session.ID, err)
		}
		if !remote {
			local = append(local, graphicalSession{ID: session.ID, UID: session.UID})
		}
	}
	return local, nil
}

func logindInhibitors(ctx context.Context) ([]logindInhibitor, error) {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("connect to system bus: %w", err)
	}
	defer conn.Close()

	var entries []logindInhibitor
	if err := conn.Object("org.freedesktop.login1", dbus.ObjectPath("/org/freedesktop/login1")).CallWithContext(
		ctx, "org.freedesktop.login1.Manager.ListInhibitors", 0,
	).Store(&entries); err != nil {
		return nil, fmt.Errorf("list logind inhibitors: %w", err)
	}
	return entries, nil
}

func inhibitsSleep(what string) bool {
	for _, category := range strings.Split(what, ":") {
		if category == "sleep" {
			return true
		}
	}
	return false
}

func stringProperty(ctx context.Context, object dbus.BusObject, name string) (string, error) {
	call := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.freedesktop.login1.Session", name)
	var property dbus.Variant
	if err := call.Store(&property); err != nil {
		return "", err
	}
	value, ok := property.Value().(string)
	if !ok {
		return "", fmt.Errorf("property %s is not a string", name)
	}
	return value, nil
}

func boolProperty(ctx context.Context, object dbus.BusObject, name string) (bool, error) {
	call := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.freedesktop.login1.Session", name)
	var property dbus.Variant
	if err := call.Store(&property); err != nil {
		return false, err
	}
	value, ok := property.Value().(bool)
	if !ok {
		return false, fmt.Errorf("property %s is not a boolean", name)
	}
	return value, nil
}
