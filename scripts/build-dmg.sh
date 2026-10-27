#!/bin/sh
# Builds the desktop app, bundles tmux into it, and packs desktop/build/bin/Drupal-Updater.dmg
# Only the .dmg is kept: a stray .app in the build folder shows up in Spotlight next to /Applications.
set -eu
cd "$(dirname "$0")/.."
BIN=desktop/build/bin
trap 'rm -rf "$BIN/drupal-updater.app" "$BIN/dmg-stage"' EXIT
(cd desktop && "${WAILS:-$HOME/go/bin/wails}" build -clean)
scripts/bundle-tmux.sh "$BIN/drupal-updater.app" 2>/dev/null
rm -rf "$BIN/dmg-stage" "$BIN/Drupal-Updater.dmg"
mkdir "$BIN/dmg-stage"
mv "$BIN/drupal-updater.app" "$BIN/dmg-stage/"
ln -s /Applications "$BIN/dmg-stage/Applications"
hdiutil create -volname "Drupal Updater" -srcfolder "$BIN/dmg-stage" -ov -format UDZO "$BIN/Drupal-Updater.dmg" >/dev/null 2>&1
hdiutil verify "$BIN/Drupal-Updater.dmg" 2>&1 | tail -1
