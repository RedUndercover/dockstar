#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
bin_dir=${XDG_BIN_HOME:-"$HOME/.local/bin"}
data_dir=${XDG_DATA_HOME:-"$HOME/.local/share"}
config_dir=${XDG_CONFIG_HOME:-"$HOME/.config"}

install -Dm755 "$project_dir/build/dockstar" "$bin_dir/dockstar"
install -Dm755 "$project_dir/build/dockstar-ui" "$bin_dir/dockstar-ui"
install -Dm644 "$project_dir/packaging/systemd/dockstar.service" "$config_dir/systemd/user/dockstar.service"
sed -i "s|ExecStart=/usr/bin/dockstar daemon|ExecStart=$bin_dir/dockstar daemon|" "$config_dir/systemd/user/dockstar.service"
install -Dm644 "$project_dir/packaging/dbus/io.github.redundercover.Dockstar.service" "$data_dir/dbus-1/services/io.github.redundercover.Dockstar.service"
sed -i "s|Exec=/usr/bin/dockstar daemon|Exec=$bin_dir/dockstar daemon|" "$data_dir/dbus-1/services/io.github.redundercover.Dockstar.service"
install -Dm644 "$project_dir/packaging/desktop/io.github.redundercover.Dockstar.desktop" "$data_dir/applications/io.github.redundercover.Dockstar.desktop"
install -Dm644 "$project_dir/packaging/desktop/io.github.redundercover.Dockstar.Tray.desktop" "$config_dir/autostart/io.github.redundercover.Dockstar.Tray.desktop"
sed -i "s|dockstar-ui|$bin_dir/dockstar-ui|" "$data_dir/applications/io.github.redundercover.Dockstar.desktop" "$config_dir/autostart/io.github.redundercover.Dockstar.Tray.desktop"
install -Dm644 "$project_dir/packaging/icons/io.github.redundercover.Dockstar.svg" "$data_dir/icons/hicolor/scalable/apps/io.github.redundercover.Dockstar.svg"
install -Dm644 "$project_dir/man/dockstar.1" "$data_dir/man/man1/dockstar.1"
install -Dm644 "$project_dir/completions/dockstar.bash" "$data_dir/bash-completion/completions/dockstar"
install -Dm644 "$project_dir/completions/_dockstar" "$data_dir/zsh/site-functions/_dockstar"
install -Dm644 "$project_dir/completions/dockstar.fish" "$config_dir/fish/completions/dockstar.fish"

systemctl --user daemon-reload
systemctl --user enable --now dockstar.service
printf 'Dockstar installed for %s. Hardware wake setup remains disabled.\n' "$USER"
