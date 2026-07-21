# Troubleshooting

## Start with diagnostics

```sh
dockstar diagnose
systemctl --user status dockstar.service
journalctl --user -u dockstar.service --since '15 minutes ago'
```

For more daemon detail:

```sh
systemctl --user edit dockstar.service
```

Add:

```ini
[Service]
Environment=DOCKSTAR_DEBUG=1
```

Then reload and restart:

```sh
systemctl --user daemon-reload
systemctl --user restart dockstar.service
```

## Daemon is unavailable

Symptoms include `connect to daemon`, D-Bus ownership errors, or a tray label
that says the daemon is unavailable.

```sh
systemctl --user restart dockstar.service
busctl --user list | grep io.github.redundercover.Dockstar
busctl --user introspect \
  io.github.redundercover.Dockstar \
  /io/github/redundercover/Dockstar
```

If a development daemon is already running, it may own the name. Find it with:

```sh
pgrep -af 'dockstar.*daemon'
```

Only one instance may own the well-known bus name.

## Daemon will not start after editing config

```sh
dockstar config validate ~/.config/dockstar/config.json
journalctl --user -u dockstar.service -b
```

Restore valid JSON, schema version 2, a revision greater than zero, and valid
enum values. Duplicate display IDs and scales outside 0.5 through 4 are
rejected.

Schema 1 files are migrated automatically. If a manually edited schema 2 file
is rejected, compare its `power` object with the complete example in the
[Configuration Reference](configuration.md).

## External display is connected but nothing happens

```sh
dockstar devices | jq '.[] | {connector, deviceId, panel, connected, approved}'
dockstar config show | jq '{detection, displays}'
```

For `allowlist`, approve the exact ID reported by the live output:

```sh
dockstar allow 'edid:DEVICE_FINGERPRINT'
dockstar apply
```

Check that detection is not `off`. Dockstar evaluates every two seconds, but a
forced apply is useful after manual edits.

## Display ID changed

An `edid:` ID should survive connector changes. A `kde:` or `connector:` ID may
change after compositor data, hardware routing, or connector naming changes.
Run `dockstar devices`, approve the new ID, and deny the obsolete rule if
desired.

If EDID should be available, inspect:

```sh
find /sys/class/drm -path '*/edid' -size +0c -print
```

## Wrong display is primary

Inspect rule ranks and preferred sizes:

```sh
dockstar config show | jq '.displays[] | {label, id, primaryRank}'
dockstar devices | jq '.[] | select(.panel == false) | {label, deviceId, preferredModeIds, modes}'
```

Give the intended external display a larger `primaryRank`. When ranks tie,
Dockstar chooses the largest preferred resolution.

## Extend placement is wrong

Confirm each external rule has one of `left`, `right`, `above`, or `below` and
a valid scale. Dockstar anchors the internal panel at `0,0` and accumulates
logical sizes around it. Fractional scale changes logical width and height.

Validate and reload after a manual edit:

```sh
dockstar config validate
busctl --user call \
  io.github.redundercover.Dockstar \
  /io/github/redundercover/Dockstar \
  io.github.redundercover.Dockstar1 Reload
dockstar apply
```

## Mirror fails

Typical errors are no common mirror mode, missing KDE output management, an
incomplete KWin UUID, or KWin rejecting the transaction.

```sh
dockstar status | jq '.lastError, .outputs[] | {connector, uuid, modes}'
systemctl --user edit dockstar.service
```

To capture Wayland protocol traffic, add `Environment=WAYLAND_DEBUG=1` to the
service override, restart the service, reproduce the mirror attempt, and read
the user journal. Remove the override afterward because protocol logs are
verbose.

Both displays must advertise the same width and height with refresh rates
within 1 Hz and at or below `mirror.fallbackMaxRefreshHz`. Try a 60 Hz cap and a
common 1920x1080 mode first.

Mirror is Plasma Wayland-specific. It does not fall back to XRandR or another
compositor protocol.

## Layout command failed but changed the default

`dockstar mode` saves `defaultLayout` before forcing application. If no
approved external exists, application can fail while the new default remains
saved. Choose another mode or edit the config, then apply when the required
display is present.

## Audio did not switch

```sh
pactl get-default-sink
pactl -f json list sinks | jq '.[] | {name, description}'
```

Dockstar selects the first sink containing `hdmi` or `displayport` in its name
or description. The per-display `audioSink` field is reserved and not currently
used. Set the policy to `unchanged` when automatic selection is undesirable.

## Ethernet or Wi-Fi behavior is unexpected

```sh
nmcli -t -f DEVICE,TYPE,STATE device status
nmcli radio wifi
```

Dockstar acts on the first disconnected Ethernet device. In `wired-only`, it
disables Wi-Fi only after it initiated that Ethernet connection. It does not
disconnect an Ethernet device that was already connected before docking.

## Power profile did not change

```sh
powerprofilesctl get
find /sys/class/power_supply -maxdepth 2 -name type -o -name online
```

Dockstar applies the profile only when a supply of type `Mains` reports
`online=1`. Confirm the configured profile is supported by
`powerprofilesctl list`.

## Old audio or power state was not restored

Restore baselines are stored only in daemon memory. A daemon restart while
docked loses the original pre-dock baseline. Restore manually, undock and dock
again, or restart Dockstar while undocked before the next session.

## Closed-lid undock did not suspend

Both `lockOnClosedUndock` and `suspendOnClosedUndock` must be true. The daemon
must first have observed an active dock, then observe no approved external while
PowerDevil reports the lid closed.

Check PowerDevil and logind:

```sh
qdbus6 org.kde.Solid.PowerManagement \
  /org/kde/Solid/PowerManagement \
  org.kde.Solid.PowerManagement.isLidClosed
loginctl show-session "$XDG_SESSION_ID" -p Remote -p Type -p State
```

## Wake command requests authentication and fails

Confirm the system package installed both helper and Polkit action:

```sh
ls -l /usr/bin/dockstar-wake-helper
ls -l /usr/share/polkit-1/actions/io.github.redundercover.Dockstar.policy
pkcheck --action-id io.github.redundercover.Dockstar.wake \
  --process $$ --allow-user-interaction
```

A source-only user installation does not provide the privileged layer.

## Machine does not wake when docked

```sh
dockstar wake status
cat /sys/power/mem_sleep
```

Verify the controller for the physical path reports `enabled`. Test USB-C,
Thunderbolt, direct HDMI, and direct DisplayPort separately. Firmware can ignore
an enabled Linux wake source, especially in deep suspend.

Hardware wake must already be possible before Dockstar can see the new display.
The automatic sleep-protection toggle cannot compensate for firmware that does
not generate a wake event.

## Sleep protection is unavailable

Inspect the daemon's structured status and both inhibition managers:

```sh
dockstar status | jq '.sleepProtection'
qdbus6 org.kde.Solid.PowerManagement \
  /org/kde/Solid/PowerManagement/PolicyAgent \
  org.freedesktop.DBus.Properties.Get \
  org.kde.Solid.PowerManagement.PolicyAgent ActiveInhibitions
systemd-inhibit --list
```

`backend: "powerdevil"` is the normal Plasma path. `backend: "logind"` with
`degraded: true` means compatibility protection is active; restart the user
service after PowerDevil is available to retry immediately:

```sh
systemctl --user restart dockstar.service
```

An error saying the inhibition was blocked in Plasma means the user explicitly
disabled Dockstar's inhibition in the battery applet. Dockstar respects that
choice and does not use logind to bypass it. Re-enable Dockstar in Plasma's
active-inhibition UI, or turn off **Keep computer awake while docked** in
Dockstar.

## Manual Sleep is blocked while docked

In the normal `powerdevil` mode, explicit Plasma Sleep remains available. In
`logind` compatibility mode, the lower-level inhibitor also blocks manual
sleep. Turn off **Keep computer awake while docked** temporarily or restore
PowerDevil and restart Dockstar. No global lid or suspend policy needs to be
changed.

## Login reports three failed attempts

Dockstar does not submit passwords, synthesize authentication, change PAM, or
retry login. Sleep protection is held through D-Bus inhibitor leases only. To
separate a display-manager or authentication problem from Dockstar, disable
startup and stop the current daemon session:

```sh
systemctl --user disable --now dockstar.service
```

Then inspect the boot's display-manager and authentication logs. Depending on
the installed display manager, useful units include `sddm.service` and
`systemd-logind.service`:

```sh
journalctl -b -u sddm.service -u systemd-logind.service
```

Re-enable Dockstar with `systemctl --user enable --now dockstar.service` after
the authentication issue is understood. Toggling sleep protection off in the
GUI releases an active Dockstar inhibitor immediately.

## Machine wakes too easily or uses more suspend power

Return to the original recorded state:

```sh
dockstar wake restore
```

Do not use `wake disable` when the goal is the original mixed controller state;
disable creates a policy that turns every managed source off.

## Tray is missing

The daemon can remain healthy without the tray. Start it manually:

```sh
LC_ALL=en_US.UTF-8 dockstar-ui
```

Check the KDE-only autostart entry and running process:

```sh
ls ~/.config/autostart/io.github.redundercover.Dockstar.Tray.desktop \
   /etc/xdg/autostart/io.github.redundercover.Dockstar.Tray.desktop 2>/dev/null
pgrep -af dockstar-ui
```

The normal autostart launch keeps the Wails window hidden. Click the tray icon
or run `dockstar-ui --settings` to show the existing single-instance window.

## Settings window is blank

Confirm the WebKitGTK 4.1 runtime is installed, then start the UI from a
terminal to collect WebKit errors:

```sh
pkg-config --modversion webkit2gtk-4.1
dockstar-ui --settings
```

Distribution packages declare WebKitGTK and GTK 3 runtime dependencies. Source
builds require the matching development packages during compilation.

## Settings save reports a revision conflict

Another writer changed config after the window loaded it. Close and reopen the
settings window, reapply the intended changes, and save again. The rejection is
protecting the newer config from being overwritten.

## Collecting a useful issue report

Include:

```sh
dockstar version
dockstar diagnose
kscreen-doctor -j
systemctl --user status dockstar.service --no-pager
journalctl --user -u dockstar.service -b --no-pager
```

Remove display serials, EDID fingerprints, usernames, and other identifiers if
they should not be public. For security issues, follow
[SECURITY.md](../SECURITY.md) rather than opening a public issue.
