#!/bin/sh
# Debian passes "remove", RPM passes "0", and Arch runs this only from its
# pre_remove lifecycle function (not during upgrades).
if [ "$1" = "remove" ] || [ "$1" = "0" ] || command -v pacman >/dev/null 2>&1; then
  systemctl --global disable dockstar.service >/dev/null 2>&1 || true
  systemctl disable --now dockstar-wakeup.service >/dev/null 2>&1 || true
  systemctl disable --now dockstar-startup.service >/dev/null 2>&1 || true
fi
