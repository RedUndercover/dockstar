# Hardware Wake

## Two different events

Dockstar handles two distinct parts of docking:

1. Firmware and hardware must wake the CPU when a dock, USB-C cable, or display
   controller signals activity.
2. After Linux resumes, the user daemon identifies the display, applies trust
   policy, holds a short sleep-protection guard, wakes display power with
   KScreen, and configures the layout.

The daemon cannot read EDID or apply an allowlist while the CPU is asleep. An
unknown device may therefore wake the computer before Dockstar decides not to
configure it.

## Sleep protection after wake

Hardware wake and sleep protection solve different halves of the same flow.
When the root-owned wake policy is enabled, `PrepareForSleep(false)` starts a
30-second resume guard before display discovery. The same guard starts when
Dockstar observed a docked or lid-closed state before sleep. This prevents a
closed-lid machine from immediately following its normal automatic sleep
policy while KWin is still discovering the dock. As soon as an approved
display appears, the guard becomes docked protection. If none appears, it
expires and releases the inhibitor; an open-lid undocked result releases it
early.

The user-level `power.keepAwakeWhileDocked` setting controls this behavior and
defaults to enabled. It does not enable hardware wake by itself. Dockstar does
not require users to select **Do nothing** for lid-close behavior and does not
modify Plasma power settings, logind configuration, PAM, or authentication.

Dockstar uses one persistent logind `sleep` block because PowerDevil can treat
lid-close policy as explicit and bypass an application inhibitor. While the
lease is active, use Dockstar's **Suspend now** action or `dockstar suspend`
for a deliberate one-cycle release. See [Sleep Protection](sleep-protection.md)
for the lifecycle and [Troubleshooting](troubleshooting.md#sleep-protection-is-unavailable)
for diagnostics.

## Package requirement

Hardware wake setup requires these system-installed files:

| Path | Purpose |
| --- | --- |
| `/usr/bin/dockstar-wake-helper` | Narrow root helper |
| `/usr/share/polkit-1/actions/io.github.redundercover.Dockstar.policy` | Authorization policy |
| `/usr/lib/systemd/system-sleep/50-dockstar` | Reapply policy after resume |
| `/usr/lib/systemd/system/dockstar-wakeup.service` | Reapply the saved policy at boot |

The GUI also requires the system `pkexec` executable supplied by Polkit so it
can show the desktop authorization prompt without running the UI or daemon as
root.

The source-only user installer intentionally does not install them.

Dockstar Settings checks the helper, Polkit policy, and resume hook separately
from the wake policy in `/etc/dockstar/wake.json`. When the packaged support is
present, **Wake & power** exposes an **Allow docks to wake this computer**
toggle. Changing it requests administrator authorization through Polkit. A
source-only install instead shows the missing system components and keeps the
toggle unavailable rather than attempting an unsafe privileged installation
from a user-writable build directory.

## Discovering sources

Dockstar scans PCI controllers under `/sys/bus/pci/devices/*` and USB root hubs
under `/sys/bus/usb/devices/usb*`. A PCI device is managed when:

- Its PCI address matches `0000:BB:DD.F` with lowercase hexadecimal digits.
- Its class begins with `0x03` for a display controller or `0x0c03` for a USB
  controller, including Thunderbolt USB and NHI classes on supported systems.
- Its `power/wakeup` file is readable.

A USB root hub is managed when its name is strictly `usbN` and its
`power/wakeup` file is readable. Root hubs are required on systems where the
PCI xHCI controller is wake-capable but USB connect detection remains disabled
at the USB bus layer.

`dockstar wake status` reports each source ID, class, vendor, device, and live
`enabled` or `disabled` state.

## Enable

```sh
dockstar wake enable
```

Polkit authenticates the user, then the helper:

1. Discovers the current managed source set.
2. Records the original state of each source in
   `/var/lib/dockstar/wake-baseline.json` if no baseline exists.
3. Writes an enabled policy with PCI controller and USB root-hub source IDs to
   `/etc/dockstar/wake.json`.
4. Writes `enabled` to each validated source's `power/wakeup` file.

The baseline is intentionally write-once per source. Later enable or disable
operations preserve recorded values, while newly supported sources are added
with their then-current state.

## Disable versus restore

```sh
dockstar wake disable
```

`disable` writes a persistent Dockstar policy that sets every currently
discovered managed source to `disabled`. The original baseline remains stored.

```sh
dockstar wake restore
```

`restore` writes each first-recorded value back, then removes the Dockstar
policy and baseline. Use restore when uninstalling the system package or
returning control to the previous system configuration.

## Reapplication

The packaged system-sleep hook calls:

```sh
dockstar-wake-helper apply
```

after resume. If no policy exists, the helper fails harmlessly and the hook
continues. Native packages enable `dockstar-wakeup.service`, which performs the
same one-shot operation at boot. The service has no effect until
`/etc/dockstar/wake.json` exists, so installing Dockstar does not enable
hardware wake by itself.

Package upgrades also start the service once. This immediately reapplies an
existing policy and ensures that installations from versions which left the
service disabled become persistent without another wake authorization prompt.
Source-only user installations do not install or enable the system service.

## Privilege boundary

The daemon and UI never run as root. Polkit authorizes only the dedicated wake
helper path. The helper accepts `enable`, `disable`, `apply`, or `restore`; it
rejects arbitrary actions, malformed source identifiers, filesystem paths, and
wake states.

The helper derives each sysfs path from a validated PCI or USB-root identifier
and checks that the target exists before writing it. Policy JSON is written
atomically.

## Firmware limitations

Wake support is hardware-specific:

- USB-C and Thunderbolt hotplug usually depends on USB or Thunderbolt
  controller wake support.
- A direct HDMI or DisplayPort connector may depend on the GPU, root port, and
  firmware routing.
- Some firmware wakes only from shallow sleep, not deep suspend.
- A source can report `enabled` while firmware still ignores its signal.
- Enabling additional sources can increase suspended power consumption.

Test each physical path separately after enabling wake. Keep another way to
wake the machine available during the first test.

## Verification checklist

```sh
dockstar wake status
cat /sys/power/mem_sleep
systemctl suspend
```

After resume:

```sh
dockstar status
journalctl --user -u dockstar.service --since '5 minutes ago'
dockstar wake status
```

Confirm that the machine resumed, the intended display is approved, the layout
was applied, and the controller states still match the policy.
