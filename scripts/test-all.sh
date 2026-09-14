#!/usr/bin/env bash
# Runs every check CI runs: installs dependencies, then go vet, Go tests, pgTAP
# against the local Postgres, and type-check + Vitest + build across the npm
# workspace (packages/ui, web-app, desktop-app/frontend).
#
#   ./scripts/test-all.sh
#
# The database suites need the Supabase stack running (`supabase start`). Go's
# integration tests skip themselves when Postgres is unreachable unless
# STOCKROOM_REQUIRE_DB=1 is set. This script sets it whenever the database is
# actually up, so a silent skip can never pass for a success.
set -uo pipefail

cd "$(dirname "$0")/.."
failed=()

# CI runs `npm ci` before any of this; a fresh clone here would otherwise fail
# every npm step with "vite: not found". Same script the start scripts use, so
# there is one definition of an installed tree.
if ! ./scripts/ensure-deps.sh; then
  echo
  echo "FAILED: dependencies could not be installed; nothing else was run."
  exit 1
fi

db_up() {
  local host=127.0.0.1 port=54322
  if command -v nc >/dev/null 2>&1; then
    nc -z -G 2 "$host" "$port" >/dev/null 2>&1
  else
    return 1
  fi
}

run() {
  local name="$1"; shift
  echo
  echo "=== $name ==="
  if "$@"; then
    echo "--- $name: PASS"
  else
    echo "--- $name: FAIL"
    failed+=("$name")
  fi
}

if db_up; then
  export STOCKROOM_REQUIRE_DB=1
else
  echo "warning: Postgres is not reachable on 127.0.0.1:54322."
  echo "         Run 'supabase start' to include the database-backed tests."
fi

run "go vet" go vet ./...
run "go" go test ./... -count=1

if db_up; then
  run "pgtap" supabase test db
else
  failed+=("pgtap (skipped: database down)")
fi

# From the repo root, through the workspace. A --prefix install/run gives that
# app its own copy of Svelte and Vite, which breaks reactivity silently
# (docs/design/design-system.md 2.2).
run "svelte-check (packages/ui + both hosts)" npm run check
run "vitest (packages/ui + both hosts)" npm test
run "build (both hosts)" npm run build

echo
if [ ${#failed[@]} -eq 0 ]; then
  echo "All suites passed."
else
  printf 'FAILED: %s\n' "${failed[@]}"
  exit 1
fi
