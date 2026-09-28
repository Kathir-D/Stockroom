#!/usr/bin/env bash
# Installs or upgrades Stockroom on Debian or Ubuntu, including Ubuntu under
# WSL 2:
#
#   curl -fsSL https://raw.githubusercontent.com/Kathir-D/Stockroom/main/scripts/get.sh | sudo bash
#
# It downloads the .deb from the latest GitHub release, checks it against the
# release's checksums.txt, installs it with apt (which pulls in PostgreSQL and
# rclone), then runs `stockroom setup`. Running it again is the upgrade.
#
# Set STOCKROOM_VERSION=v1.2.3 to install a particular release. Arguments
# after `bash -s --` go to `stockroom setup`, for example:
#
#   curl -fsSL …/get.sh | sudo bash -s -- --service-user stockroom
set -euo pipefail

repo="Kathir-D/Stockroom"

say() { printf '==> %s\n' "$*"; }
die() { printf 'stockroom: %s\n' "$*" >&2; exit 1; }

# 1. Root. apt and setup both need it. When the script arrived through a pipe
#    there's no file to re-run, so ask for sudo on the pipe instead.
if [ "$(id -u)" -ne 0 ]; then
  if [ -f "${BASH_SOURCE[0]:-}" ]; then
    exec sudo -E bash "${BASH_SOURCE[0]}" "$@"
  fi
  die "run this as root: curl -fsSL https://raw.githubusercontent.com/$repo/main/scripts/get.sh | sudo bash"
fi

# 2. The distribution. Only Debian and Ubuntu have the .deb.
[ -r /etc/os-release ] || die "can't read /etc/os-release to tell which Linux this is"
# shellcheck disable=SC1091
. /etc/os-release
case "${ID:-} ${ID_LIKE:-}" in
  *debian*|*ubuntu*) ;;
  *)
    cat >&2 <<EOF
stockroom: this installer supports Debian and Ubuntu, and this is ${PRETTY_NAME:-an unknown system}.
On macOS: brew install kathir-d/stockroom/stockroom && stockroom setup
Elsewhere, see https://github.com/$repo/blob/main/docs/INSTALL.md
EOF
    exit 1
    ;;
esac

# 3. The architecture, named the way the release names its files.
arch=$(dpkg --print-architecture)
case "$arch" in
  amd64|arm64) ;;
  *) die "there is no Stockroom package for $arch (only amd64 and arm64)" ;;
esac

# 4. Download the package and the checksums, from the latest release or the
#    one STOCKROOM_VERSION names.
# STOCKROOM_BASE_URL points at another copy of the release files, for tests.
if [ -n "${STOCKROOM_BASE_URL:-}" ]; then
  base="$STOCKROOM_BASE_URL"
elif [ -n "${STOCKROOM_VERSION:-}" ]; then
  base="https://github.com/$repo/releases/download/$STOCKROOM_VERSION"
else
  base="https://github.com/$repo/releases/latest/download"
fi
deb="stockroom_${arch}.deb"
command -v curl >/dev/null || die "curl is missing: apt-get install curl"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# apt reads the file as the _apt user, which can't enter mktemp's 700 folder.
chmod 755 "$tmp"
say "Downloading $deb from ${STOCKROOM_VERSION:-the latest release}"
curl -fsSL -o "$tmp/$deb" "$base/$deb"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"

# 5. Check the download against checksums.txt. A mismatch stops here.
say "Checking the checksum"
(cd "$tmp" && grep " ${deb}\$" checksums.txt | sha256sum -c --status) \
  || die "$deb doesn't match checksums.txt; nothing was installed"

# 6. Install. apt resolves the dependencies: PostgreSQL, its client, rclone.
say "Installing"
export DEBIAN_FRONTEND=noninteractive STOCKROOM_GET=1
apt-get update -q
apt-get install -y -q "$tmp/$deb"

# 7. Setup creates the database, writes the config and starts the service. On
#    an upgrade it finds everything in place and only repairs what's missing.
#    Its questions read from /dev/tty, so they work through the pipe.
say "Running stockroom setup"
stockroom setup "$@"
