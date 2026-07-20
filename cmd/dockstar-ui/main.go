package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	Config    config.Config `json:"config"`
	Revision  uint64        `json:"revision"`
	Status    daemon.State  `json:"status"`
	Wake      wake.Status   `json:"wake"`
	WakeError string        `json:"wakeError,omitempty"`
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

	state := UIState{Revision: revision}
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, findDockstar(), "wake", action)
	if output, err := command.CombinedOutput(); err != nil {
		return UIState{}, fmt.Errorf("change wake sources: %w: %s", err, string(output))
	}
	return a.GetState()
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
