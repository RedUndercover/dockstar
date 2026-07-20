# Common Tasks

## User tasks

Find and approve a monitor:

```sh
dockstar devices | jq '.[] | {label, connector, deviceId, approved}'
dockstar allow 'edid:DEVICE_FINGERPRINT'
dockstar apply
```

Switch layouts:

```sh
dockstar mode extend
dockstar mode mirror
dockstar mode external-only
dockstar mode internal-only
```

Return hardware wake to its original state:

```sh
dockstar wake restore
```

Inspect a problem:

```sh
dockstar diagnose
journalctl --user -u dockstar.service --since '10 minutes ago'
```

## Developer tasks

Build and test:

```sh
make build
make test
make race
```

Regenerate Wayland code:

```sh
make generate
git diff -- internal/protocol/protocol_gen.go
```

Run a development daemon:

```sh
systemctl --user stop dockstar.service
DOCKSTAR_CONFIG="$(mktemp -d)/config.json" DOCKSTAR_DEBUG=1 ./build/dockstar daemon
```

Check D-Bus:

```sh
busctl --user introspect \
  io.github.redundercover.Dockstar \
  /io/github/redundercover/Dockstar
```

Build release packages:

```sh
PATH="$HOME/go/bin:$PATH" make package
find dist -maxdepth 1 -type f -printf '%f\n' | sort
```

## Before changing behavior

1. Identify the owning package in [Codebase Tour](Codebase-Tour.md).
2. Add a fake-backed or temporary-root test.
3. Keep config, CLI, D-Bus, and package compatibility explicit.
4. Update the matching detailed document under `docs/`.
5. Run tests, vet, Staticcheck, vulnerability scanning, and a snapshot package.

For precise recipes, use the [Development Guide](../docs/development.md).
