# Dockstar

Dockstar is a small display docking controller for KDE Plasma 6 Wayland. It
recognizes external monitors, applies an atomic KWin layout, and coordinates
audio, networking, power, lock, and suspend behavior around dock transitions.

The daemon and CLI are written in Go. Mirror mode talks directly to KDE's
output-management protocol; other layouts use KScreen's atomic command API.
The tray and settings window are separate from the daemon, so display
automation continues when the UI is closed.

## Features

- Approve individual displays by EDID fingerprint, trust all displays, or turn
  automatic docking off.
- Extend with the highest-resolution approved external display as primary,
  mirror at the best common mode, or use either internal or external outputs.
- Use a separate closed-lid layout.
- Prefer display audio, wired networking, and an AC power profile while docked,
  then restore the previous state on undock.
- Lock and suspend after a closed-lid undock.
- Optionally enable eligible PCI display, USB, and Thunderbolt controllers as
  hardware wake sources through a narrowly scoped Polkit helper.
- Inspect and control the daemon through D-Bus, JSON CLI output, or the settings
  window.

## Requirements

- Linux amd64
- KDE Plasma 6 on Wayland
- KScreen and `kscreen-doctor`
- PipeWire Pulse (`pactl`) for audio automation
- NetworkManager (`nmcli`) for networking automation
- power-profiles-daemon for power-profile automation

The last three integrations are optional when their policies are set to
`unchanged`.

## Build

Go 1.25 or newer and the normal Fyne Linux development libraries are required.

```sh
make build
make test
```

Install the development build for the current user:

```sh
make install-user
```

The source installer starts `dockstar.service` but does not install or enable
the privileged wake layer. Distribution packages include the Polkit policy and
sleep hook; wake sources still remain untouched until explicitly enabled in
Dockstar Settings or with `dockstar wake enable`.

## Commands

```text
dockstar status
dockstar devices
dockstar allow DEVICE_ID
dockstar deny DEVICE_ID
dockstar trust all|allowlist|off
dockstar mode extend|mirror|external-only|internal-only
dockstar apply
dockstar diagnose
dockstar wake status|enable|disable|restore
dockstar config show|validate [PATH]
```

Configuration lives at `$XDG_CONFIG_HOME/dockstar/config.json`. The file is
versioned and updates use revision checks so simultaneous UI and CLI edits do
not overwrite each other.

## Documentation

- [Documentation index](docs/README.md)
- [Getting started](docs/getting-started.md)
- [User guide](docs/user-guide.md)
- [CLI reference](docs/cli.md)
- [Configuration reference](docs/configuration.md)
- [Hardware wake](docs/wake.md)
- [Architecture](docs/architecture.md)
- [D-Bus API](docs/dbus-api.md)
- [Development guide](docs/development.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Small Markdown wiki](wiki/Home.md)

## Hardware wake limitations

Firmware must resume the machine before Linux can read a newly attached
monitor's EDID. The display allowlist controls Dockstar's actions after resume;
it cannot prevent firmware from waking for an unknown device. Direct HDMI and
DisplayPort wake behavior also depends on laptop firmware and controller
support.

`dockstar wake restore` restores every source changed during initial setup to
its recorded baseline.

## Packaging and release

GoReleaser and nFPM produce an amd64 tarball plus Arch, Debian, and RPM
packages:

```sh
make package
```

Release archives include checksums and SBOMs. Tagged GitHub releases are built
by the release workflow.

## License

Dockstar is available under the MIT License.
