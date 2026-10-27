#!/bin/sh
# Builds the TUI and desktop app on Linux and packs desktop/build/bin/drupal-updater-linux-<arch>.tar.gz
# Needs: Go, npm, wails, libgtk-3-dev and libwebkit2gtk-4.1-dev (or 4.0). Runtime needs tmux and claude on PATH.
set -eu
cd "$(dirname "$0")/.."
BIN=desktop/build/bin
PKG="drupal-updater-linux-$(uname -m)"
TAGS=""
pkg-config --exists webkit2gtk-4.1 2>/dev/null && TAGS="-tags webkit2_41" # Ubuntu 24.04+/Debian 13 ship only 4.1
(cd desktop && "${WAILS:-$HOME/go/bin/wails}" build -clean $TAGS)
rm -rf "$BIN/$PKG"
mkdir -p "$BIN/$PKG"
mv "$BIN/drupal-updater-desktop" "$BIN/$PKG/"
go build -o "$BIN/$PKG/drupal-updater" .
cp desktop/build/appicon.png "$BIN/$PKG/drupal-updater.png"
cat > "$BIN/$PKG/install.sh" <<'INSTALL'
#!/bin/sh
# Installs into ~/.local and adds a menu entry. Runtime needs tmux and claude on PATH.
set -eu
cd "$(dirname "$0")"
D="$HOME/.local/share/drupal-updater"
install -Dm755 drupal-updater drupal-updater-desktop -t "$HOME/.local/bin"
install -Dm644 drupal-updater.png -t "$D"
mkdir -p "$HOME/.local/share/applications"
cat > "$HOME/.local/share/applications/drupal-updater.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=Drupal Updater
Exec=$HOME/.local/bin/drupal-updater-desktop
Icon=$D/drupal-updater.png
Categories=Development;
DESKTOP
echo "Installed to ~/.local/bin. Make sure tmux and claude are installed."
INSTALL
chmod +x "$BIN/$PKG/install.sh"
tar -C "$BIN" -czf "$BIN/$PKG.tar.gz" "$PKG"
rm -rf "$BIN/$PKG"
echo "$BIN/$PKG.tar.gz"
