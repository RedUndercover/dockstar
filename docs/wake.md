# Hardware Wake

## Two different events

Dockstar handles two distinct parts of docking:

1. Firmware and hardware must wake the CPU when a dock, USB-C cable, or display
   controller signals activity.
2. After Linux resumes, the user daemon identifies the display, applies trust
   policy, wakes display power with KScreen, and configures the layout.

The daemon cannot read EDID or apply an allowlist while the CPU is asleep. An
unknown device may therefore wake the computer before Dockstar decides not to
configure it.

## Package requirement

Hardware wake setup requires these system-installed files:

| Path | Purpose |
| --- | --- |
| `/usr/bin/dockstar-wake-helper` | Narrow root helper |
| `/usr/share/polkit-1/actions/io.github.redundercover.Dockstar.policy` | Authorization policy |
| `/usr/lib/systemd/system-sleep/50-dockstar` | Reapply policy after resume |
| `/usr/lib/systemd/system/dockstar-wakeup.service` | Optional one-shot policy application |

The source-only user installer intentionally does not install them.

## Discovering sources

Dockstar scans `/sys/bus/pci/devices/*`. A device is managed when:

- Its PCI address matches `0000:BB:DD.F` with lowercase hexadecimal digits.
- Its class begins with `0x03` for a display controller or `0x0c03` for a USB
  controller, including Thunderbolt USB and NHI classes on supported systems.
- Its `power/wakeup` file is readable.

`dockstar wake status` reports the PCI ID, class, vendor, device, and live
`enabled` or `disabled` state for every managed source.

## Enable

```sh
dockstar wake enable
```

Polkit authenticates the user, then the helper:

1. Discovers the current managed source set.
2. Records the original state of each source in
   `/var/lib/dockstar/wake-baseline.json` if no baseline exists.
3. Writes an enabled policy and source IDs to `/etc/dockstar/wake.json`.
4. Writes `enabled` to each validated source's `power/wakeup` file.

The baseline is intentionally write-once. Later enable or disable operations do
not replace the original values.

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
continues. The optional `dockstar-wakeup.service` performs the same one-shot
operation and is not enabled by Dockstar automatically.

## Privilege boundary

The daemon and UI never run as root. Polkit authorizes only the dedicated wake
helper path. The helper accepts `enable`, `disable`, `apply`, or `restore`; it
rejects arbitrary actions, PCI identifiers, filesystem paths, and wake states.

The helper derives each sysfs path from a validated PCI identifier and checks
that the target exists before writing it. Policy JSON is written atomically.

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
