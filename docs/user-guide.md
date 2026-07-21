# User Guide

## Runtime pieces

Dockstar has two unprivileged desktop processes:

- `dockstar daemon` owns detection, configuration, layout changes, actions,
  runtime state, and the D-Bus API.
- `dockstar-ui` owns the persistent tray and the on-demand Wails settings
  window in one single-instance process.

Closing the settings window or tray does not stop automation. The daemon is a
systemd user service and can also be D-Bus activated.

## Tray

The tray refreshes status every five seconds. Its menu provides:

| Item | Behavior |
| --- | --- |
| Status | Shows undocked or the active Dockstar layout |
| Open Dockstar | Shows and focuses the settings window |
| Apply current profile | Forces an evaluation and reapplies the active profile |
| Layout | Saves and applies extend, mirror, external-only, or internal-only |
| Quit | Exits the tray and settings process, but not the daemon |

KDE starts the tray from
`$XDG_CONFIG_HOME/autostart/io.github.redundercover.Dockstar.Tray.desktop` for a
source installation, or `/etc/xdg/autostart` for a package installation.

## Settings window

The settings window has five views:

| View | Contents |
| --- | --- |
| Overview | Dock state, graphical topology, primary output, sleep protection, backend, update time, and quick layout application |
| Displays | Connected outputs, approval, mode, scale, position, primary priority, and EDID identity |
| Automation | Detection, layouts, system policies, restore behavior, closed-lid safety, and an outcome preview |
| Wake & power | Hardware-wake toggle, sleep-protection status, packaged-support status, controller states, and baseline restore |
| Diagnostics | The structured `dockstar diagnose` report, with refresh and copy actions |

The **Start Dockstar automatically** toggle under **Automation** controls both
the user service and KDE tray at sign-in. Turning it off does not interrupt the
current session. Opening Dockstar Settings later may still start the daemon on
demand through D-Bus activation.

Save operations use the revision loaded with the current UI state. If another
client changes the configuration in the meantime, Dockstar rejects the stale
save instead of overwriting newer settings. Refresh, reapply the intended
changes, and save again.

Closing the window hides it while the tray remains active. Launching
`dockstar-ui --settings` while the tray process already exists focuses that
same window rather than starting another settings client.

Closed-lid undock locking is edge-triggered and guarded against connection
flapping. After Dockstar locks or suspends once, it will not repeat that action
until the lid has been opened. Both closed-lid safety actions default to off and
must be enabled explicitly under **Automation**.

## Detection policies

### `allowlist`

Only external outputs with a matching `displays` rule and `allowed: true`
trigger docking. Unknown outputs produce the `PendingDisplay` D-Bus signal.

### `all`

Every connected non-panel output is approved. Device rules still supply label,
rank, scale, mode, and position overrides when present.

### `off`

No external output triggers the automatic dock state. Output discovery,
status, diagnostics, and `internal-only` manual application remain available.

## Layouts

### Extend

Dockstar enables approved external displays and the internal panel. The
selected external primary receives KScreen priority 1 and the panel priority 2.
Each external output is placed relative to the panel using its rule's
`position`. Scaling and a non-`preferred` mode are applied when configured.

### Mirror

Dockstar selects one external primary, finds the largest resolution that both
it and the internal panel advertise, and restricts candidates to the configured
refresh cap. It then submits one KDE output-management transaction that:

- Enables the internal panel and selected external output.
- Disables other known outputs.
- Sets both outputs to the common mode and scale 1.
- Places both at `0,0`.
- Makes the external display primary.
- Makes the internal panel replicate the external display.

Mirror mode requires KWin's `kde_output_management_v2` and
`kde_output_device_registry_v2` globals. These are KDE implementation-detail
protocols, so compatibility is intentionally contained in one backend file.

### External only

Dockstar enables all approved external outputs, makes the selected external
display primary, and disables internal panels.

### Internal only

Dockstar disables connected external outputs, enables the internal panel, and
gives it priority 1. This is the only layout that does not require an approved
external display.

## Primary selection

Dockstar sorts approved external outputs by `primaryRank`, with larger values
winning. When ranks are equal or rules are missing, the output with the largest
preferred pixel area wins. Equal results retain KScreen discovery order.

## Lid behavior

Each evaluation queries PowerDevil for the lid state. An open lid uses
`defaultLayout`; a closed lid uses `lidClosedLayout`. The default closed-lid
layout is `external-only`.

If the machine becomes undocked while the lid is closed, Dockstar can lock the
session and request suspend 500 milliseconds later. Suspend is conditional on
lock being enabled as well as suspend being enabled.

## Dock actions

Actions run after a successful display transaction on the first dock
transition and on a forced Apply.

### Audio

`prefer-display` saves the current default sink, lists Pulse sinks as JSON, and
selects the first sink whose name or description contains `hdmi` or
`displayport`. `restore: true` restores the saved sink on undock.

### Network

`prefer-wired` asks NetworkManager to connect the first disconnected Ethernet
device. `wired-only` does the same and disables Wi-Fi when Dockstar initiated
that Ethernet connection. `unchanged` performs no network action.

On undock Dockstar disconnects only Ethernet it connected itself and re-enables
Wi-Fi only when it disabled Wi-Fi itself.

### Power

When AC power is online, Dockstar saves the current power profile and applies
the configured `performance`, `balanced`, or `power-saver` profile. It restores
the saved profile on undock when `restore` is true.

**Keep computer awake while docked** is enabled by default. It prevents
automatic idle and lid sleep only after an approved external display creates
the Dockstar docked state. Plasma's native inhibitor is preferred, which means
choosing **Sleep** explicitly still works. Dockstar uses logind temporarily
while PowerDevil activates the inhibitor and as a compatibility fallback if
PowerDevil is unavailable.

The protection status appears on **Overview** and **Wake & power**:

| Status | Meaning |
| --- | --- |
| Ready | Enabled, with no approved dock currently active |
| Active | PowerDevil is protecting the docked session |
| Starting | The temporary logind bridge is covering PowerDevil warmup |
| Resume guard | Dockstar is waiting for display discovery after hardware wake |
| Disconnect guard | A short closed-lid disconnect window is active |
| Compatibility mode | logind is protecting the session because PowerDevil is unavailable |
| Unavailable | Neither backend is active, or Plasma blocked the inhibition |

Compatibility mode can block an explicit Sleep request because logind cannot
distinguish it from automatic sleep. The GUI shows this condition rather than
silently changing desktop settings.

## Restore lifetime

Audio, power, Ethernet, and Wi-Fi baselines are held in daemon memory. A daemon
restart while docked loses the original pre-dock values and may establish a new
baseline from the already-docked state. Apply configuration changes before
docking when exact restoration matters.

## Timing

- Display state is evaluated every two seconds.
- Identical connected-device, approval, and lid signatures are skipped.
- Resume from logind starts protection immediately when hardware wake is
  enabled, then polls after 250 milliseconds, every 500 milliseconds for the
  first five seconds, and every second for up to 20 seconds.
- PowerDevil activation is verified after six seconds; logind covers that
  warmup and is normally released afterward.
- Closed-lid undock protection lasts up to five seconds unless Dockstar is
  intentionally suspending.
- Platform commands have a 15-second timeout.
- Normal CLI operations have a 30-second context timeout.
