#!/bin/sh
set -eu

bin_dir=${XDG_BIN_HOME:-"$HOME/.local/bin"}
data_dir=${XDG_DATA_HOME:-"$HOME/.local/share"}
config_dir=${XDG_CONFIG_HOME:-"$HOME/.config"}

systemctl --user disable --now dockstar.service 2>/dev/null || true
rm -f "$config_dir/systemd/user/dockstar.service"
rm -f "$config_dir/autostart/io.github.redundercover.Dockstar.Tray.desktop"
rm -f "$data_dir/applications/io.github.redundercover.Dockstar.desktop"
rm -f "$data_dir/dbus-1/services/io.github.redundercover.Dockstar.service"
rm -f "$data_dir/icons/hicolor/scalable/apps/io.github.redundercover.Dockstar.svg"
rm -f "$data_dir/man/man1/dockstar.1"
rm -f "$data_dir/bash-completion/completions/dockstar"
rm -f "$data_dir/zsh/site-functions/_dockstar"
rm -f "$config_dir/fish/completions/dockstar.fish"
rm -f "$bin_dir/dockstar" "$bin_dir/dockstar-ui"
systemctl --user daemon-reload
printf 'Dockstar binaries and integration files removed. Configuration was kept.\n'
