# Changelog

## Unreleased

## 0.1.0 - 2026-09-22

- Refactor dock detection to use physical DRM state for sleep protection,
  independent of compositor queries; bound backend failure grace and display
  command runtime, retry failed layouts, and preserve deferred dock actions.
- Serialize inhibitor lifecycle transitions, clear grace on disable/open-lid
  undock, rearm closed-lid undock on re-dock, and inhibit idle and direct lid
  handling as well as sleep.
- Add authenticated EDID enrollment and a hardened pre-login system guard,
  available through `dockstar startup enable|sync|disable|status`, with
  readiness-aware handoff to graphical user sessions and a fixed 15-second limit.
- Cancel and revalidate delayed closed-undock suspend across lifecycle changes,
  including hardware or lid changes during compositor discovery. Run security
  transitions independently of successful display layout application.
- Preserve explicitly approved session fallback identities on unambiguous
  EDID-less DRM connectors without extending boot enrollment beyond EDID.
- Show session and pre-login approvals together, including disconnected and
  boot-only devices, pending differences, and authenticated sync/revocation.
- Retain failed platform restorations for retry, preserve ownership on redock,
  and honor explicit audio and power restoration opt-outs.
- Initial Go implementation of the Dockstar daemon, CLI, tray, settings UI,
  Plasma Wayland display backend, optional wake helper, packaging, and CI.
- Add comprehensive user, configuration, wake, architecture, D-Bus,
  development, release, and troubleshooting documentation plus a compact
  Markdown wiki.
- Replace the Fyne settings tabs with a responsive Wails interface derived from
  the Dockstar design mockups, including graphical display status, display rule
  editing, live automation outcomes, wake controls, and diagnostics.
- Add default-on, dock-scoped sleep protection with one persistent logind
  `sleep` block lease, 30-second resume and five-second closed-lid disconnect
  guards, live GUI status, schema 1 migration, and recovery documentation
  without changing the user's desktop power policy.
- Require systemd 257 for strict same-user block-inhibitor semantics and encode
  the minimum in runtime checks and native package dependencies.
- Add a generation-scoped intentional suspend path with GUI and tray
  confirmation, `dockstar suspend`, D-Bus API support, immediate failure
  recovery, logind release acknowledgement, non-interactive authorization, and
  a display-independent restore path with a 15-second start watchdog.
