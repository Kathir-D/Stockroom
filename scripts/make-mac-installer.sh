#!/usr/bin/env bash
# Builds the macOS graphical installer: "Install Stockroom.app" in a zip.
#
#   scripts/make-mac-installer.sh ARM64_BINARY AMD64_BINARY VERSION OUT.zip
#
# The app is a folder, so this runs on the Linux release runner as well as on
# a Mac: no hdiutil, no pkgbuild. It holds both darwin builds of stockroom and
# packaging/mac/installer/install-stockroom, which starts the right one as
# `setup --gui --install-packages`. release.yml runs this after GoReleaser and
# adds the zip to checksums.txt.
#
# The app is not signed (ROADMAP, Not planned), so the first open needs
# Control-click, Open, or Open Anyway in System Settings, Privacy & Security.
# docs/INSTALL.md says so.
set -euo pipefail

[ $# -eq 4 ] || { echo "usage: $0 ARM64_BINARY AMD64_BINARY VERSION OUT.zip" >&2; exit 2; }
arm64=$1 amd64=$2 version=${3#v} out=$4
here=$(cd "$(dirname "$0")/.." && pwd)
src="$here/packaging/mac/installer"

for f in "$arm64" "$amd64"; do
  [ -f "$f" ] || { echo "$0: $f is not a file" >&2; exit 1; }
done
command -v zip >/dev/null || { echo "$0: zip is missing" >&2; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
app="$work/Install Stockroom.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"

sed "s/__VERSION__/$version/g" "$src/Info.plist" > "$app/Contents/Info.plist"
install -m 755 "$src/install-stockroom" "$app/Contents/MacOS/install-stockroom"
install -m 755 "$arm64" "$app/Contents/Resources/stockroom-arm64"
install -m 755 "$amd64" "$app/Contents/Resources/stockroom-amd64"

case "$out" in /*) ;; *) out="$PWD/$out" ;; esac
rm -f "$out"
# -y keeps symlinks as links and -X drops the extra fields that would make
# two builds of the same commit differ.
(cd "$work" && zip -q -r -y -X "$out" "Install Stockroom.app")
echo "wrote $out"
