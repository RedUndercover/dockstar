# Development Guide

## Repository map

| Path | Responsibility |
| --- | --- |
| `cmd/dockstar` | CLI parsing, daemon assembly, signals, logging, and version output |
| `cmd/dockstar-ui` | Wails adapter, tray process, and embedded frontend |
| `cmd/dockstar-ui/frontend` | Vanilla JavaScript, CSS, Lucide icons, and Vite build |
| `cmd/dockstar-wake-helper` | Root helper entry point |
| `cmd/dockstar-startup-helper` | Authenticated boot-policy helper entry point |
| `internal/config` | Schema, validation, revisioning, and atomic storage |
| `internal/daemon` | Evaluation loop and dock state machine |
| `internal/display` | Physical DRM discovery, KScreen adapter, layout planning, Wayland mirror |
| `internal/inhibit` | Persistent logind sleep lease and release acknowledgement |
| `internal/ipc` | D-Bus server, client, introspection, signals, and error mapping |
| `internal/platform` | Audio, network, power, lid, lock, and suspend integration |
| `internal/protocol` | Generated Wayland client bindings |
| `internal/wake` | Controller discovery, policy, baseline, Polkit invocation, and sysfs writes |
| `internal/startup` | Pre-login guard, EDID enrollment, policy persistence, and session handoff |
| `protocols` | Pinned Wayland and KDE XML inputs |
| `packaging` | systemd, D-Bus, desktop, Polkit, icons, and install scripts |
| `.github/workflows` | CI and tagged release automation |

The [wiki codebase tour](../wiki/Codebase-Tour.md) is a shorter reading order.

## Toolchain

- Go 1.25 or newer; CI covers Go 1.25 and 1.26.
- Node.js 22 or newer and npm for the embedded frontend.
- A C toolchain, GTK 3 development files, and WebKitGTK 4.1 development files
  for `dockstar-ui`.
- KScreen for live integration checks.
- Optional `staticcheck`, `govulncheck`, GoReleaser, and Syft for full quality
  and release targets.

## Common targets

```sh
make build          # build all three binaries into build/
make test           # go test ./...
make race           # race-test internal packages
make generate       # regenerate Wayland bindings
make lint           # format, vet, staticcheck, and govulncheck
make package        # snapshot release packages in dist/
make install-user   # install and start a source build for this user
make uninstall-user
```

`make build` runs `npm ci` and the Vite production build before compiling Go.
The Wails binary uses the `production`, `desktop`, and, when available,
`webkit2_41` build tags. Generated frontend assets are embedded into
`dockstar-ui`.

Run the browser preview with representative mock state:

```sh
npm --prefix cmd/dockstar-ui/frontend run dev
```

The preview is for layout and interaction development. The installed Wails
window uses live state through the Go adapter and D-Bus.

## Run without installing

Use an isolated config so development does not alter normal rules:

```sh
export DOCKSTAR_CONFIG="$(mktemp -d)/config.json"
DOCKSTAR_DEBUG=1 ./build/dockstar daemon
```

In another terminal in the same desktop session:

```sh
./build/dockstar status
LC_ALL=en_US.UTF-8 ./build/dockstar-ui --settings
```

Only one daemon can own the D-Bus name. Stop the installed service first when
running a development daemon:

```sh
systemctl --user stop dockstar.service
```

Restart it after the test:

```sh
systemctl --user start dockstar.service
```

## Tests

### Config tests

`internal/config/config_test.go` covers valid defaults, revision conflicts,
display rule validation, and fuzzes policy, layout, and scale combinations.

### Display tests

`internal/display/kscreen_test.go` uses an injected fake command runner and a
temporary DRM tree. It verifies EDID identity, complete internal-only
transactions, and mirror common-mode selection without changing real outputs.

### Wake tests

`internal/wake/wake_test.go` builds a fake PCI sysfs tree under a temporary root
and verifies discovery, baseline recording, enable, restore, and action
rejection. Passing a non-root filesystem root makes privileged behavior
testable without root.

### Sleep-protection tests

`internal/inhibit/inhibit_test.go` verifies lease persistence, reacquisition,
failure reporting, and cleanup with fakes. The daemon tests cover dock, resume,
disconnect, intentional-suspend, request-failure, release-failure, and watchdog
state transitions.

An opt-in test acquires a real inhibitor from the current system bus, confirms
that logind reports suspend inhibited, verifies the lease survives beyond the
removed promotion window, and checks cleanup. It does not request suspend:

```sh
DOCKSTAR_INHIBIT_INTEGRATION=1 \
  go test ./internal/inhibit -run TestRealCoordinatorIntegration -count=1 -v
```

### Race and static analysis

```sh
go test -race ./internal/...
go vet ./...
staticcheck $(go list ./... | grep -v '/internal/protocol$')
govulncheck ./...
```

Staticcheck excludes generated protocol code because the generator emits two
intentional assignments that trigger `SA4006`. Generated code remains covered
by compilation, vet, tests, and drift checking.

## Protocol generation

`internal/protocol/protocol_gen.go` is generated from:

- `protocols/wayland.xml`
- `protocols/kde-output-device-v2.xml`
- `protocols/kde-output-management-v2.xml`

The `go:generate` directive pins `wlclgen` v1.1.2 and formats the output:

```sh
make generate
git diff -- internal/protocol/protocol_gen.go
```

Commit XML and generated changes together. CI regenerates the file and fails if
the worktree changes.

## Change recipes

### Add a config field

1. Add the typed field and default in `internal/config`.
2. Add validation only when invalid input would cause ambiguous behavior.
3. Add store or validation tests.
4. Add UI controls only if the field is user-facing.
5. Document the field and whether older configs require a schema migration.

Schema version 1 currently requires all typed objects. Introducing a backward
incompatible shape requires a migration path before incrementing
`SchemaVersion`.

### Add a D-Bus method

1. Extend `ipc.Handler`.
2. Implement the controller behavior.
3. Export the server method with D-Bus-compatible types.
4. Add introspection XML.
5. Add a client wrapper when the bundled CLI or UI uses it.
6. Document method signature, errors, and side effects.

Keep method signatures small; prefer versioned JSON strings for structured data
that may grow.

### Add a display backend

1. Implement `display.Backend` without leaking compositor types.
2. Preserve `Output.DeviceID` semantics and approval calculation.
3. Apply related output changes atomically when the compositor supports it.
4. Return a stable backend name for diagnostics.
5. Add fake-backed planning tests before hardware integration tests.

Backend selection is currently fixed in `runDaemon`; adding another backend
also requires explicit capability detection there.

### Add a dock action

Put session integrations in `internal/platform`. Record exactly what Dockstar
changes, restore only that state, apply a timeout, and return errors without
undoing an already successful display transaction.

### Change privileged behavior

Keep untrusted values out of filesystem paths and command names. Extend helper
actions only when the operation cannot be performed unprivileged, update the
Polkit policy if the executable path changes, and add fake-root tests for every
accepted and rejected input.

## Live verification

Before changing display behavior, record the current setup:

```sh
kscreen-doctor -j > /tmp/kscreen-before.json
dockstar diagnose > /tmp/dockstar-before.json
```

Test each layout with a recoverable setup. Mirror and external-only can disable
the internal panel, so keep terminal or SSH access available during early
backend work.

After testing:

```sh
journalctl --user -u dockstar.service --since '10 minutes ago'
dockstar diagnose
```

Do not run wake helper tests against `/` unless a real system wake change is
intended.
