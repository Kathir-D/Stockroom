#!/usr/bin/env bash
# Stockroom's one script. Everything you can do to a working copy from a
# terminal is a subcommand here, so there is one place to look and one place to
# fix. Previously this was start-mac.sh + ensure-deps.sh + test-all.sh, which
# drifted: the start script grew a health gate the test script never got, and
# "the dependencies are installed" meant something slightly different in each.
#
#   ./scripts/dev.sh              bring the whole dev environment up (same as `up`)
#   ./scripts/dev.sh up           ... with flags: --no-desktop --no-web --no-open --ci
#   ./scripts/dev.sh deps [--ci]  npm workspace + Go modules + git hooks, nothing else
#   ./scripts/dev.sh test         the full suite (what .githooks/pre-commit and CI run)
#   ./scripts/dev.sh stop         stop Supabase and anything left listening
#   ./scripts/dev.sh status       what is running right now, and what is not
#
# `up` is idempotent: run it again after a crash and it reuses whatever is
# already healthy instead of starting a second copy that cannot bind its port.
#
# scripts/dev.ps1 is the Windows counterpart and carries the same
# subcommands; bash is not a given on the closet PC, so the two cannot be one
# file. They must stay in step.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)" || exit 1
cd "$REPO_ROOT" || { echo "cannot cd to the repository root" >&2; exit 1; }

# ---------------------------------------------------------------------------
# The three numbers the browser has baked in
#
# localOrigins in server/router.go allows :5173 and nothing else, and
# DEFAULT_BASE_URL in packages/ui/src/lib/api/client.ts points at :8080 whatever
# SERVER_ADDR says. Drift on either side reaches the user as "cannot reach the
# Stockroom server", because a blocked CORS preflight and a dead server throw
# the same fetch error. Pinning them here is what keeps that error honest.
# ---------------------------------------------------------------------------
WEB_PORT=5173
STUDIO_PORT=54323
PG_PORT=54322
DEFAULT_SERVER_ADDR="127.0.0.1:8080"

LOG_DIR="$REPO_ROOT/.run-logs"

say()  { echo "$@"; }
ok()   { echo "  [OK] $*"; }
warn() { echo "  [WARN] $*"; }
err()  { echo "  [ERROR] $*" >&2; }

# The pid listening on a TCP port, empty if nobody is.
port_pid() { lsof -nP -iTCP:"$1" -sTCP:LISTEN -t 2>/dev/null | head -1; }

# A 2xx/3xx from a URL. Used for readiness, so a connection refused and a 500
# are both "not ready" rather than an error to report.
http_ok() { curl -fsS -m "${2:-2}" "$1" >/dev/null 2>&1; }

# SERVER_ADDR as .env has it, defaulting the way the Go server does.
server_addr() {
  local addr
  addr="$(sed -n 's/^[[:space:]]*SERVER_ADDR[[:space:]]*=[[:space:]]*//p' .env 2>/dev/null | tail -1 | tr -d '"'\''' | tr -d '\r')"
  echo "${addr:-$DEFAULT_SERVER_ADDR}"
}

# ===========================================================================
# deps -- one definition of "the dependencies are up to date"
# ===========================================================================
cmd_deps() {
  local mode="install"
  [ "${1:-}" = "--ci" ] && mode="ci"

  say "== Dependencies =="

  # Point git at the tracked hooks directory, so .githooks/pre-commit runs the
  # suite before a commit lands (CI.md, "Pre-commit"). Set here rather than
  # documented as a manual step because a hook that depends on every
  # contributor remembering one git config line is a hook that is off in most
  # clones. Idempotent, and harmless when git is absent (a tarball download).
  #
  # A failure here is fatal rather than a warning. Every other step in this
  # function is recoverable by rerunning it; this one silently decides whether
  # the suite guards a commit at all, and a script that prints a warning nobody
  # reads and then exits 0 is how a clone commits untested code for a week.
  if command -v git >/dev/null 2>&1 && [ -d .githooks ]; then
    if [ "$(git config --get core.hooksPath 2>/dev/null)" != ".githooks" ]; then
      if git config core.hooksPath .githooks; then
        ok "git hooks -> .githooks"
      else
        err "could not set core.hooksPath; the pre-commit hook will not run"
        return 1
      fi
    fi
    chmod +x .githooks/* 2>/dev/null || true
  fi

  # A nested node_modules under a workspace shadows the hoisted one, and the
  # failure is silent: that app gets its own copy of Svelte and Vite,
  # components render, and their state stops updating
  # (docs/design/design-system.md §2.2). Not hypothetical -- it is how
  # desktop-app/frontend ended up pinned to Vite 7 while the root was on 8.
  #
  # "Shadowing" means an installed *package*, so only non-dot entries count.
  # Vite writes its dep-optimisation cache to <app>/node_modules/.vite and
  # .vite-temp, and npm puts workspace script binaries in .bin; all three are
  # normal, and deleting them every run would reinstall the world each time.
  local nested_found=0 nested packages
  for nested in desktop-app/frontend/node_modules web-app/node_modules packages/ui/node_modules; do
    [ -d "$nested" ] || continue
    packages=$(find "$nested" -mindepth 1 -maxdepth 1 ! -name '.*' -print -quit 2>/dev/null)
    if [ -n "$packages" ]; then
      say "  Removing a nested install that would shadow the workspace: $nested"
      rm -rf "$nested"
      nested_found=1
    fi
  done
  # npm hoists during install, so a tree built alongside a nested copy can be
  # missing packages the nested one was satisfying. Start clean when we find one.
  if [ "$nested_found" = "1" ] && [ "$mode" = "install" ]; then
    say "  Reinstalling from the lockfile, because removing a nested copy can leave gaps."
    mode="ci"
  fi

  if [ "$mode" = "ci" ] && [ ! -f package-lock.json ]; then
    say "  No package-lock.json; falling back to npm install."
    mode="install"
  fi

  if [ "$mode" = "ci" ]; then
    say "  npm ci (exact lockfile) across packages/ui, web-app, desktop-app/frontend..."
    npm ci || { err "npm ci failed."; return 1; }
  else
    # `npm install` is a no-op in a few hundred ms when the tree already
    # matches, and updates it when package.json moved, so it is safe to run
    # on every start.
    say "  npm install across packages/ui, web-app, desktop-app/frontend..."
    npm install || { err "npm install failed."; return 1; }
  fi

  # Go modules are fetched on demand by `go run` and `go test`, but doing it
  # here means a missing module fails now, with a readable message, rather than
  # three lines into the server's startup log.
  say "  Go modules..."
  go mod download || { err "go mod download failed. Check your network or GOPROXY."; return 1; }

  ok "Dependencies are up to date"
}

# ===========================================================================
# test -- every check CI runs
# ===========================================================================
# A TCP connect to Postgres, using bash's own /dev/tcp redirection so the probe
# needs no external binary at all.
#
# The obvious alternative, `nc -z -G 2`, carries a connect-timeout flag that only
# BSD netcat has: on GNU netcat, on busybox, or on a machine without `nc` the
# probe fails outright and a perfectly healthy database reads as down -- which
# `test` then reports as a FAILED pgTAP run rather than a passing one.
db_up() {
  (exec 3<>/dev/tcp/127.0.0.1/"$PG_PORT") >/dev/null 2>&1
}

cmd_test() {
  local failed=() with_deps=1 arg
  for arg in "$@"; do
    case "$arg" in
      --no-deps) with_deps=0 ;;
      *) err "unknown flag for test: $arg"; return 2 ;;
    esac
  done

  # CI runs `npm ci` before any of this; a fresh clone here would otherwise
  # fail every npm step with "vite: not found".
  #
  # --no-deps exists for .githooks/pre-commit, and for one reason: `npm install`
  # may rewrite package-lock.json, which is a tracked file. A hook that edits
  # the working tree in the middle of a commit is its own bug -- the rewrite is
  # not staged, so the commit records code against a lockfile that has silently
  # moved, and the next `git status` blames the author. Skipping it is safe
  # there because a stale tree fails the suite loudly and the commit is blocked
  # either way; the message below says which command fixes it.
  if [ "$with_deps" -eq 1 ]; then
    if ! cmd_deps; then
      echo
      echo "FAILED: dependencies could not be installed; nothing else was run."
      return 1
    fi
  fi

  run_suite() {
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

  # Go's integration tests skip themselves when Postgres is unreachable. Set
  # STOCKROOM_REQUIRE_DB whenever the database is actually up, so a silent skip
  # can never pass for a success.
  if db_up; then
    export STOCKROOM_REQUIRE_DB=1
  else
    echo "warning: Postgres is not reachable on 127.0.0.1:$PG_PORT."
    echo "         Run './scripts/dev.sh up' to include the database-backed tests."
  fi

  run_suite "go vet" go vet ./...
  run_suite "go" go test ./... -count=1

  if db_up; then
    run_suite "pgtap" supabase test db
  else
    failed+=("pgtap (skipped: database down)")
  fi

  # From the repo root, through the workspace. A --prefix install/run gives that
  # app its own copy of Svelte and Vite, which breaks reactivity silently
  # (docs/design/design-system.md §2.2).
  run_suite "svelte-check (packages/ui + both hosts)" npm run check
  run_suite "vitest (packages/ui + both hosts)" npm test
  run_suite "build (both hosts)" npm run build

  echo
  if [ ${#failed[@]} -eq 0 ]; then
    echo "All suites passed."
    return 0
  fi
  printf 'FAILED: %s\n' "${failed[@]}"
  return 1
}

# ===========================================================================
# up -- the whole environment
# ===========================================================================
PIDS=()
CLEANED_UP=0
# Whether *this* invocation started the Supabase stack, and so owns stopping it.
#
# The trap is armed before the first precondition is checked, so cleanup runs on
# every exit path including the ones that started nothing. Stopping Supabase
# unconditionally therefore meant a missing-tool error tore down a stack another
# terminal was using -- and on the reuse-an-existing-server path it pulled the
# database out from under a server it had deliberately left running, producing
# the "holds the port but does not answer /health" state cmd_up itself refuses
# to handle. Tear down exactly what we brought up, and nothing else.
STARTED_SUPABASE=0

cleanup() {
  [ "$CLEANED_UP" -eq 1 ] && return
  CLEANED_UP=1

  # Nothing of ours ever started: leave without a shutdown banner for a
  # shutdown that is not happening.
  if [ "${#PIDS[@]}" -eq 0 ] && [ "$STARTED_SUPABASE" -eq 0 ]; then
    return
  fi

  echo ""
  echo "== Shutting down Stockroom =="
  local pid
  for pid in "${PIDS[@]:-}"; do
    [ -z "$pid" ] && continue
    # A negative pid signals the whole process group (the subshell plus
    # whatever it spawned: wails, npm, vite, node), not just the subshell.
    kill -TERM -- "-$pid" 2>/dev/null
  done
  sleep 2
  for pid in "${PIDS[@]:-}"; do
    [ -z "$pid" ] && continue
    kill -KILL -- "-$pid" 2>/dev/null
  done

  if [ "$STARTED_SUPABASE" -eq 1 ]; then
    echo "Stopping Supabase (data is preserved)..."
    supabase stop >/dev/null 2>&1
  else
    echo "Leaving Supabase running: it was already up before this run."
  fi
  echo "Done. All processes stopped."
}

check_tools() {
  local fail=0
  say "== Checking dependencies =="

  local name cmd url
  while IFS='|' read -r name cmd url; do
    [ -z "$name" ] && continue
    if command -v "$cmd" >/dev/null 2>&1; then
      ok "$name"
    else
      say "  [MISSING] $name. Install from $url"
      fail=1
    fi
  done <<'TOOLS'
Go|go|https://go.dev/dl/
Node.js/npm|npm|https://nodejs.org/en/download
Docker|docker|https://www.docker.com/products/docker-desktop/
Supabase CLI|supabase|https://supabase.com/docs/guides/cli/getting-started
TOOLS

  # Wails is the one tool we can install ourselves, so we do rather than
  # bouncing the user to a docs page for a one-line `go install`.
  if command -v wails >/dev/null 2>&1; then
    ok "Wails CLI"
  elif command -v go >/dev/null 2>&1; then
    say "  [MISSING] Wails CLI. Installing via 'go install'..."
    go install github.com/wailsapp/wails/v2/cmd/wails@latest
    export PATH="$PATH:$(go env GOPATH)/bin"
    if command -v wails >/dev/null 2>&1; then
      ok "Wails CLI (installed)"
    else
      say "  [MISSING] Wails CLI install failed. See https://wails.io/docs/gettingstarted/installation"
      fail=1
    fi
  else
    say "  [MISSING] Wails CLI. Cannot auto-install without Go. See https://go.dev/dl/"
    fail=1
  fi

  if [ "$fail" -eq 1 ]; then
    echo ""
    echo "Please install the missing dependencies above, then re-run this script."
    return 1
  fi
}

start_docker() {
  say ""
  say "== Starting Docker (required for Supabase) =="
  if docker info >/dev/null 2>&1; then
    ok "Docker is ready"
    return 0
  fi
  if ! command -v open >/dev/null 2>&1; then
    err "Docker is not running and this is not macOS. Start it yourself and re-run."
    return 1
  fi
  say "Docker isn't running. Starting Docker Desktop..."
  open -a Docker
  say "Waiting for Docker to become ready..."
  local i
  for i in $(seq 1 60); do
    if docker info >/dev/null 2>&1; then
      ok "Docker is ready"
      return 0
    fi
    sleep 2
  done
  err "Docker did not start in time. Please start Docker Desktop manually and re-run."
  return 1
}

# Wait until a URL actually answers. Readiness only: the opening is separate,
# because every tab has to be handed to `open` in a single call to land in one
# window (see open_tabs below).
#
# Nothing is opened before its URL responds. A tab pointed at a port nothing is
# listening on yet shows a browser error page as the first thing the user sees,
# which reads exactly like a broken app.
wait_for_url() {
  local url="$1" label="$2" tries="${3:-60}" i
  for ((i = 0; i < tries; i++)); do
    if http_ok "$url" 2; then
      ok "$label ready: $url"
      return 0
    fi
    sleep 1
  done
  warn "$label never came up at $url; not opening a tab."
  return 1
}

# Open every URL as a tab in one browser window.
#
# One `open` invocation with several URLs gives one window with a tab each;
# calling `open` once per URL gives a window each, which is what this used to do
# and is a mess to clean up on a shared machine.
open_tabs() {
  [ "$#" -eq 0 ] && return 0
  if ! command -v open >/dev/null 2>&1; then
    warn "no 'open' command; visit these yourself: $*"
    return 0
  fi
  open "$@" >/dev/null 2>&1
  ok "Opened $# tab(s) in one window: $*"
}

cmd_up() {
  local want_desktop=1 want_web=1 want_open=1 deps_mode=""
  local arg
  for arg in "$@"; do
    case "$arg" in
      --no-desktop) want_desktop=0 ;;
      --no-web)     want_web=0 ;;
      --no-open)    want_open=0 ;;
      --ci)         deps_mode="--ci" ;;
      *) err "unknown flag for up: $arg"; return 2 ;;
    esac
  done

  # Each background job gets its own process group, so cleanup can kill it and
  # its children together.
  set -m
  trap cleanup EXIT INT TERM HUP

  check_tools || return 1
  start_docker || return 1

  say ""
  say "== Starting local Supabase stack =="
  # `supabase start` is idempotent, so it runs either way; the probe first is
  # only about *ownership*. If Postgres was already answering, somebody else
  # started this stack and Ctrl+C here must not take it away from them.
  if db_up; then
    ok "Supabase is already up; leaving it running when this script exits"
    if ! supabase start; then
      err "supabase start failed. The stack was already up, so this is likely a"
      err "partially-started stack; try './scripts/dev.sh stop' and re-run."
      return 1
    fi
  else
    # The flag goes up *before* the call, not after. `supabase start` creates
    # containers as it goes, so a Ctrl+C in the middle of it leaves a stack
    # this run brought up -- and cleanup only stops what it owns. Set it after
    # and that interrupt leaks the containers. Setting it early can at worst
    # make cleanup run `supabase stop` against a stack that never started,
    # which is a no-op.
    STARTED_SUPABASE=1
    if ! supabase start; then
      err "supabase start failed. Check that Docker is running and has room,"
      err "then re-run. Nothing below this point can work without Postgres."
      return 1
    fi
  fi

  say ""
  cmd_deps $deps_mode || {
    say "  Fix the error above and re-run; nothing below will work without these."
    return 1
  }

  if [ ! -f .env ]; then
    say ""
    say "== No .env found. Creating one from .env.example =="
    cp .env.example .env
    say "  Edit .env to set ADMIN_STUDENT_NUMBER / ADMIN_PASSWORD (see CLAUDE.md §9)."
  fi

  local addr health_url
  addr="$(server_addr)"
  health_url="http://${addr}/health"
  if [ "$addr" != "$DEFAULT_SERVER_ADDR" ]; then
    say ""
    warn ".env sets SERVER_ADDR=$addr, but the frontends are hardcoded to"
    say "         http://$DEFAULT_SERVER_ADDR (DEFAULT_BASE_URL). They will not connect."
  fi

  mkdir -p "$LOG_DIR"

  say ""
  say "== Launching Stockroom =="
  say "  Logs: $LOG_DIR/{server,desktop,web}.log"

  # An already-running server would make `go run ./server` fail to bind and exit
  # within a second, leaving the frontends pointed at whatever else holds the
  # port. Reuse it if it answers /health; refuse to continue if it does not.
  local server_pid="" existing
  existing="$(port_pid "${addr##*:}")"
  if [ -n "$existing" ]; then
    if http_ok "$health_url" 3; then
      say "Go API server: already running and healthy (pid $existing), reusing it."
    else
      err "Something holds $addr (pid $existing) but does not answer /health."
      say "          Stop it and re-run:  kill $existing"
      return 1
    fi
  else
    say "Starting Go API server..."
    (go run ./server 2>&1 | tee "$LOG_DIR/server.log") &
    server_pid=$!
    PIDS+=("$server_pid")

    # Gate the frontends on a real 200. Starting them first is what produced a
    # UI that loads, scans, and only then says it cannot reach the server.
    printf "Waiting for %s " "$health_url"
    local ready=0 i
    for i in $(seq 1 90); do
      if http_ok "$health_url" 2; then ready=1; break; fi
      if ! kill -0 "$server_pid" 2>/dev/null; then
        echo ""
        err "The Go server exited before it became healthy. Last lines:"
        tail -20 "$LOG_DIR/server.log" | sed 's/^/      /'
        return 1
      fi
      printf "."
      sleep 1
    done
    echo ""
    if [ "$ready" -eq 0 ]; then
      err "The Go server never answered /health. Last lines:"
      tail -20 "$LOG_DIR/server.log" | sed 's/^/      /'
      return 1
    fi
    ok "API server healthy"
  fi

  if [ "$want_desktop" = "1" ]; then
    say "Starting desktop app (Wails)..."
    (cd desktop-app && wails dev 2>&1 | tee "$LOG_DIR/desktop.log") &
    PIDS+=($!)
  fi

  if [ "$want_web" = "1" ]; then
    # web-app/package.json pins `vite --port 5173 --strictPort`. Without
    # strictPort Vite silently moves to 5174 when 5173 is taken, and 5174 is not
    # an allowed origin, so every request fails CORS and the UI blames an
    # unreachable server. It lives in package.json, not here, so a bare
    # `npm run dev:web` gets it too.
    local web_holder
    web_holder="$(port_pid "$WEB_PORT")"
    if [ -n "$web_holder" ]; then
      warn "Port $WEB_PORT is held by pid $web_holder; the web app cannot use it."
      say "         Only the web app is affected. Free it and re-run:  kill $web_holder"
    fi
    say "Starting web app (localhost:$WEB_PORT)..."
    (npm run dev:web 2>&1 | tee "$LOG_DIR/web.log") &
    PIDS+=($!)
  fi

  if [ "$want_open" = "1" ]; then
    say ""
    say "== Opening tabs =="
    local tabs=()
    if [ "$want_web" = "1" ] && wait_for_url "http://localhost:$WEB_PORT" "Web app"; then
      tabs+=("http://localhost:$WEB_PORT")
    fi
    if wait_for_url "http://127.0.0.1:$STUDIO_PORT" "Supabase Studio" 20; then
      tabs+=("http://127.0.0.1:$STUDIO_PORT")
    fi
    # Not "${tabs[@]:-}": on an empty array that expands to one empty string,
    # and `open ""` is a Finder window nobody asked for.
    if [ ${#tabs[@]} -gt 0 ]; then open_tabs "${tabs[@]}"; fi
  fi

  say ""
  say "Stockroom is running."
  say "  API server:  $health_url  (healthy)"
  [ "$want_desktop" = "1" ] && say "  Desktop app: a native window should open automatically"
  [ "$want_web" = "1" ]     && say "  Web app:     http://localhost:$WEB_PORT"
  say "  Studio:      http://127.0.0.1:$STUDIO_PORT"
  say ""
  say "  Sign in with 123456 / password (admin) or 234567 / password (student)."
  say ""
  say "Press Ctrl+C to stop everything and close all processes."

  wait
}

# ===========================================================================
# stop / status
# ===========================================================================
cmd_stop() {
  say "== Stopping Stockroom =="
  local addr pid
  addr="$(server_addr)"
  for port in "${addr##*:}" "$WEB_PORT" 34115; do
    pid="$(port_pid "$port")"
    if [ -n "$pid" ]; then
      say "  Killing pid $pid on port $port"
      kill -TERM "$pid" 2>/dev/null
    fi
  done
  say "  Stopping Supabase (data is preserved)..."
  supabase stop >/dev/null 2>&1
  ok "Stopped."
}

cmd_status() {
  local addr health_url
  addr="$(server_addr)"
  health_url="http://${addr}/health"
  say "== Stockroom status =="

  if docker info >/dev/null 2>&1; then ok "Docker running"; else warn "Docker not running"; fi
  if db_up; then ok "Postgres on $PG_PORT"; else warn "Postgres not reachable on $PG_PORT"; fi

  local pid
  pid="$(port_pid "${addr##*:}")"
  if [ -z "$pid" ]; then
    warn "No API server on $addr"
  elif http_ok "$health_url" 3; then
    ok "API server healthy on $addr (pid $pid)"
  else
    warn "pid $pid holds $addr but /health is not OK (Postgres down?)"
  fi

  pid="$(port_pid "$WEB_PORT")"
  if [ -n "$pid" ]; then ok "Web app on $WEB_PORT (pid $pid)"; else warn "No web app on $WEB_PORT"; fi

  if http_ok "http://127.0.0.1:$STUDIO_PORT" 3; then
    ok "Studio on $STUDIO_PORT"
  else
    warn "No Studio on $STUDIO_PORT"
  fi
}

# The header comment is the help text. A line range would drift the moment the
# header grew a line, so read until the comments stop instead.
usage() {
  awk 'NR==1 {next} /^#/ {sub(/^# ?/, ""); print; next} {exit}' "${BASH_SOURCE[0]}"
}

main() {
  local cmd="${1:-up}"
  [ $# -gt 0 ] && shift
  case "$cmd" in
    up)              cmd_up "$@" ;;
    deps)            cmd_deps "$@" ;;
    test|tests)      cmd_test "$@" ;;
    stop|down)       cmd_stop "$@" ;;
    status)          cmd_status "$@" ;;
    -h|--help|help)  usage ;;
    # `dev.sh --no-open` should mean `dev.sh up --no-open`, not an error: `up`
    # is the default subcommand, so its flags have to work without it.
    --*)             cmd_up "$cmd" "$@" ;;
    *)               err "unknown command: $cmd"; echo; usage; return 2 ;;
  esac
}

main "$@"
