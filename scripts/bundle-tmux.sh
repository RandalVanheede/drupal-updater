#!/bin/sh
# Copies tmux and its non-system dylibs into <app>/Contents/Resources/tmux-bundle and re-signs the app.
# Usage: scripts/bundle-tmux.sh path/to/drupal-updater.app   (needs Homebrew tmux)
set -eu
APP=${1:?usage: bundle-tmux.sh <app>}
SRC=$(command -v tmux || echo /opt/homebrew/bin/tmux)
DEST="$APP/Contents/Resources/tmux-bundle"
rm -rf "$DEST" && mkdir -p "$DEST/lib"
cp "$SRC" "$DEST/tmux" && chmod u+w "$DEST/tmux"

# non-system dependencies of a binary, one path per line
deps() { otool -L "$1" | tail -n +2 | awk '{print $1}' | grep -v -e '^/usr/lib/' -e '^/System/' -e '^@' || true; }

fix() { # fix <file> <relative prefix from file to lib dir>
  for d in $(deps "$1"); do
    name=$(basename "$d")
    if [ ! -f "$DEST/lib/$name" ]; then
      cp "$d" "$DEST/lib/$name" && chmod u+w "$DEST/lib/$name"
      install_name_tool -id "@loader_path/$name" "$DEST/lib/$name"
      fix "$DEST/lib/$name" "@loader_path"
    fi
    install_name_tool -change "$d" "$2/$name" "$1"
  done
}
fix "$DEST/tmux" "@executable_path/lib"

for f in "$DEST"/lib/* "$DEST/tmux"; do codesign --force -s - "$f" >/dev/null 2>&1; done
codesign --force -s - "$APP" >/dev/null 2>&1
echo "bundled tmux: $(otool -L "$DEST/tmux" | tail -n +2 | awk '{print $1}' | tr '\n' ' ')"
