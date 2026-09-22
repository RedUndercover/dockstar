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
Without a restart, failed restoration remains pending and retries automatically;
inspect `dockstar status` for `lastError`. Successful items are not repeated.
Audio/power `restore: false` deliberately discards the saved baseline on undock.

## Closed-lid undock did not suspend

Both `lockOnClosedUndock` and `suspendOnClosedUndock` must be true. The daemon
must first have observed an active dock, then observe no approved external while
PowerDevil reports the lid closed.
A successful layout is not required. A re-dock, opened lid, policy/config change,
sleep/resume, or unknown connectivity/lid state cancels the delayed request.

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

Inspect the daemon's structured status and logind inhibitor list:

```sh
dockstar status | jq '.sleepProtection'
systemd-inhibit --list
```

During a protected phase, the expected values are `active: true`, `backend:
"logind"`, and `mode: "block"`, with a Dockstar row in
`systemd-inhibit --list`. `backend: "none"` is normal while undocked or during
`manual-suspend`. A `lastError` during `docked`, `resume-grace`,
`undock-grace`, or `snapshot-grace` means the lease could not be acquired.

Restart the user service after an upgrade or a logind restart:

```sh
systemctl --user restart dockstar.service
journalctl --user -u dockstar.service -b -n 100
```

If a docked state still reports `backend: "powerdevil"`, an older Dockstar
daemon is running. Current builds report only `logind` or `none`; restart the
service and verify the installed executable with `systemctl --user status
dockstar.service`.

## Manual Sleep is blocked while docked

This is expected while strict dock protection is active. Use **Suspend now**
on Dockstar's **Wake & power** page or tray menu, or run:

```sh
dockstar suspend
```

Dockstar releases protection for that request and restores it after resume. Do
not disable lid handling, edit `logind.conf`, or turn off the protection toggle
for routine suspend.

If the command reports an authorization or inhibition error, another system
policy or application is blocking suspend. Dockstar deliberately does not open
a Polkit prompt or request permission to ignore third-party inhibitors; inspect
`systemd-inhibit --list` and the user-service journal.

## Protection before login does not activate

The Settings Startup switch controls only the user session. Pre-login
protection requires the installed system helper/unit and explicit enrollment:

```sh
dockstar startup status
systemctl status dockstar-startup.service
journalctl -u dockstar-startup.service -b
```

If no policy is installed, approve a display with an `edid:` identity and run
`dockstar startup enable`, authenticating as an administrator. Use
`dockstar startup sync` after approval changes, including the final revocation.
Settings → Displays shows pending differences and has the same authenticated
controls. Trust-all never enrolls arbitrary displays.
Source `make install-user` does not install the privileged boot components.

At graphical login, the boot guard waits for a Dockstar sleep-block lease owned
by a local graphical session's UID, or for its fixed 15-second timeout, then
hands over. Locked sessions count as logged in. The guard does not reacquire
during overlapping sessions after handoff. Inspect normal `dockstar status` and
the user-service journal if it logs a handoff timeout. Unreadable physical state
or boot policy still releases protection immediately.

## Login reports three failed attempts

Dockstar does not submit passwords, synthesize authentication, change PAM, or
retry login. Rapid suspend and resume cycles can nevertheless repeatedly return
the machine to the display manager and make an authentication symptom appear
alongside a power-policy problem.

First verify that the upgraded daemon is running:

```sh
systemctl --user restart dockstar.service
dockstar status | jq '.sleepProtection.backend, .sleepProtection.mode'
journalctl -b | grep -E 'PowerDevil|Suspending system|suspend requested'
```

`powerdevil` in Dockstar status identifies the removed implementation. The
current implementation reports `logind` while protected and does not depend on
PowerDevil application inhibition.

To separate a display-manager or authentication problem from Dockstar, disable
pre-login protection and stop the current daemon session:

```sh
dockstar startup disable
systemctl --user disable --now dockstar.service
```

Then inspect the boot's display-manager and authentication logs. Depending on
the installed display manager, useful units include `sddm.service` and
`systemd-logind.service`:

```sh
journalctl -b -u sddm.service -u systemd-logind.service
```

Re-enable the user daemon with `systemctl --user enable --now dockstar.service`
after the authentication issue is understood. Re-enroll approved boot devices
with `dockstar startup enable` if desired. The GUI keep-awake toggle releases
only the session inhibitor; boot enrollment is managed separately.

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
