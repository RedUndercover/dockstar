# Sleep Protection

Dockstar keeps a docked laptop awake with a temporary systemd-logind inhibitor.
It does not change Plasma's saved power profile, `logind.conf`, PAM, or the
machine-wide lid-switch policy.

## Why Dockstar uses logind

Plasma application inhibitors are useful for idle activity, but PowerDevil can
treat a configured lid-close action as explicit and bypass them. Dockstar needs
the stronger contract while a closed laptop is driving an external display, so
the daemon holds this low-level lease on the system bus:

```text
Inhibit("sleep:idle:handle-lid-switch", "Dockstar", "Protecting an active dock session", "block")
```

logind returns a file descriptor. The same descriptor remains open for the
entire protected interval; losing the descriptor or the logind D-Bus owner
causes Dockstar to reacquire it on the next evaluation. There is no PowerDevil
fallback or warmup period.

The lease blocks ordinary Plasma and logind sleep requests, logind idle actions,
and direct logind lid handling while protection is active. This prevents lid
and idle policy from racing display hotplug state.

Strict same-user enforcement requires systemd 257 or newer. Distribution
packages declare that dependency, and source or archive builds refuse to report
an active lease on an older systemd release.

## When protection is active

| Phase | Starts when | Ends when |
| --- | --- | --- |
| `docked` | An approved external display makes Dockstar docked | Normal undock, disable, or daemon exit |
| `resume-grace` | The previous sleep state was docked or lid-closed, or the system wake policy is enabled | An approved dock appears, an open-lid undocked state is confirmed, or 30 seconds pass |
| `undock-grace` | The last approved display disappears while the lid is closed | The display returns or five seconds pass |
| `snapshot-grace` | Both physical and compositor discovery fail after a known dock | Discovery recovers, protection is disabled, or 30 seconds pass without extending the deadline |
| `manual-suspend` | Dockstar is deliberately requesting suspend | The request fails, its 15-second start watchdog expires, or the system resumes |

After a closed-lid disconnect guard expires, Dockstar releases its lease and
the normal desktop policy is in control again. If **Suspend on closed-lid
undock** is enabled, Dockstar instead uses its deliberate-suspend path after
locking the session; it does not leave the inhibitor open and hope another
component can bypass it.

The 500-millisecond automatic suspend is cancelled by re-docking, opening the
lid, config/policy changes, manual suspend, sleep/resume, or daemon shutdown.
Before requesting sleep it rechecks current policy, connectivity, and lid state;
unknown connectivity or lid state cancels the request. Security transitions do
not depend on successful KScreen discovery or layout application when physical
connectivity is known.

Turning off **Keep computer awake while docked** releases protection
immediately. Disabling detection, a normal open-lid undock, and daemon shutdown
also release it.

Sleep protection uses kernel DRM connection state and the same EDID approvals
as display control, rather than depending on a successful KScreen layout query.
Cached EDID on a disconnected connector does not count as a dock. A KScreen
failure cannot retain an undocked lease indefinitely; if neither discovery
source is usable, the bounded `snapshot-grace` applies. Display commands have
a five-second timeout so a hung compositor client cannot stall evaluation
indefinitely. Failed layout transactions are retried without requiring a new
hotplug event.
For EDID-less displays, an explicitly approved `kde:` or `connector:` session
identity can be matched through a unique connected DRM connector. The current
approval is checked again even when the session snapshot is cached. This does
not grant boot approval or reuse a cached EDID for an unidentified replacement.

## Suspending deliberately

Because the inhibitor is deliberately strict, use one of Dockstar's suspend
actions while protected:

- Select **Suspend now** on **Wake & power** in Dockstar Settings.
- Select **Suspend now** in the tray menu.
- Run `dockstar suspend`.

The GUI and tray ask for confirmation. The CLI is intended for scripts and
does not prompt.

For one suspend cycle the daemon:

1. Marks an intentional suspend as pending.
2. Releases the logind lease.
3. Waits up to one second for logind to stop listing Dockstar's own lease.
4. Calls `org.freedesktop.login1.Manager.Suspend` without interactive
   authorization.
5. Restores protection immediately if release cannot be confirmed, the request
   fails, or sleep never starts. This restore uses the last dock state and
   preserved grace deadline, so it does not depend on a fresh display query.
6. On `PrepareForSleep(false)`, starts the bounded resume guard when needed.

This bypass is in daemon memory only. It is not a configuration switch and
cannot leave a persistent system policy behind.

Dockstar never asks Polkit to override another application's inhibitor. If the
active session is not already allowed to suspend, the request fails and
Dockstar restores its own protection rather than opening an authentication
prompt.

## Verify the active lease

While an approved dock is active:

```sh
dockstar status | jq '.sleepProtection'
systemd-inhibit --list | grep Dockstar
```

The status should resemble:

```json
{
  "enabled": true,
  "active": true,
  "phase": "docked",
  "backend": "logind",
  "mode": "block",
  "acquiredAt": "2026-07-21T17:00:00Z",
  "degraded": false
}
```

`backend: "none"` is normal while undocked. When `active` is false and
`lastError` is present during a protected phase, inspect the user-service log:

```sh
journalctl --user -u dockstar.service -b
```

After upgrading a running installation, restart the daemon so the new binary
owns the lease:

```sh
systemctl --user restart dockstar.service
```

## Before login

After installing the system package and approving displays in your normal
session, enroll the approved EDID devices:

```sh
dockstar startup enable
dockstar startup status
systemctl status dockstar-startup.service
```

Enabling requires administrator authentication. The helper stores only explicit
approved EDID fingerprints in `/var/lib/dockstar/startup.json`; trust-all,
connector-only identities, and KDE session identities never grant boot approval.
The system service runs before the display manager, reads DRM directly, and
holds protection only while an enrolled external display is physically connected.
Unplugging or unreadable policy/hardware state releases its lease immediately.
On detecting a local graphical user session, including a locked one, it waits
for a Dockstar sleep-block lease owned by a local graphical session's UID.
It hands over once that lease appears, or at a fixed 15-second deadline if the
session daemon is unavailable. It does not extend the deadline or reacquire
after handoff during overlapping user sessions, so intentional session suspend
cannot be blocked by the boot guard.

The user daemon owns protection after handoff, so keep normal session startup
enabled. The system guard does not apply KWin layouts or change greeter
configuration, audio, networking, authentication, or hardware wake settings.

Enrollment is a root-owned snapshot, not a live link to a home directory.
After adding **or revoking** approvals, run `dockstar startup sync`, or use
**Sync saved approvals** under Settings → Displays. That page shows both
allowlists, pending differences, disconnected devices, and boot-only entries.
Synchronizing the final revocation, disabled detection, or disabled keep-awake
writes a disabled boot policy. To turn it off directly, use **Disable** or
`dockstar startup disable`. Session changes alone never update the root policy.

Inspect the pre-login service with:

```sh
journalctl -u dockstar-startup.service -b
```

## Hardware wake is separate

Sleep protection controls whether Linux may return to sleep. Hardware wake
controls whether a USB-C, Thunderbolt, USB, or display controller can wake the
CPU in the first place. Enabling one does not enable the other. See
[Hardware Wake](wake.md) for controller setup and firmware limitations.
