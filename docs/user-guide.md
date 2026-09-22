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
| Suspend now | Confirms and releases Dockstar protection for one suspend cycle |
| Quit | Exits the tray and settings process, but not the daemon |

KDE starts the tray from
`$XDG_CONFIG_HOME/autostart/io.github.redundercover.Dockstar.Tray.desktop` for a
source installation, or `/etc/xdg/autostart` for a package installation.

## Settings window

The settings window has five views:

| View | Contents |
| --- | --- |
| Overview | Dock state, graphical topology, primary output, sleep protection, backend, update time, and quick layout application |
| Displays | Connected and saved displays, session and boot approvals, authenticated boot sync, mode, scale, position, primary priority, and identity |
| Automation | Detection, layouts, system policies, restore behavior, closed-lid safety, and an outcome preview |
| Wake & power | Hardware-wake toggle, sleep-protection status, intentional suspend, packaged-support status, controller states, and baseline restore |
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
Re-docking, opening the lid, changing configuration, manual suspend,
sleep/resume, or exiting the daemon cancels the pending request. At execution,
current connectivity and lid state must still confirm a closed undocked laptop;
unknown state cancels it. Lock/suspend transitions do not require a successful
display layout transaction.

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
the Dockstar docked state. Dockstar holds a persistent logind block for
`sleep:idle:handle-lid-switch`, covering PowerDevil sleep requests, logind idle
actions, and direct logind lid handling. This also blocks an
ordinary Plasma **Sleep** request while protected. Use Dockstar's **Suspend
now** action in the window or tray, or `dockstar suspend`, for a deliberate
one-cycle release.

The protection status appears on **Overview** and **Wake & power**:

| Status | Meaning |
| --- | --- |
| Ready | Enabled, with no approved dock currently active |
| Protected | logind is blocking sleep for the docked session |
| Resume guard | Dockstar is waiting for display discovery after hardware wake |
| Disconnect guard | A short closed-lid disconnect window is active |
| Suspending | Protection is released for one Dockstar suspend request |
| Unavailable | logind protection could not be acquired |

The daemon never changes the saved desktop power policy. After the five-second
closed-lid disconnect guard, that policy takes control again unless Dockstar's
explicit closed-lid suspend automation is enabled. The complete state and
verification commands are documented in [Sleep Protection](sleep-protection.md).

For protection at the login screen, install the system package and use **Enable**
under **Displays → Pre-login sleep protection**, or run `dockstar startup enable`
after explicitly approving EDID devices. This administrator-authorized boot
allowlist is separate from session approvals and hardware wake.

**Known displays** includes disconnected saved devices and entries retained only
by the boot policy. Each row shows both approval states. Session changes save
without elevation; **Sync saved approvals** (or `dockstar startup sync`) applies
the displayed differences with administrator authentication. **Revoke and sync**
also removes boot-only devices. Revoking the final approved EDID and syncing
disables pre-login protection. Authentication failure does not claim a successful
boot update. **Disable** or `dockstar startup disable` turns it off directly.
The normal Startup switch controls session startup only.

## Restore lifetime

Audio, power, Ethernet, and Wi-Fi baselines are held in daemon memory. A daemon
restart while docked loses the original pre-dock values and may establish a new
baseline from the already-docked state. Apply configuration changes before
docking when exact restoration matters.

Failed undock restoration remains pending and retries on subsequent evaluations,
even without a new hotplug event. Successful items are not repeated. Check
`lastError` in status for pending failures. Re-docking preserves unresolved
baselines and reapplies dock policy. Explicitly setting audio or power
`restore: false` discards that saved baseline on undock instead of retrying it;
later opt-in captures a fresh baseline.

## Timing

- Display state is evaluated every two seconds.
- Unchanged signatures skip layout work, but not safety checks or pending restoration.
- Resume from logind starts protection when the pre-sleep state was docked or
  lid-closed, or when hardware wake is enabled. It polls after 250
  milliseconds, every 500 milliseconds for the first five seconds, and every
  second for up to 30 seconds. An open-lid undocked result releases early.
- Closed-lid undock protection lasts up to five seconds. An enabled Dockstar
  closed-lid suspend uses the intentional-suspend path after 500 milliseconds.
- An intentional suspend request that does not begin restores protection after
  a 15-second watchdog.
- Platform commands have a 15-second timeout.
- Normal CLI operations have a 30-second context timeout.
