# Architecture

`dockstar` owns configuration, device policy, display state, D-Bus, and docking
actions. It monitors KScreen state and logind resume events, debounces identical
snapshots, and emits state changes over `io.github.redundercover.Dockstar1`.

`dockstar-ui` runs either as a tray process or an on-demand Fyne settings
window. Both are D-Bus clients; neither owns automation state.

The display backend uses KScreen JSON for discovery and atomic KScreen
transactions for extend and single-display layouts. Mirror mode uses generated
pure-Go bindings for KDE output-management v2 because KScreen's CLI reports but
does not set replication. KDE's protocol is an implementation detail, so all
protocol-specific behavior remains inside the display backend.

`dockstar-wake-helper` is the only privileged executable. It accepts fixed
actions and derives all writable sysfs paths from validated PCI identifiers.
The Polkit policy grants no privileged execution path to the primary binary or
UI.
