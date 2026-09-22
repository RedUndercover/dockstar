# Dockstar Wiki

Welcome to the short-form guide to Dockstar. This wiki is meant to answer
"where does that happen?" quickly. The [full documentation](../docs/README.md)
contains installation, API, schema, wake, troubleshooting, and release details.

## Start here

| Page | Use it for |
| --- | --- |
| [Concepts](Concepts.md) | The vocabulary behind approval, docking, layouts, and wake |
| [Codebase Tour](Codebase-Tour.md) | A practical reading order through the Go packages |
| [Runtime Flow](Runtime-Flow.md) | What happens from hotplug or resume through apply and restore |
| [Common Tasks](Common-Tasks.md) | Commands and code-change recipes |

## One-minute model

Dockstar is a user daemon with D-Bus clients. The daemon polls KScreen, decides
whether an approved external display makes the laptop docked, applies a Plasma
layout, and coordinates session actions. The tray and settings window never own
the automation state.

Most layouts are one atomic `kscreen-doctor` command. Mirroring uses KDE's
Wayland output-management protocol because the KScreen CLI cannot set a
replication source.

Hardware wake is separate. A tiny Polkit-authorized helper changes validated PCI
`power/wakeup` files and records a restore baseline. The desktop daemon never
runs as root.

## Useful entry points

- User command: `cmd/dockstar/main.go`
- State machine: `internal/daemon/controller.go`
- Display backend: `internal/display/kscreen.go`
- Mirror transaction: `internal/display/wayland_mirror.go`
- Config schema: `internal/config/config.go`
- D-Bus contract: `internal/ipc/server.go`
- Dock actions: `internal/platform/actions.go`
- Wake boundary: `internal/wake/wake.go`

## Current scope

Version 1 supports Linux amd64 with KDE Plasma 6 Wayland and systemd 257 or
newer. It has one display backend, schema version 2, an empty allowlist by
default, and optional packaged hardware wake integration.
