# Dockstar Documentation

This directory is the detailed reference for using, operating, extending, and
releasing Dockstar. The shorter [Markdown wiki](../wiki/Home.md) is a guided
tour of the same codebase.

## For users

- [Getting started](getting-started.md): requirements, installation, first
  run, and approving the first display.
- [User guide](user-guide.md): tray, settings, layouts, docking actions, and
  expected dock and undock behavior.
- [CLI reference](cli.md): every command, arguments, side effects, and JSON
  output.
- [Configuration reference](configuration.md): the complete schema, defaults,
  validation, identity rules, and examples.
- [Sleep protection](sleep-protection.md): the logind lease, deliberate
  suspend path, timing, verification, and desktop-policy handoff.
- [Hardware wake](wake.md): setup, privilege boundary, controller discovery,
  restore behavior, and firmware limitations.
- [Troubleshooting](troubleshooting.md): diagnostics organized by symptom.

## For integrators and contributors

- [Architecture](architecture.md): processes, data flow, state machine,
  display backends, and security boundary.
- [D-Bus API](dbus-api.md): service identity, methods, signals, errors, and
  command-line examples.
- [Development guide](development.md): repository layout, build commands,
  tests, generated code, and change recipes.
- [Release guide](releasing.md): GoReleaser, nFPM packages, SBOMs, CI, and
  release verification.
- [Contributing](../CONTRIBUTING.md) and [security policy](../SECURITY.md).

## Version scope

These documents describe schema version 2 and the Go implementation.
Version 1 targets Linux amd64 running KDE Plasma 6 on Wayland with systemd 257
or newer. X11, GNOME, wlroots compositors, and arm64 are outside the current
compatibility promise.
