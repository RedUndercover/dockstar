#!/bin/sh
systemctl --global disable dockstar.service >/dev/null 2>&1 || true
printf '%s\n' 'Dockstar installed. Startup and hardware wake can be enabled in Dockstar Settings.'
