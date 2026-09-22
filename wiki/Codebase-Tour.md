# Codebase Tour

## Recommended reading order

### 1. Entrypoint

Start at `cmd/dockstar/main.go`. It shows which commands run locally, which use
D-Bus, and how the daemon assembles config, display, controller, and IPC.

### 2. Shared model

Read `internal/config/config.go` and `internal/display/display.go`. These files
define almost all values that cross subsystem boundaries.

### 3. State machine

Read `internal/daemon/controller.go`, especially `Run` and `Evaluate`. This is
the product behavior in one place: poll, resume, debounce, pending devices,
layout selection, dock actions, undock restore, and emitted state.

### 4. Display application

Read `internal/display/kscreen.go` for discovery, identity, approval, primary
selection, extend placement, and non-mirror transactions. Then read
`internal/display/wayland_mirror.go` for KDE global discovery and the mirror
transaction.

### 5. Desktop actions

`internal/platform/actions.go` contains every external command and desktop
D-Bus call. Its `Manager` remembers exactly what should be restored.

Read `internal/inhibit/inhibit.go` next. Its coordinator owns and validates the
persistent logind lease. The daemon decides when that lease should exist and
owns the one-shot intentional-suspend bypass; the package decides how to hold
and release the file descriptor.

### 6. Public API

`internal/ipc/server.go` is the wire contract. `internal/ipc/client.go` is the
small wrapper used by the CLI and UI.

### 7. Privileged code

Read `cmd/dockstar-wake-helper/main.go`, then `internal/wake/wake.go`. The
entrypoint intentionally accepts only one action argument; the package owns
discovery, validation, policy, baseline, and writes.

### 8. UI

`cmd/dockstar-ui/main.go` is the typed Wails-to-D-Bus adapter and process
lifecycle. `cmd/dockstar-ui/tray.go` owns the systray menu and polling. The
embedded application lives under `cmd/dockstar-ui/frontend`: `src/main.js`
owns state and interactions, while `src/styles.css` implements the shared
responsive visual system.

## Package dependency direction

```text
cmd/dockstar --------> config, daemon, display, ipc, wake
cmd/dockstar-ui -----> config, daemon models, ipc, Wails
cmd/wake-helper -----> wake

daemon -------------> config, display contract, inhibit, platform, wake status
inhibit ------------> logind D-Bus API
display ------------> config, generated protocol
ipc ----------------> config error identity and daemon Handler contract
platform -----------> config
wake ----------------> standard library and pkexec
```

`internal/protocol` is generated. Do not hand-edit it.

## Files that define compatibility

- Config compatibility: `internal/config/config.go`
- CLI compatibility: `cmd/dockstar/main.go`
- D-Bus compatibility: `internal/ipc/server.go`
- Display identity compatibility: `internal/display/kscreen.go`
- Privilege compatibility: `internal/wake/wake.go` and the Polkit policy
- Package layout: `.goreleaser.yaml`

Changes to these files deserve explicit tests and documentation updates.
