#!/usr/bin/env bash
# One-step start for Stockroom on macOS: verifies dependencies, starts Docker +
# the local Supabase stack, installs frontend packages, then launches the
# desktop app and web app. Ctrl+C shuts everything down cleanly.
set -uo pipefail
set -m # each background job gets its own process group, so we can kill it and its children together

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

PIDS=()
CLEANED_UP=0

cleanup() {
  [ "$CLEANED_UP" -eq 1 ] && return
  CLEANED_UP=1
  echo ""
  echo "== Shutting down Stockroom =="
  for pid in "${PIDS[@]:-}"; do
    [ -z "$pid" ] && continue
    # negative pid signals the whole process group (the subshell plus
    # whatever it spawned, e.g. wails/npm/vite/node), not just the subshell
    kill -TERM -- "-$pid" 2>/dev/null
  done
  sleep 2
  for pid in "${PIDS[@]:-}"; do
    [ -z "$pid" ] && continue
    kill -KILL -- "-$pid" 2>/dev/null
  done
  echo "Stopping Supabase (data is preserved)..."
  supabase stop >/dev/null 2>&1
  echo "Done. All processes stopped."
}
trap cleanup EXIT INT TERM HUP

echo "== Checking dependencies =="
fail=0

check_cmd() {
  local name="$1" cmd="$2" url="$3"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "  [MISSING] $name — install from: $url"
    fail=1
  else
    echo "  [OK] $name"
  fi
}

check_cmd "Go" go "https://go.dev/dl/"
check_cmd "Node.js/npm" npm "https://nodejs.org/en/download"
check_cmd "Docker" docker "https://www.docker.com/products/docker-desktop/"
check_cmd "Supabase CLI" supabase "https://supabase.com/docs/guides/cli/getting-started"

if ! command -v wails >/dev/null 2>&1; then
  if command -v go >/dev/null 2>&1; then
    echo "  [MISSING] Wails CLI — installing via 'go install'..."
    go install github.com/wailsapp/wails/v2/cmd/wails@latest
    export PATH="$PATH:$(go env GOPATH)/bin"
    if command -v wails >/dev/null 2>&1; then
      echo "  [OK] Wails CLI (installed)"
    else
      echo "  [MISSING] Wails CLI install failed. See https://wails.io/docs/gettingstarted/installation"
      fail=1
    fi
  else
    echo "  [MISSING] Wails CLI — cannot auto-install without Go. See https://go.dev/dl/"
    fail=1
  fi
else
  echo "  [OK] Wails CLI"
fi

if [ "$fail" -eq 1 ]; then
  echo ""
  echo "Please install the missing dependencies above, then re-run this script."
  exit 1
fi

echo ""
echo "== Starting Docker (required for Supabase) =="
if ! docker info >/dev/null 2>&1; then
  echo "Docker isn't running — starting Docker Desktop..."
  open -a Docker
  echo "Waiting for Docker to become ready..."
  ready=0
  for i in $(seq 1 60); do
    if docker info >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 2
  done
  if [ "$ready" -eq 0 ]; then
    echo "  [ERROR] Docker did not start in time. Please start Docker Desktop manually and re-run."
    exit 1
  fi
fi
echo "  [OK] Docker is ready"

echo ""
echo "== Starting local Supabase stack =="
supabase start

echo ""
echo "== Installing frontend dependencies (if needed) =="
(cd desktop-app/frontend && npm install)
(cd web-app && npm install)

echo ""
echo "== Launching Stockroom =="
echo "Starting desktop app (Wails)..."
(cd desktop-app && wails dev) &
PIDS+=($!)

echo "Starting web app (localhost)..."
(cd web-app && npm run dev) &
PIDS+=($!)

echo ""
echo "Stockroom is running."
echo "  Desktop app: a native window should open automatically"
echo "  Web app:     see the URL printed above (usually http://localhost:5173)"
echo "  Studio:      http://127.0.0.1:54323"
echo ""
echo "Press Ctrl+C to stop everything and close all processes."

wait
