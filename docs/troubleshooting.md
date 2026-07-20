# Troubleshooting

Run `dockstar diagnose` first. It reports the selected backend, session type,
outputs, recent error, configuration path, and hardware wake capabilities.

Use `systemctl --user status dockstar.service` and
`journalctl --user -u dockstar.service` for daemon logs. Set
`DOCKSTAR_DEBUG=1` in the service environment for debug logging.

If a display is pending, approve its ID in the Displays tab or run
`dockstar allow DEVICE_ID`. If mirroring fails, confirm both outputs advertise
a common resolution at 60 Hz or below.

Hardware wake can only be enabled from an installed system package because the
Polkit helper and policy are system files. Some firmware cannot wake from a
direct HDMI or DisplayPort hotplug even when every eligible controller reports
`enabled`.
