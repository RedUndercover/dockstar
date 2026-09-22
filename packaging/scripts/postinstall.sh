#!/bin/sh
systemctl --global disable dockstar.service >/dev/null 2>&1 || true
systemctl daemon-reload >/dev/null 2>&1 || true
systemctl enable --now dockstar-wakeup.service >/dev/null 2>&1 || true
systemctl enable --now dockstar-startup.service >/dev/null 2>&1 || true
printf '%s\n' 'Dockstar installed. Startup and hardware wake can be enabled in Dockstar Settings.'
printf '%s\n' 'For approved docks before login, enroll them with: dockstar startup enable'
printf '%s\n' 'After an upgrade, restart an active daemon with: systemctl --user restart dockstar.service'
