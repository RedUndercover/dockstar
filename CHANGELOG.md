# Changelog

## Unreleased

- Initial Go implementation of the Dockstar daemon, CLI, tray, settings UI,
  Plasma Wayland display backend, optional wake helper, packaging, and CI.
- Add comprehensive user, configuration, wake, architecture, D-Bus,
  development, release, and troubleshooting documentation plus a compact
  Markdown wiki.
- Replace the Fyne settings tabs with a responsive Wails interface derived from
  the Dockstar design mockups, including graphical display status, display rule
  editing, live automation outcomes, wake controls, and diagnostics.
- Add default-on, dock-scoped automatic sleep protection with native
  PowerDevil inhibition, a bounded logind bridge and fallback, resume and
  closed-lid disconnect guards, live GUI status, schema 1 migration, and
  recovery documentation without changing the user's desktop power policy.
