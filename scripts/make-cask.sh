#!/usr/bin/env bash
# Fills packaging/mac/stockroom.rb with the version and checksums of a release,
# writing the cask Homebrew actually reads.
#
#   scripts/make-cask.sh VERSION CHECKSUMS.txt OUTPUT.rb
#
# The checked-in cask is a template: `__VERSION__` and the `__SHA256_*__`
# placeholders. This is what turns it into a real one, on every release.
#
# The checksums come from the release's own checksums.txt rather than from
# hashing the downloads, so the cask and the published file cannot disagree. A
# missing entry is an error rather than an empty substitution: an empty sha256
# would produce a cask that Homebrew refuses to install, or worse, one that
# installs without checking anything.
#
# The template is also checked for leftovers afterwards, so a placeholder that
# gets renamed here but not in the template fails the build instead of shipping
# a literal `__SHA256_DARWIN_ARM64__` into the tap.
set -euo pipefail

version=$1 checksums=$2 out=$3
template=packaging/mac/stockroom.rb

[ -f "$template" ] || { echo "make-cask: no template at $template" >&2; exit 1; }
[ -f "$checksums" ] || { echo "make-cask: no checksums at $checksums" >&2; exit 1; }

# Made here rather than relied on from GoReleaser, which no longer writes a
# cask and so no longer creates the directory. Creating the parent of the
# output is the script's business anyway: the caller names a file, not a tree
# it has to have built first.
mkdir -p "$(dirname "$out")"

# sha_of FILE prints the checksum of FILE, or fails. GoReleaser writes
# "<sha>  <name>", two spaces, which is what `shasum -c` expects.
#
# `return 1` and not `exit 1`: the calls below sit inside `$(...)`, which runs
# in a subshell, and `exit` there ends only the subshell. The script then
# carried on and wrote a cask with an empty sha256, so a missing archive
# shipped a broken cask and still reported success. Returning propagates the
# failure to the `|| exit 1` in the caller, which is the parent shell.
sha_of() {
  local name=$1 line
  line=$(grep -F "  $name" "$checksums" || true)
  case $line in
    "") echo "make-cask: $name is not in $checksums" >&2; return 1 ;;
    *) printf '%s\n' "${line%% *}" ;;
  esac
}

# Resolved before the sed, so a missing archive stops the script before it has
# written anything.
sha_darwin_arm64=$(sha_of stockroom_darwin_arm64.tar.gz) || exit 1
sha_darwin_amd64=$(sha_of stockroom_darwin_amd64.tar.gz) || exit 1
sha_linux_arm64=$(sha_of stockroom_linux_arm64.tar.gz) || exit 1
sha_linux_amd64=$(sha_of stockroom_linux_amd64.tar.gz) || exit 1

# The leading v is GoReleaser's; the cask's version is the bare number, which
# is what the download URL interpolates.
version=${version#v}

sed \
  -e "s|__VERSION__|$version|g" \
  -e "s|__SHA256_DARWIN_ARM64__|$sha_darwin_arm64|g" \
  -e "s|__SHA256_DARWIN_AMD64__|$sha_darwin_amd64|g" \
  -e "s|__SHA256_LINUX_ARM64__|$sha_linux_arm64|g" \
  -e "s|__SHA256_LINUX_AMD64__|$sha_linux_amd64|g" \
  "$template" > "$out"

if grep -q '__[A-Z0-9_]*__' "$out"; then
  echo "make-cask: unsubstituted placeholders left in $out:" >&2
  grep -n '__[A-Z0-9_]*__' "$out" >&2
  exit 1
fi

echo "wrote $out for v$version"
