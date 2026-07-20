#!/bin/sh
systemctl --global enable dockstar.service >/dev/null 2>&1 || true
printf '%s\n' 'Dockstar installed. Hardware wake sources remain disabled until enabled in Dockstar Settings.'
