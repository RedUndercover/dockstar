package main

import (
	"log"
	"time"

	"fyne.io/systray"
)

type trayController struct {
	app        *App
	statusItem *systray.MenuItem
}

func newTrayController(app *App) *trayController {
	return &trayController{app: app}
}

func (t *trayController) externalLoop() (start, stop func()) {
	return systray.RunWithExternalLoop(t.ready, func() {})
}

func (t *trayController) ready() {
	systray.SetTemplateIcon(iconPNG, iconPNG)
	systray.SetTitle("Dockstar")
	systray.SetTooltip("Dockstar display controller")
	systray.SetOnTapped(t.app.showWindow)

	showItem := systray.AddMenuItem("Open Dockstar", "Open Dockstar settings")
	t.statusItem = systray.AddMenuItem("Checking status...", "Current Dockstar state")
	t.statusItem.Disable()
	systray.AddSeparator()
	applyItem := systray.AddMenuItem("Apply current profile", "Apply the current docking profile")
	layouts := systray.AddMenuItem("Layout", "Select display layout")
	extendItem := layouts.AddSubMenuItem("Extend", "Extend with the external display primary")
	mirrorItem := layouts.AddSubMenuItem("Mirror", "Mirror both displays")
	externalItem := layouts.AddSubMenuItem("External only", "Use only the external display")
	internalItem := layouts.AddSubMenuItem("Internal only", "Use only the laptop display")
	suspendItem := systray.AddMenuItem("Suspend now", "Release Dockstar protection for one suspend")
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit", "Quit Dockstar")

	go t.handle(showItem.ClickedCh, t.app.showWindow)
	go t.handle(applyItem.ClickedCh, func() { t.run("Apply") })
	go t.handle(extendItem.ClickedCh, func() { t.run("SetLayout", "extend") })
	go t.handle(mirrorItem.ClickedCh, func() { t.run("SetLayout", "mirror") })
	go t.handle(externalItem.ClickedCh, func() { t.run("SetLayout", "external-only") })
	go t.handle(internalItem.ClickedCh, func() { t.run("SetLayout", "internal-only") })
	go t.handle(suspendItem.ClickedCh, t.suspendNow)
	go t.handle(quitItem.ClickedCh, t.app.quit)
	go func() {
		for range systray.TrayOpenedCh {
			t.refreshStatus()
		}
	}()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			t.refreshStatus()
		}
	}()
	t.refreshStatus()
}

func (t *trayController) suspendNow() {
	confirmed, err := t.app.SuspendNow()
	if err != nil {
		log.Printf("Dockstar tray: suspend: %v", err)
		systray.SetTooltip("Dockstar · Suspend failed")
		return
	}
	if confirmed {
		t.refreshStatus()
	}
}

func (t *trayController) handle(clicks <-chan struct{}, action func()) {
	for range clicks {
		action()
	}
}

func (t *trayController) run(method string, args ...any) {
	if err := t.app.callDaemon(25*time.Second, method, args...); err != nil {
		log.Printf("Dockstar tray: %s: %v", method, err)
		systray.SetTooltip("Dockstar · Action failed")
	} else {
		t.refreshStatus()
	}
}

func (t *trayController) refreshStatus() {
	state, err := t.app.statusSummary()
	if err != nil {
		if t.statusItem != nil {
			t.statusItem.SetTitle("Dockstar daemon unavailable")
		}
		systray.SetTooltip("Dockstar · Daemon unavailable")
		return
	}
	label := "Undocked"
	if state.Docked {
		label = "Docked · " + displayLabel(state.Layout)
	}
	if t.statusItem != nil {
		t.statusItem.SetTitle(label)
	}
	systray.SetTooltip("Dockstar · " + label)
}

func displayLabel(value string) string {
	switch value {
	case "external-only":
		return "External only"
	case "internal-only":
		return "Internal only"
	case "extend":
		return "Extend"
	case "mirror":
		return "Mirror"
	default:
		return value
	}
}
