# D-Bus API

## Identity

| Item | Value |
| --- | --- |
| Bus | User session bus |
| Well-known name | `io.github.redundercover.Dockstar` |
| Object path | `/io/github/redundercover/Dockstar` |
| Interface | `io.github.redundercover.Dockstar1` |

The service also exports `org.freedesktop.DBus.Introspectable` on the same
object path.

Inspect the live interface with:

```sh
busctl --user introspect \
  io.github.redundercover.Dockstar \
  /io/github/redundercover/Dockstar \
  io.github.redundercover.Dockstar1
```

## Method table

| Method | Input | Output | Effect |
| --- | --- | --- | --- |
| `GetStatus` | none | `s statusJSON` | Returns the current runtime state |
| `GetConfig` | none | `s configJSON`, `t revision` | Returns config and its revision together |
| `UpdateConfig` | `s configJSON`, `t expectedRevision` | `t revision` | Validates and atomically replaces config |
| `Apply` | none | none | Forces evaluation and active profile application |
| `SetLayout` | `s layout` | none | Saves `defaultLayout` and forces evaluation |
| `ApproveDisplay` | `s id`, `b allowed` | none | Creates or updates a display rule |
| `SetTrustPolicy` | `s policy` | none | Saves `allowlist`, `all`, or `off` |
| `Reload` | none | none | Reloads and validates config from disk |
| `GetWakeStatus` | none | `s statusJSON` | Returns policy and live controller state |
| `Diagnose` | none | `s diagnosticsJSON` | Returns combined status and environment diagnostics |

`Apply`, `SetLayout`, `UpdateConfig`, and convenience mutations can block while
configuration or display operations complete. Clients should use a reasonable
timeout; the bundled CLI uses 30 seconds.

## JSON payloads

### Status

`GetStatus` returns a JSON object matching `daemon.State`:

| Field | Type | Meaning |
| --- | --- | --- |
| `docked` | boolean | At least one approved external is active under a non-off policy |
| `lidClosed` | boolean | Last observed PowerDevil lid state |
| `layout` | string | Last successfully applied Dockstar layout, or empty while inactive |
| `primary` | string | Connector chosen as primary, or empty while inactive |
| `lastError` | string | Most recent evaluation error; omitted when empty |
| `backend` | string | Display backend name |
| `outputs` | array | Complete normalized output records |
| `updated` | RFC 3339 timestamp | Time the state record was built |

### Configuration

`GetConfig` and `UpdateConfig` use the schema described in
[Configuration Reference](configuration.md). `UpdateConfig` ignores the input
`schemaVersion` and `revision` values for write sequencing: it sets the current
schema and advances the store's current revision after matching
`expectedRevision`.

### Diagnostics

`Diagnose` returns:

- `configPath`
- `backend`
- `waylandDisplay`
- `desktop`
- `status` or `statusError`
- `wake` or `wakeError`

## Signals

| Signal | Arguments | Emitted when |
| --- | --- | --- |
| `StateChanged` | `s statusJSON` | A state evaluation is stored |
| `PendingDisplay` | `s id`, `s label` | A connected external is not approved |
| `ApplyFailed` | `s message` | Snapshot or layout application fails |
| `WakeCapabilitiesChanged` | `s statusJSON` | Reserved by interface version 1; not currently emitted |

Pending signals may repeat after a forced evaluation or signature reset.
Clients should identify pending displays by ID rather than treating every
signal as a new device.

Monitor signals with:

```sh
busctl --user monitor io.github.redundercover.Dockstar
```

## Errors

General failures use:

```text
io.github.redundercover.Dockstar1.Error.Failed
```

A stale expected revision uses:

```text
io.github.redundercover.Dockstar1.Error.RevisionConflict
```

The first error argument is a human-readable message.

## `busctl` examples

Get status:

```sh
busctl --user call \
  io.github.redundercover.Dockstar \
  /io/github/redundercover/Dockstar \
  io.github.redundercover.Dockstar1 GetStatus
```

Approve a display:

```sh
busctl --user call \
  io.github.redundercover.Dockstar \
  /io/github/redundercover/Dockstar \
  io.github.redundercover.Dockstar1 ApproveDisplay \
  sb 'edid:DEVICE_FINGERPRINT' true
```

Set layout:

```sh
busctl --user call \
  io.github.redundercover.Dockstar \
  /io/github/redundercover/Dockstar \
  io.github.redundercover.Dockstar1 SetLayout \
  s mirror
```

Reload a hand-edited config:

```sh
busctl --user call \
  io.github.redundercover.Dockstar \
  /io/github/redundercover/Dockstar \
  io.github.redundercover.Dockstar1 Reload
```

For `UpdateConfig`, fetch JSON and revision in one call and use a D-Bus library
that preserves the unsigned 64-bit revision. The bundled Go client is the
reference implementation.
