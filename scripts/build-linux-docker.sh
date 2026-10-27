#!/bin/sh
# Builds the Linux tarballs in Docker, so it works from macOS and for other CPUs (emulated, slower).
# Usage: scripts/build-linux-docker.sh [amd64] [arm64]   (default: both)
# Output: desktop/build/bin/drupal-updater-linux-<x86_64|aarch64>.tar.gz
set -eu
cd "$(dirname "$0")/.."
OUT="$PWD/desktop/build/bin"
mkdir -p "$OUT"
for arch in ${@:-amd64 arm64}; do
	echo "== linux/$arch"
	docker run --rm --platform "linux/$arch" -v "$PWD":/src:ro -v "$OUT":/out golang:1.25-trixie sh -euc '
		apt-get update -qq >/dev/null
		apt-get install -y -qq libgtk-3-dev libwebkit2gtk-4.1-dev nodejs npm pkg-config >/dev/null 2>&1
		go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0 >/dev/null 2>&1
		mkdir /work && cd /src
		# host node_modules hold native binaries for the wrong OS/CPU; the build reinstalls them
		tar --exclude=./desktop/frontend/node_modules --exclude=./.omc --exclude=./bin --exclude=./desktop/build/bin -cf - . | tar -C /work -xf -
		cd /work && WAILS=/go/bin/wails sh scripts/build-linux.sh >/dev/null
		cp desktop/build/bin/*.tar.gz /out/'
done
ls -1 "$OUT"/drupal-updater-linux-*.tar.gz
