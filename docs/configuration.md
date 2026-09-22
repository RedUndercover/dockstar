# Configuration Reference

## Location and ownership

The default path is:

```text
$XDG_CONFIG_HOME/dockstar/config.json
```

When `XDG_CONFIG_HOME` is unset, this resolves to
`~/.config/dockstar/config.json`. `DOCKSTAR_CONFIG` overrides the complete path
and is primarily useful for tests and isolated daemon instances.

Dockstar creates the directory with mode `0700`, writes the file with mode
`0600`, synchronizes a temporary file, and atomically renames it over the
previous config.

## Complete default

```json
{
  "schemaVersion": 2,
  "revision": 1,
  "detection": "allowlist",
  "defaultLayout": "extend",
  "lidClosedLayout": "external-only",
  "mirror": {
    "source": "primary-external",
    "fallbackMaxRefreshHz": 60
  },
  "audio": {
    "policy": "prefer-display",
    "restore": true
  },
  "network": {
    "policy": "prefer-wired"
  },
  "power": {
    "onAcProfile": "performance",
    "restore": true,
    "keepAwakeWhileDocked": true
  },
  "security": {
    "lockOnClosedUndock": false,
    "suspendOnClosedUndock": false
  },
  "wake": {
    "enabled": false
  },
  "displays": []
}
```

The empty display list is intentional. No external display is trusted by a
fresh installation.

## Top-level fields

| Field | Type | Values and behavior |
| --- | --- | --- |
| `schemaVersion` | integer | Must be `2`; schema 1 is upgraded automatically when loaded |
| `revision` | unsigned integer | Must be greater than zero; incremented on every managed write |
| `detection` | string | `allowlist`, `all`, or `off` |
| `defaultLayout` | string | Layout used while the lid is open |
| `lidClosedLayout` | string | Layout used while the lid is closed |
| `mirror` | object | Mirror source and maximum fallback refresh |
| `audio` | object | Dock audio selection and restore behavior |
| `network` | object | Dock network policy |
| `power` | object | AC power profile and restore behavior |
| `security` | object | Closed-lid undock lock and suspend behavior |
| `wake` | object | Reserved user-level wake preference; not the system wake source of truth |
| `displays` | array | Per-device trust and layout rules |

## Detection and layouts

`detection` accepts:

| Value | Result |
| --- | --- |
| `allowlist` | A non-panel output is approved only by a matching allowed rule |
| `all` | Every connected non-panel output is approved |
| `off` | External outputs never create the automatic dock state |

Both layout fields accept `extend`, `mirror`, `external-only`, or
`internal-only`. See the [user guide](user-guide.md#layouts) for exact output
transactions.

## Mirror object

| Field | Valid value |
| --- | --- |
| `source` | Currently must be `primary-external` |
| `fallbackMaxRefreshHz` | Integer from 24 through 240 |

Common modes match when width and height are equal and refresh rates differ by
less than 1 Hz. Dockstar sorts candidates by pixel area and then refresh rate.

## Audio object

`policy` is `prefer-display` or `unchanged`. `restore` controls whether the
pre-dock default sink is restored on undock.
Failed restoration is retained for retry; `restore: false` explicitly discards
the saved baseline on undock, so re-enabling it later starts a fresh baseline.

The display sink search is case-insensitive and matches `hdmi` or
`displayport` in the Pulse sink name or description. It selects the first
matching sink reported by `pactl`.

## Network object

| Policy | Behavior |
| --- | --- |
| `prefer-wired` | Connect the first disconnected Ethernet device; leave Wi-Fi enabled |
| `wired-only` | Connect Ethernet and disable Wi-Fi when Dockstar initiated that connection |
| `unchanged` | Do not call NetworkManager |

## Power object

`onAcProfile` accepts `performance`, `balanced`, `power-saver`, or `unchanged`.
Profiles are changed only when a power supply of type `Mains` reports online.
`restore` controls whether the pre-dock profile is restored on undock.
Failures are retried on later undocked evaluations. `restore: false` discards
the saved baseline on undock rather than retaining a deferred restoration.

`keepAwakeWhileDocked` defaults to `true`. While an approved external display
keeps Dockstar in the docked state, the daemon holds one persistent logind
`sleep:idle:handle-lid-switch` block inhibitor. This blocks lid, idle, and
ordinary explicit Plasma sleep requests for as long as the protected dock session
exists. Use **Suspend
now** in Dockstar Settings or the tray, or run `dockstar suspend`, to release
the lease for one intentional suspend cycle. Dockstar never rewrites Plasma
power settings, `logind.conf`, or PAM configuration.

Protection is released on normal undock, when detection is turned off, when
this field is disabled, and when the daemon exits. A five-second closed-lid
undock guard absorbs display disconnect churn, then hands control back to the
desktop policy. An intentional Dockstar suspend releases protection before
requesting suspend and restores it after resume when the guarded conditions
still apply. See [Sleep Protection](sleep-protection.md) for the full lifecycle.

## Security object

`lockOnClosedUndock` locks through `org.freedesktop.ScreenSaver` when an active
dock is removed while the lid is closed. `suspendOnClosedUndock` requests
logind suspend 500 milliseconds after the lock. Suspend requires both settings,
which default to `false`. A confirmed re-dock or open lid rearms the closed-undock
transition. Re-dock, lid-open, config/policy changes, manual suspend, sleep/resume,
and daemon exit cancel a pending automatic request. The callback rechecks
connectivity and lid state after compositor discovery; unknown state cancels it.
Lock/suspend decisions do not depend on successful layout application.

## Wake object

`wake.enabled` is retained for UI/config compatibility but
is not consulted by the privileged helper. The authoritative system wake
policy is `/etc/dockstar/wake.json`, and live state comes from each controller's
`power/wakeup` sysfs file. Use `dockstar wake` commands rather than manually
editing this field.

## Display rules

Example:

```json
{
  "id": "edid:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "label": "Desk monitor",
  "allowed": true,
  "primaryRank": 20,
  "scale": 1.25,
  "mode": "2560x1440@60",
  "position": "right",
  "audioSink": "alsa_output.pci-0000_02_00.1.hdmi-stereo"
}
```

| Field | Type | Behavior |
| --- | --- | --- |
| `id` | string | Required unique Dockstar identity |
| `label` | string | Human-readable label shown in status and UI |
| `allowed` | boolean | Trust decision for `allowlist` detection |
| `primaryRank` | integer | Higher ranks win primary selection |
| `scale` | number | From 0.5 through 4; applied to non-mirror external layouts |
| `mode` | string | `preferred` or a value accepted by `kscreen-doctor output.NAME.mode.VALUE` |
| `position` | string | `left`, `right`, `above`, or `below` |
| `audioSink` | string | Reserved; current audio selection does not consult it |

Device IDs must be unique. Mode strings are passed to KScreen and are not
validated beyond being strings, so inspect `dockstar devices` before setting a
custom value.

## Display identity

Dockstar determines identity in this order:

1. Read at least 128 bytes from `/sys/class/drm/card*-CONNECTOR/edid`, hash the
   complete EDID with SHA-256, and prefix it with `edid:`.
2. Use the KScreen UUID prefixed with `kde:` when EDID is unavailable.
3. Hash `connector:CONNECTOR` with SHA-256 and prefix it with `connector:`.

EDID identity survives connector changes and is preferred for allowlisting.
The connector fallback is stable only while the display uses the same logical
connector name.
An explicitly allowed `kde:` or `connector:` identity can authorize an EDID-less
session display only when its connected DRM connector matches unambiguously.
Current rules are rechecked when cached session metadata is used. These fallback
identities cannot be enrolled in the root-owned pre-login allowlist.

## Revision rules

The UI and `UpdateConfig` D-Bus method use optimistic concurrency:

1. Read config JSON and its revision together.
2. Modify the JSON.
3. Submit it with the revision that was read.
4. Dockstar rejects the write with `RevisionConflict` if another writer already
   advanced the revision.

Convenience methods such as `allow`, `deny`, `trust`, and `mode` update under
the store lock and always increment the current revision.

## Schema migration

Dockstar accepts schema 1 configuration files, upgrades them to schema 2, sets
`power.keepAwakeWhileDocked` to `true`, advances the revision once, and writes
the migrated file atomically. Newer unknown schemas remain an error. The
migration does not edit any desktop or system power configuration.

## Editing by hand

1. Save a copy of the file.
2. Edit without changing `schemaVersion` or reducing `revision` to zero.
3. Run `dockstar config validate PATH`.
4. Replace the active file.
5. Restart the service or call D-Bus `Reload`.

```sh
cp ~/.config/dockstar/config.json /tmp/dockstar-config.json
$EDITOR /tmp/dockstar-config.json
dockstar config validate /tmp/dockstar-config.json
install -m 600 /tmp/dockstar-config.json ~/.config/dockstar/config.json
systemctl --user restart dockstar.service
```

Manual writes bypass revision coordination. Do not edit the file while the
settings window or another writer is saving it.
