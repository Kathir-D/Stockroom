#!/usr/bin/env bash
# Bring every dependency up to date: the npm workspace and the Go modules.
#
#   ./scripts/ensure-deps.sh          # npm install: honours package.json
#   ./scripts/ensure-deps.sh --ci     # npm ci: exact lockfile, wipes node_modules
#
# One definition of "install the dependencies", called by start-mac.sh and
# test-all.sh, so the two can't drift about what a working tree looks like.
# scripts/start-windows.ps1 carries a PowerShell copy of the same steps.
#
# Exits non-zero if anything fails. Nothing downstream works without these, and
# a half-installed tree fails later with a much worse error message.
set -uo pipefail

cd "$(dirname "$0")/.."

mode="install"
if [ "${1:-}" = "--ci" ]; then
  mode="ci"
fi

echo "== Dependencies =="

# A nested node_modules under a workspace shadows the hoisted one, and the
# failure is silent: that app gets its own copy of Svelte and Vite, components
# render, and their state stops updating (docs/design/design-system.md §2.2).
# This is not hypothetical — it is how desktop-app/frontend ended up pinned to
# Vite 7 while the root was on Vite 8. Check rather than trust.
#
# "Shadowing" means an installed *package*, so only non-dot entries count.
# Vite writes its dep-optimisation cache to <app>/node_modules/.vite and
# .vite-temp, and npm puts workspace script binaries in .bin; all three are
# normal, and deleting them on every run would throw away the cache and
# reinstall the world each time.
nested_found=0
for nested in desktop-app/frontend/node_modules web-app/node_modules packages/ui/node_modules; do
  [ -d "$nested" ] || continue
  packages=$(find "$nested" -mindepth 1 -maxdepth 1 ! -name '.*' -print -quit 2>/dev/null)
  if [ -n "$packages" ]; then
    echo "  Removing a nested install that would shadow the workspace: $nested"
    rm -rf "$nested"
    nested_found=1
  fi
done
# npm hoists during install, so a tree built alongside a nested copy can be
# missing packages the nested one was satisfying. Start clean when we find one.
if [ "$nested_found" = "1" ] && [ "$mode" = "install" ]; then
  echo "  Reinstalling from the lockfile, because removing a nested copy can leave gaps."
  mode="ci"
fi

if [ "$mode" = "ci" ] && [ ! -f package-lock.json ]; then
  echo "  No package-lock.json; falling back to npm install."
  mode="install"
fi

if [ "$mode" = "ci" ]; then
  echo "  npm ci (exact lockfile) across packages/ui, web-app, desktop-app/frontend..."
  npm ci || { echo "  [ERROR] npm ci failed."; exit 1; }
else
  # `npm install` is a no-op in a few hundred ms when the tree already matches,
  # and updates it when package.json moved, so it is safe to run every start.
  echo "  npm install across packages/ui, web-app, desktop-app/frontend..."
  npm install || { echo "  [ERROR] npm install failed."; exit 1; }
fi

# Go modules are fetched on demand by `go run` and `go test`, but doing it here
# means a missing module fails now, with a readable message, rather than three
# lines into the server's startup log.
echo "  Go modules..."
go mod download || { echo "  [ERROR] go mod download failed. Check your network or GOPROXY."; exit 1; }

echo "  [OK] Dependencies are up to date"
