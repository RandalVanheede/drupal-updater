#!/bin/sh
# Builds the desktop app, bundles tmux into it, and packs desktop/build/bin/Drupal-Updater.dmg
set -eu
cd "$(dirname "$0")/.."
(cd desktop && "${WAILS:-$HOME/go/bin/wails}" build -clean)
BIN=desktop/build/bin
scripts/bundle-tmux.sh "$BIN/drupal-updater.app" 2>/dev/null
rm -rf "$BIN/dmg-stage" "$BIN/Drupal-Updater.dmg"
mkdir "$BIN/dmg-stage"
cp -R "$BIN/drupal-updater.app" "$BIN/dmg-stage/"
ln -s /Applications "$BIN/dmg-stage/Applications"
hdiutil create -volname "Drupal Updater" -srcfolder "$BIN/dmg-stage" -ov -format UDZO "$BIN/Drupal-Updater.dmg" >/dev/null 2>&1
rm -rf "$BIN/dmg-stage"
hdiutil verify "$BIN/Drupal-Updater.dmg" 2>&1 | tail -1
