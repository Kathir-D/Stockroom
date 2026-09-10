#!/usr/bin/env bash
# Runs every check CI runs: go vet, Go tests under -race, pgTAP against the
# local Postgres, and type-check + Vitest for both frontends.
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
run "go (-race)" go test ./... -race

if db_up; then
  run "pgtap" supabase test db
else
  failed+=("pgtap (skipped: database down)")
fi

run "desktop-app: svelte-check" npm --prefix desktop-app/frontend run check
run "desktop-app: vitest" npm --prefix desktop-app/frontend test
run "web-app: svelte-check" npm --prefix web-app run check
run "web-app: vitest" npm --prefix web-app test
run "web-app: build" npm --prefix web-app run build

echo
if [ ${#failed[@]} -eq 0 ]; then
  echo "All suites passed."
else
  printf 'FAILED: %s\n' "${failed[@]}"
  exit 1
fi
