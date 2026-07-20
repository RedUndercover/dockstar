# Contributing

Use Go 1.25 or newer. Keep changes focused and include tests for behavior or
configuration changes.

```sh
make generate
make test
make race
make lint
```

Generated Wayland bindings are committed. Run `make generate` after changing a
protocol fixture and include the generated diff in the same commit.

Read the [development guide](docs/development.md) for the package map, runtime
setup, tests, protocol workflow, and subsystem change recipes. Update the
matching reference page and the [small wiki](wiki/Home.md) when a change affects
public behavior or code ownership.

Commit messages should use an imperative subject. Pull requests should explain
the affected docking workflow, verification performed, and any hardware or
Plasma-version assumptions.
