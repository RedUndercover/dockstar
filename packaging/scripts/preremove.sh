#!/bin/sh
if [ "$1" = "remove" ] || [ "$1" = "0" ]; then
  systemctl --global disable dockstar.service >/dev/null 2>&1 || true
fi
