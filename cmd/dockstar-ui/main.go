package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/redundercover/dockstar/internal/config"
	"github.com/redundercover/dockstar/internal/daemon"
	"github.com/redundercover/dockstar/internal/ipc"
	"github.com/redundercover/dockstar/internal/wake"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed icon.png
var iconPNG []byte

type App struct {
	mu  sync.RWMutex
	ctx context.Context
}

type UIState struct {
	Config      config.Config `json:"config"`
	Revision    uint64        `json:"revision"`
	Status      daemon.State  `json:"status"`
	Wake        wake.Status   `json:"wake"`
	WakeSupport WakeSupport   `json:"wakeSupport"`
	WakeError   string        `json:"wakeError,omitempty"`
	Startup     StartupStatus `json:"startup"`
}

type WakeSupport struct {
	Manageable bool     `json:"manageable"`
	Persistent bool     `json:"persistent"`
	Missing    []string `json:"missing,omitempty"`
}

type StartupStatus struct {
	Enabled        bool   `json:"enabled"`
	ServiceEnabled bool   `json:"serviceEnabled"`
	TrayEnabled    bool   `json:"trayEnabled"`
	Error          string `json:"error,omitempty"`
}

const trayAutostartName = "io.github.redundercover.Dockstar.Tray.desktop"

var wakeSupportPaths = []struct {
	path       string
	label      string
	executable bool
	required   bool
	persistent bool
}{
	{path: "/usr/bin/pkexec", label: "Polkit authorization broker", executable: true, required: true},
	{path: "/usr/bin/dockstar-wake-helper", label: "privileged wake helper", executable: true, required: true},
	{path: "/usr/share/polkit-1/actions/io.github.redundercover.Dockstar.policy", label: "Polkit policy", required: true},
	{path: "/usr/lib/systemd/system-sleep/50-dockstar", label: "resume hook", executable: true, persistent: true},
}

func main() {
	showOnStart := len(os.Args) > 1 && os.Args[1] == "--settings"
	application := &App{}
	tray := newTrayController(application)
	startTray, stopTray := tray.externalLoop()
	startTray()
	defer stopTray()

	if err := wails.Run(&options.App{
		Title:             "Dockstar",
		Width:             1360,
		Height:            860,
		MinWidth:          940,
		MinHeight:         640,
		StartHidden:       !showOnStart,
		HideWindowOnClose: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: options.NewRGBA(17, 19, 22, 255),
		OnStartup:        application.startup,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "io.github.redundercover.Dockstar",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				application.showWindow()
			},
		},
		Linux: &linux.Options{
			Icon:        iconPNG,
			ProgramName: "dockstar",
		},
		Bind: []interface{}{application},
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
}

func (a *App) GetState() (UIState, error) {
	client, err := ipc.Connect()
	if err != nil {
		return UIState{}, fmt.Errorf("connect to Dockstar daemon: %w", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	rawConfig, revision, err := client.Config(ctx)
	if err != nil {
		return UIState{}, fmt.Errorf("load configuration: %w", err)
	}
	rawStatus, err := client.Status(ctx)
	if err != nil {
		return UIState{}, fmt.Errorf("load display status: %w", err)
	}

	state := UIState{
		Revision:    revision,
		WakeSupport: detectWakeSupport("/"),
		Startup:     detectStartupStatus(),
	}
	if err := json.Unmarshal([]byte(rawConfig), &state.Config); err != nil {
		return UIState{}, fmt.Errorf("decode configuration: %w", err)
	}
	if err := json.Unmarshal([]byte(rawStatus), &state.Status); err != nil {
		return UIState{}, fmt.Errorf("decode display status: %w", err)
	}
	if rawWake, wakeErr := client.JSONCall(ctx, "GetWakeStatus"); wakeErr != nil {
		state.WakeError = wakeErr.Error()
	} else if err := json.Unmarshal([]byte(rawWake), &state.Wake); err != nil {
		state.WakeError = err.Error()
	}
	return state, nil
}

func (a *App) SaveConfig(next config.Config, expectedRevision uint64) (UIState, error) {
	raw, err := json.Marshal(next)
	if err != nil {
		return UIState{}, fmt.Errorf("encode configuration: %w", err)
	}
	client, err := ipc.Connect()
	if err != nil {
		return UIState{}, fmt.Errorf("connect to Dockstar daemon: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, err = client.UpdateConfig(ctx, string(raw), expectedRevision)
	client.Close()
	if err != nil {
		return UIState{}, fmt.Errorf("save configuration: %w", err)
	}
	return a.GetState()
}

func (a *App) Apply() (UIState, error) {
	if err := a.callDaemon(25*time.Second, "Apply"); err != nil {
		return UIState{}, err
	}
	return a.GetState()
}

func (a *App) SetLayout(layout string) (UIState, error) {
	if err := a.callDaemon(25*time.Second, "SetLayout", layout); err != nil {
		return UIState{}, err
	}
	return a.GetState()
}

func (a *App) ApproveDisplay(id string, approved bool) (UIState, error) {
	if err := a.callDaemon(8*time.Second, "ApproveDisplay", id, approved); err != nil {
		return UIState{}, err
	}
	return a.GetState()
}

func (a *App) WakeAction(action string) (UIState, error) {
	if action != "enable" && action != "disable" && action != "restore" {
		return UIState{}, fmt.Errorf("unsupported wake action %q", action)
	}
	if support := detectWakeSupport("/"); !support.Manageable {
		return UIState{}, fmt.Errorf("hardware wake support is not installed; install the Dockstar system package, then try again")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, findDockstar(), "wake", action)
	if output, err := command.CombinedOutput(); err != nil {
		return UIState{}, fmt.Errorf("change wake sources: %w: %s", err, string(output))
	}
	return a.GetState()
}

func (a *App) SetStartupEnabled(enabled bool) (UIState, error) {
	if err := setServiceStartup(enabled); err != nil {
		return UIState{}, err
	}
	if err := setTrayStartup(enabled); err != nil {
		return UIState{}, err
	}
	return a.GetState()
}

func detectStartupStatus() StartupStatus {
	status := StartupStatus{}
	var problems []string
	serviceEnabled, err := serviceStartupEnabled()
	if err != nil {
		problems = append(problems, err.Error())
	}
	trayEnabled, err := trayStartupEnabled()
	if err != nil {
		problems = append(problems, err.Error())
	}
	status.ServiceEnabled = serviceEnabled
	status.TrayEnabled = trayEnabled
	status.Enabled = serviceEnabled && trayEnabled
	status.Error = strings.Join(problems, "; ")
	return status
}

func serviceStartupEnabled() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "systemctl", "--user", "is-enabled", "dockstar.service").CombinedOutput()
	value := strings.TrimSpace(string(output))
	if value == "enabled" || value == "enabled-runtime" {
		return true, nil
	}
	if value == "disabled" || value == "masked" || value == "masked-runtime" || value == "not-found" {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check Dockstar startup: %s", value)
	}
	return false, nil
}

func setServiceStartup(enabled bool) error {
	action := "disable"
	if enabled {
		action = "enable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "systemctl", "--user", action, "dockstar.service").CombinedOutput(); err != nil {
		return fmt.Errorf("%s Dockstar at startup: %w: %s", action, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func trayStartupEnabled() (bool, error) {
	userPath, err := trayAutostartPath()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(userPath)
	if err == nil {
		return !desktopEntryHidden(data), nil
	}
	if !os.IsNotExist(err) {
		return false, fmt.Errorf("read tray autostart entry: %w", err)
	}
	data, err = os.ReadFile(filepath.Join("/etc/xdg/autostart", trayAutostartName))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read system tray autostart entry: %w", err)
	}
	return !desktopEntryHidden(data), nil
}

func setTrayStartup(enabled bool) error {
	path, err := trayAutostartPath()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate Dockstar Settings: %w", err)
	}
	var hidden string
	if !enabled {
		hidden = "Hidden=true\n"
	}
	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Dockstar Tray
Icon=io.github.redundercover.Dockstar
Exec=/usr/bin/env LC_ALL=en_US.UTF-8 %s
OnlyShowIn=KDE;
X-KDE-autostart-after=panel
X-KDE-StartupNotify=false
NoDisplay=true
%s`, executable, hidden)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create autostart directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dockstar-autostart-*")
	if err != nil {
		return fmt.Errorf("create tray autostart entry: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install tray autostart entry: %w", err)
	}
	return nil
}

func trayAutostartPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user configuration: %w", err)
	}
	return filepath.Join(configDir, "autostart", trayAutostartName), nil
}

func desktopEntryHidden(data []byte) bool {
	for _, line := range bytes.Split(data, []byte("\n")) {
		if strings.EqualFold(strings.TrimSpace(string(line)), "Hidden=true") {
			return true
		}
	}
	return false
}

func detectWakeSupport(root string) WakeSupport {
	if root == "" {
		root = "/"
	}
	support := WakeSupport{}
	support.Manageable = true
	for _, component := range wakeSupportPaths {
		path := filepath.Join(root, filepath.Clean(component.path))
		info, err := os.Stat(path)
		present := err == nil && info.Mode().IsRegular()
		if present && component.executable {
			present = info.Mode().Perm()&0o111 != 0
		}
		if !present {
			support.Missing = append(support.Missing, component.label)
			if component.required {
				support.Manageable = false
			}
			continue
		}
		if component.persistent {
			support.Persistent = true
		}
	}
	return support
}

func (a *App) GetDiagnostics() (string, error) {
	client, err := ipc.Connect()
	if err != nil {
		return "", fmt.Errorf("connect to Dockstar daemon: %w", err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	raw, err := client.JSONCall(ctx, "Diagnose")
	if err != nil {
		return "", fmt.Errorf("load diagnostics: %w", err)
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) == nil {
		formatted, marshalErr := json.MarshalIndent(value, "", "  ")
		if marshalErr == nil {
			raw = string(formatted)
		}
	}
	return raw, nil
}

func (a *App) callDaemon(timeout time.Duration, method string, args ...any) error {
	client, err := ipc.Connect()
	if err != nil {
		return fmt.Errorf("connect to Dockstar daemon: %w", err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := client.Call(ctx, method, args...); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	return nil
}

func (a *App) statusSummary() (daemon.State, error) {
	client, err := ipc.Connect()
	if err != nil {
		return daemon.State{}, err
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := client.Status(ctx)
	if err != nil {
		return daemon.State{}, err
	}
	var state daemon.State
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return daemon.State{}, err
	}
	return state, nil
}

func (a *App) showWindow() {
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx == nil {
		return
	}
	wailsruntime.WindowShow(ctx)
	wailsruntime.WindowUnminimise(ctx)
}

func (a *App) quit() {
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx != nil {
		wailsruntime.Quit(ctx)
	}
}

func findDockstar() string {
	if binary, err := exec.LookPath("dockstar"); err == nil {
		return binary
	}
	if executable, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(executable), "dockstar")
	}
	return "dockstar"
}
