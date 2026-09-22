# CLI Reference

The `dockstar` command is both the daemon entry point and the administrative
CLI. Commands that query or change desktop state call the running daemon over
the session bus. Configuration inspection, hardware wake, and startup commands
can run without the daemon.

All structured output is pretty-printed JSON. Errors are written to standard
error with the `dockstar:` prefix and produce a nonzero exit status.

## Daemon and version

### `dockstar daemon`

Starts the foreground daemon, claims `io.github.redundercover.Dockstar`, and
runs until SIGINT or SIGTERM. Normally systemd starts this command.

Set `DOCKSTAR_DEBUG=1` to include debug messages:

```sh
DOCKSTAR_DEBUG=1 dockstar daemon
```

### `dockstar version`

Prints the version, commit, and build date injected by Make or GoReleaser.

## Status and diagnostics

### `dockstar status`

Returns the complete runtime state:

```json
{
  "docked": false,
  "lidClosed": false,
  "layout": "",
  "primary": "",
  "backend": "KScreen/Plasma Wayland",
  "outputs": [],
  "updated": "2026-07-20T19:00:00Z"
}
```

Each output includes identity, connector, panel, connection and approval state,
priority, scale, geometry, current and preferred mode IDs, advertised modes,
and replication source.

### `dockstar devices`

Returns only the `outputs` array from `dockstar status`. Use this to find a
display ID for `allow` or `deny`.

### `dockstar diagnose`

Returns runtime status plus the configuration path, desktop name,
`WAYLAND_DISPLAY`, backend name, and hardware wake status or error.

## Display policy

### `dockstar allow DEVICE_ID`

Creates or updates a rule with `allowed: true`. A new rule receives scale 1,
mode `preferred`, position `right`, rank 0, and the latest observed label.

### `dockstar deny DEVICE_ID`

Creates or updates a rule with `allowed: false`. Denying does not delete the
rule, so its other preferences remain available.

### `dockstar trust all|allowlist|off`

Changes the detection policy. The daemon clears its cached signature so the
next poll reevaluates the connected outputs.

### `dockstar mode LAYOUT`

Valid layouts are `extend`, `mirror`, `external-only`, and `internal-only`.
This command saves `defaultLayout` and immediately forces evaluation. The
configuration change remains saved even when the immediate display application
fails, for example because no approved external display is present.

### `dockstar apply`

Forces evaluation even when the connected-device signature did not change. A
successful forced apply also reruns dock actions.

### `dockstar suspend`

Requests one intentional suspend through the running daemon. Dockstar releases
its strict logind sleep inhibitor, asks logind to suspend, and restores the
appropriate protection after resume. A failed request restores protection
immediately; a request that never begins sleeping is covered by a 15-second
watchdog.

The CLI does not ask for confirmation. Dockstar Settings and the tray expose
the same action with a confirmation dialog.

## Configuration

### `dockstar config show`

Loads, validates, and prints the active config file. If the file does not
exist, this command creates the default config.

### `dockstar config validate [PATH]`

Validates the specified JSON file. Without `PATH`, validates the active config.
Input is limited to 1 MiB.

Examples:

```sh
dockstar config validate
dockstar config validate ./example-config.json
```

Editing the file does not update an already running daemon until it is
restarted or its D-Bus `Reload` method is called.

## Pre-login protection

### `dockstar startup enable`

Authenticates with Polkit, snapshots explicitly approved EDID displays from
the current config into `/var/lib/dockstar/startup.json`, and enables and starts
`dockstar-startup.service`. Requires the system package, detection enabled,
keep-awake enabled, and at least one explicit EDID approval. Trust-all never
enrolls unapproved displays.

The service protects approved connected docks before login. At graphical login,
including a locked session, it hands over when a Dockstar sleep-block lease is
visible for a local graphical session's UID, or after a fixed 15-second timeout.
It does not reacquire during that login after handing over. Keep the normal user
daemon enabled for session automation. The boot guard does not change layouts,
audio, networking, or hardware wake.

### `dockstar startup sync`

Authenticates with Polkit and replaces the entire boot allowlist from the saved
session config. Use after additions, revocations, or detection/keep-awake changes.
Unlike `enable`, this accepts a final revocation: no explicit EDID approvals,
disabled detection, or disabled keep-awake writes a disabled boot policy.
Settings → Displays provides the same **Sync saved approvals** action and shows
pending additions/removals. Ordinary session approval changes never elevate
automatically.

### `dockstar startup status`

Prints the saved system policy, including `installed` (whether a policy exists),
`enabled`, and `allowedEdids`. This is not a live inhibitor or service-health
report; use `systemctl status dockstar-startup.service` and `systemd-inhibit
--list` for those. No administrator authentication is required.

### `dockstar startup disable`

Authenticates with Polkit and replaces the boot policy with a disabled policy.
The running guard releases its lease on the next poll. The user-session
configuration and hardware wake policy are unchanged.

## Hardware wake

### `dockstar wake status`

Discovers eligible controllers and prints their live sysfs wake state plus the
stored Dockstar system policy state. It does not require root.

### `dockstar wake enable`

Uses Polkit to run `dockstar-wake-helper enable`. The helper records the
original state of each managed source, writes the system policy, and sets every
managed source to `enabled`.

### `dockstar wake disable`

Uses the same path to set every managed source to `disabled`. This is a
Dockstar policy state, not a baseline restore.

### `dockstar wake restore`

Restores the first recorded baseline and removes Dockstar's policy and baseline
files.

## Completion

`dockstar completion bash|zsh|fish` prints a basic top-level completion script.
Packages install equivalent completion files in each shell's standard system
directory.

Examples:

```sh
source <(dockstar completion bash)
dockstar completion fish > ~/.config/fish/completions/dockstar.fish
```

## Useful JSON queries

With `jq` installed:

```sh
dockstar status | jq '{docked, layout, primary, error: .lastError}'
dockstar devices | jq '.[] | {connector, deviceId, approved}'
dockstar wake status | jq '.sources[] | {id, class, state}'
```
