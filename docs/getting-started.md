# Getting Started

## What Dockstar does

Dockstar watches the active Plasma display configuration. When an approved
external display appears, it wakes the displays, applies the selected layout,
and runs optional audio, network, and power actions. When the last approved
external display disappears, it restores the state it changed and can lock or
suspend a closed-lid laptop.

The default installation is deliberately quiet:

- Detection uses an empty allowlist.
- Connecting an unknown display does not change the layout.
- Hardware wake sources are not changed.
- The daemon records the display as pending so it can be approved in the UI or
  CLI.

## Requirements

Dockstar version 1 requires:

- Linux amd64.
- KDE Plasma 6 running a Wayland session.
- KScreen and `/usr/bin/kscreen-doctor`.
- A user D-Bus session and systemd user manager.

The following tools are required only when their automation policy is active:

| Integration | Executable | Used for |
| --- | --- | --- |
| PipeWire Pulse | `/usr/bin/pactl` | Selecting and restoring display audio |
| NetworkManager | `/usr/bin/nmcli` | Connecting Ethernet and toggling Wi-Fi |
| power-profiles-daemon | `/usr/bin/powerprofilesctl` | Selecting and restoring the AC profile |
| Polkit | `/usr/bin/pkexec` | Authorizing hardware wake changes |

## Install from a package

Release builds provide Arch, Debian, and RPM packages. Package installation
adds the daemon, UI, root wake helper, user and system service files, D-Bus
activation, Polkit policy, desktop entries, icon, completions, manual page, and
documentation.

Examples:

```sh
sudo pacman -U dockstar_VERSION_linux_amd64.pkg.tar.zst
sudo apt install ./dockstar_VERSION_linux_amd64.deb
sudo rpm -U dockstar_VERSION_linux_amd64.rpm
```

Package installation makes the user service available globally. Hardware wake
remains disabled until a user explicitly enables it.

## Install from source

Install Go 1.25 or later, Node.js 22 or later, npm, GTK 3 development files,
and WebKitGTK 4.1 development files, then run:

```sh
git clone https://github.com/redundercover/dockstar.git
cd dockstar
make build
make test
make install-user
```

The source installer places files under `~/.local`, installs and starts
`dockstar.service`, and adds the tray to KDE autostart. It does not install the
Polkit policy, system sleep hook, or root helper. Use a system package when
hardware wake setup is required.

## Verify the installation

```sh
systemctl --user status dockstar.service
dockstar version
dockstar status
dockstar devices
```

`dockstar status` should report the `KScreen/Plasma Wayland` backend and at
least the internal panel. An undocked system normally has an empty `layout` and
`primary` value because no Dockstar layout is active.

## Approve the first external display

1. Connect the monitor or dock while the computer is awake.
2. Open Dockstar Settings from the tray or application menu.
3. Open **Displays** and enable **Approved device** for the external display.
4. Confirm the desired layout under **Automation**.
5. Select Apply or wait for the next two-second evaluation.

The CLI equivalent is:

```sh
dockstar devices
dockstar allow 'edid:DEVICE_FINGERPRINT'
dockstar apply
```

The quotes are optional for current identifiers but help scripts treat the
identifier as one argument.

## Choose a trust policy

```sh
dockstar trust allowlist
dockstar trust all
dockstar trust off
```

`allowlist` is the recommended default. `all` allows any external display to
trigger automation. `off` stops automatic docking; it does not stop the daemon
from reporting connected outputs.

## Uninstall a source build

From the repository:

```sh
make uninstall-user
```

The uninstaller stops the service and removes installed binaries and desktop
integration. It intentionally keeps
`$XDG_CONFIG_HOME/dockstar/config.json`. Remove that file separately only when
the saved device rules and policies should also be discarded.
