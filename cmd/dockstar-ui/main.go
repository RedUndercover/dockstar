package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"fyne.io/systray"
	"github.com/redundercover/dockstar/internal/config"
	"github.com/redundercover/dockstar/internal/daemon"
	"github.com/redundercover/dockstar/internal/ipc"
)

//go:embed icon.png
var iconPNG []byte

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--settings" {
		runSettings()
		return
	}
	systray.Run(trayReady, func() {})
}

func trayReady() {
	systray.SetTemplateIcon(iconPNG, iconPNG)
	systray.SetTitle("Dockstar")
	systray.SetTooltip("Dockstar display controller")
	status := systray.AddMenuItem("Checking status...", "Current Dockstar state")
	status.Disable()
	systray.AddSeparator()
	settings := systray.AddMenuItem("Settings", "Open Dockstar settings")
	apply := systray.AddMenuItem("Apply", "Apply the current docking profile")
	layouts := systray.AddMenuItem("Layout", "Select display layout")
	extend := layouts.AddSubMenuItem("Extend", "Extend with the external display primary")
	mirror := layouts.AddSubMenuItem("Mirror", "Mirror both displays")
	external := layouts.AddSubMenuItem("External only", "Use only the external display")
	internal := layouts.AddSubMenuItem("Internal only", "Use only the laptop display")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit tray", "Exit the Dockstar tray")

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		refreshTray(status)
		for {
			select {
			case <-ticker.C:
				refreshTray(status)
			case <-settings.ClickedCh:
				launchSettings()
			case <-apply.ClickedCh:
				callDaemon("Apply")
			case <-extend.ClickedCh:
				callDaemon("SetLayout", "extend")
			case <-mirror.ClickedCh:
				callDaemon("SetLayout", "mirror")
			case <-external.ClickedCh:
				callDaemon("SetLayout", "external-only")
			case <-internal.ClickedCh:
				callDaemon("SetLayout", "internal-only")
			case <-quit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func refreshTray(item *systray.MenuItem) {
	client, err := ipc.Connect()
	if err != nil {
		item.SetTitle("Dockstar daemon unavailable")
		return
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, err := client.Status(ctx)
	if err != nil {
		item.SetTitle("Dockstar daemon unavailable")
		return
	}
	var state daemon.State
	if json.Unmarshal([]byte(raw), &state) != nil {
		item.SetTitle("Dockstar status unavailable")
		return
	}
	if state.Docked {
		item.SetTitle("Docked: " + state.Layout)
	} else {
		item.SetTitle("Undocked")
	}
}

func launchSettings() {
	executable, err := os.Executable()
	if err != nil {
		return
	}
	_ = exec.Command(executable, "--settings").Start()
}

func callDaemon(method string, args ...any) {
	client, err := ipc.Connect()
	if err != nil {
		return
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = client.Call(ctx, method, args...)
}

type settingsUI struct {
	window   fyne.Window
	client   *ipc.Client
	config   config.Config
	revision uint64
	status   daemon.State

	statusLabel *widget.Label
	displays    *fyne.Container
	detection   *widget.Select
	layout      *widget.Select
	lidLayout   *widget.Select
	audio       *widget.Select
	network     *widget.Select
	power       *widget.Select
	lock        *widget.Check
	suspend     *widget.Check
	wakeLabel   *widget.Label
	diagnostics *widget.Entry
}

func runSettings() {
	a := app.NewWithID("io.github.redundercover.Dockstar")
	if resource := fyne.NewStaticResource("dockstar.png", iconPNG); resource != nil {
		a.SetIcon(resource)
	}
	window := a.NewWindow("Dockstar Settings")
	window.Resize(fyne.NewSize(860, 620))
	client, err := ipc.Connect()
	if err != nil {
		window.SetContent(container.NewCenter(widget.NewLabel("Dockstar daemon is unavailable")))
		window.ShowAndRun()
		return
	}
	defer client.Close()
	u := &settingsUI{window: window, client: client}
	if err := u.load(); err != nil {
		dialog.ShowError(err, window)
	}
	u.build()
	window.ShowAndRun()
}

func (u *settingsUI) load() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rawConfig, revision, err := u.client.Config(ctx)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(rawConfig), &u.config); err != nil {
		return err
	}
	u.revision = revision
	rawStatus, err := u.client.Status(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(rawStatus), &u.status)
}

func (u *settingsUI) build() {
	u.statusLabel = widget.NewLabel("")
	u.statusLabel.Wrapping = fyne.TextWrapWord
	apply := widget.NewButtonWithIcon("Apply", theme.MediaReplayIcon(), func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := u.client.Call(ctx, "Apply"); err != nil {
			dialog.ShowError(err, u.window)
			return
		}
		u.refreshStatus()
	})
	statusTab := container.NewBorder(nil, container.NewHBox(layout.NewSpacer(), apply), nil, nil, u.statusLabel)

	u.displays = container.NewVBox()
	displayTab := container.NewVScroll(u.displays)

	u.detection = selectWidget([]string{"allowlist", "all", "off"}, u.config.Detection)
	u.layout = selectWidget([]string{"extend", "mirror", "external-only", "internal-only"}, u.config.DefaultLayout)
	u.lidLayout = selectWidget([]string{"extend", "mirror", "external-only", "internal-only"}, u.config.LidClosedLayout)
	u.audio = selectWidget([]string{"prefer-display", "unchanged"}, u.config.Audio.Policy)
	u.network = selectWidget([]string{"prefer-wired", "wired-only", "unchanged"}, u.config.Network.Policy)
	u.power = selectWidget([]string{"performance", "balanced", "power-saver", "unchanged"}, u.config.Power.OnACProfile)
	u.lock = widget.NewCheck("Lock on closed-lid undock", nil)
	u.lock.SetChecked(u.config.Security.LockOnClosedUndock)
	u.suspend = widget.NewCheck("Suspend on closed-lid undock", nil)
	u.suspend.SetChecked(u.config.Security.SuspendOnClosedUndock)
	form := widget.NewForm(
		widget.NewFormItem("Detection", u.detection),
		widget.NewFormItem("Default layout", u.layout),
		widget.NewFormItem("Closed-lid layout", u.lidLayout),
		widget.NewFormItem("Audio", u.audio),
		widget.NewFormItem("Network", u.network),
		widget.NewFormItem("AC power profile", u.power),
	)
	save := widget.NewButtonWithIcon("Save", theme.DocumentSaveIcon(), u.save)
	automationTab := container.NewBorder(nil, container.NewHBox(layout.NewSpacer(), save), nil, nil,
		container.NewVBox(form, u.lock, u.suspend))

	u.wakeLabel = widget.NewLabel("")
	u.wakeLabel.Wrapping = fyne.TextWrapWord
	enableWake := widget.NewButtonWithIcon("Enable wake sources", theme.ConfirmIcon(), func() { u.runWake("enable") })
	disableWake := widget.NewButtonWithIcon("Disable wake sources", theme.CancelIcon(), func() { u.runWake("disable") })
	wakeTab := container.NewBorder(nil, container.NewHBox(enableWake, disableWake), nil, nil, u.wakeLabel)

	u.diagnostics = widget.NewMultiLineEntry()
	u.diagnostics.Disable()
	refreshDiagnostics := widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), u.refreshDiagnostics)
	diagnosticsTab := container.NewBorder(nil, container.NewHBox(layout.NewSpacer(), refreshDiagnostics), nil, nil, u.diagnostics)

	tabs := container.NewAppTabs(
		container.NewTabItem("Status", statusTab),
		container.NewTabItem("Displays", displayTab),
		container.NewTabItem("Automation", automationTab),
		container.NewTabItem("Wake", wakeTab),
		container.NewTabItem("Diagnostics", diagnosticsTab),
	)
	u.window.SetContent(tabs)
	u.renderStatus()
	u.renderDisplays()
	u.refreshWake()
	u.refreshDiagnostics()
}

func selectWidget(options []string, selected string) *widget.Select {
	value := widget.NewSelect(options, nil)
	value.SetSelected(selected)
	return value
}

func (u *settingsUI) renderStatus() {
	state := "Undocked"
	if u.status.Docked {
		state = "Docked"
	}
	layoutName, primary := u.status.Layout, u.status.Primary
	if layoutName == "" {
		layoutName = "Not active"
	}
	if primary == "" {
		primary = "Not active"
	}
	text := fmt.Sprintf("%s\nLayout: %s\nPrimary: %s\nBackend: %s\nUpdated: %s", state, layoutName, primary, u.status.Backend, u.status.Updated.Local().Format(time.RFC1123))
	if u.status.LastError != "" {
		text += "\nLast error: " + u.status.LastError
	}
	u.statusLabel.SetText(text)
}

func (u *settingsUI) refreshStatus() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := u.client.Status(ctx)
	if err != nil {
		dialog.ShowError(err, u.window)
		return
	}
	if err := json.Unmarshal([]byte(raw), &u.status); err != nil {
		dialog.ShowError(err, u.window)
		return
	}
	u.renderStatus()
	u.renderDisplays()
}

func (u *settingsUI) renderDisplays() {
	u.displays.RemoveAll()
	for _, output := range u.status.Outputs {
		output := output
		allowed := widget.NewCheck("Approved", nil)
		allowed.SetChecked(output.Approved)
		if output.Panel {
			allowed.Disable()
		} else {
			allowed.OnChanged = func(value bool) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := u.client.Call(ctx, "ApproveDisplay", output.DeviceID, value); err != nil {
					dialog.ShowError(err, u.window)
				}
			}
		}
		resolution := fmt.Sprintf("%dx%d at %.2fx", output.Width, output.Height, output.Scale)
		identity := widget.NewLabel(output.Label + "\n" + output.Connector + "  " + resolution + "\n" + output.DeviceID)
		identity.Wrapping = fyne.TextWrapBreak
		u.displays.Add(container.NewBorder(nil, widget.NewSeparator(), allowed, nil, identity))
	}
	if len(u.status.Outputs) == 0 {
		u.displays.Add(widget.NewLabel("No displays detected"))
	}
	u.displays.Refresh()
}

func (u *settingsUI) save() {
	u.config.Detection = u.detection.Selected
	u.config.DefaultLayout = u.layout.Selected
	u.config.LidClosedLayout = u.lidLayout.Selected
	u.config.Audio.Policy = u.audio.Selected
	u.config.Network.Policy = u.network.Selected
	u.config.Power.OnACProfile = u.power.Selected
	u.config.Security.LockOnClosedUndock = u.lock.Checked
	u.config.Security.SuspendOnClosedUndock = u.suspend.Checked
	raw, err := json.Marshal(u.config)
	if err != nil {
		dialog.ShowError(err, u.window)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	revision, err := u.client.UpdateConfig(ctx, string(raw), u.revision)
	if err != nil {
		dialog.ShowError(err, u.window)
		return
	}
	u.revision = revision
	dialog.ShowInformation("Dockstar", "Settings saved", u.window)
}

func (u *settingsUI) refreshWake() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := u.client.JSONCall(ctx, "GetWakeStatus")
	if err != nil {
		u.wakeLabel.SetText(err.Error())
		return
	}
	var status struct {
		Installed bool                         `json:"installed"`
		Enabled   bool                         `json:"enabled"`
		Sources   []struct{ ID, State string } `json:"sources"`
	}
	if json.Unmarshal([]byte(raw), &status) != nil {
		u.wakeLabel.SetText("Wake status unavailable")
		return
	}
	text := fmt.Sprintf("Installed: %t\nEnabled: %t", status.Installed, status.Enabled)
	for _, source := range status.Sources {
		text += "\n" + source.ID + ": " + source.State
	}
	u.wakeLabel.SetText(text)
}

func (u *settingsUI) runWake(action string) {
	binary := findDockstar()
	cmd := exec.Command(binary, "wake", action)
	if output, err := cmd.CombinedOutput(); err != nil {
		dialog.ShowError(fmt.Errorf("%w: %s", err, string(output)), u.window)
		return
	}
	u.refreshWake()
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

func (u *settingsUI) refreshDiagnostics() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := u.client.JSONCall(ctx, "Diagnose")
	if err != nil {
		u.diagnostics.SetText(err.Error())
		return
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) == nil {
		formatted, _ := json.MarshalIndent(value, "", "  ")
		raw = string(formatted)
	}
	u.diagnostics.SetText(raw)
}
