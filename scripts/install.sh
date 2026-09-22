#!/usr/bin/env bash
#
# Install Stockroom as a service on this machine (macOS or Linux).
#
# What this produces is one directory containing one binary -- API, web UI and
# database migrations all compiled in -- plus a compose file for a single plain
# Postgres container, a generated .env, and a service definition that starts
# the whole thing at boot and restarts it if it dies.
#
# What it deliberately does NOT produce is a development environment. The
# Supabase CLI, `supabase start`, seed.sql and Studio are all development
# tools and none of them is installed here; see deploy/docker-compose.yml.
#
#     ./scripts/install.sh                 # install or upgrade in ~/Stockroom
#     ./scripts/install.sh --home /opt/stockroom
#     ./scripts/install.sh --no-service    # set it up but don't register it
#
# Running it a second time is the UPGRADE path: it takes a database dump
# first, rebuilds, swaps the binary and restarts the service. That is why "it
# broke, I'll run the installer again" is the thing that fixes it, and why the
# dump happens before anything is replaced -- a migration that goes wrong is
# the one failure an upgrade can cause, and the dump is the only protection
# against it.
#
# Windows is not covered. dev.ps1 has never run on real Windows hardware
# (CLAUDE.md §9, still open) and an untested service installer is worse than an
# absent one; TEMPLATE-TODO Phase A carries it.
set -euo pipefail

# ---------------------------------------------------------------------------
# Where things are
# ---------------------------------------------------------------------------
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STOCKROOM_HOME="${STOCKROOM_HOME:-$HOME/Stockroom}"
SERVER_ADDR="${SERVER_ADDR:-127.0.0.1:8080}"
INSTALL_SERVICE=1
OPEN_BROWSER=1
ADMIN_NUMBER="${ADMIN_STUDENT_NUMBER:-}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-}"

say()  { echo "$@"; }
ok()   { echo "  [OK] $*"; }
warn() { echo "  [WARN] $*"; }
err()  { echo "  [ERROR] $*" >&2; }
die()  { err "$@"; exit 1; }

usage() {
  sed -n '3,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  exit 0
}

while [ $# -gt 0 ]; do
  case "$1" in
    --home)           STOCKROOM_HOME="$2"; shift 2 ;;
    --addr)           SERVER_ADDR="$2"; shift 2 ;;
    --admin-number)   ADMIN_NUMBER="$2"; shift 2 ;;
    --admin-password) ADMIN_PASSWORD="$2"; shift 2 ;;
    --no-service)     INSTALL_SERVICE=0; shift ;;
    --no-open)        OPEN_BROWSER=0; shift ;;
    -h|--help)        usage ;;
    *)                die "unknown option: $1 (try --help)" ;;
  esac
done

case "$(uname -s)" in
  Darwin) OS=macos ;;
  Linux)  OS=linux ;;
  *)      die "$(uname -s) is not supported by this installer. See docs/INSTALL.md." ;;
esac

# ---------------------------------------------------------------------------
# 1. Preconditions
# ---------------------------------------------------------------------------
say ""
say "Stockroom installer"
say "  target directory : $STOCKROOM_HOME"
say "  platform         : $OS"
say ""
say "Checking what's installed..."

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed. $2"
}

need docker "Install Docker Desktop (macOS) or Docker Engine (Linux) and run this again."
need go "Install Go from https://go.dev/dl/ -- it is needed to build, not to run."
need npm "Install Node.js from https://nodejs.org/ -- it is needed to build, not to run."
ok "docker, go and npm are present"

# The daemon, not the binary. The difference is the whole failure mode on a
# machine where Docker Desktop is installed but has never been opened.
if ! docker info >/dev/null 2>&1; then
  say "  Docker is installed but not running. Waiting for it (about a minute the first time)..."
  if [ "$OS" = macos ]; then
    open -ga Docker 2>/dev/null || true
  fi
  deadline=$(( $(date +%s) + 180 ))
  until docker info >/dev/null 2>&1; do
    [ "$(date +%s)" -lt "$deadline" ] || die "Docker did not start within 3 minutes. Start Docker and run this again."
    sleep 5
  done
fi
ok "the Docker daemon is responding"

# ---------------------------------------------------------------------------
# 2. Is this an upgrade? Take a dump before touching anything.
# ---------------------------------------------------------------------------
UPGRADE=0
if [ -x "$STOCKROOM_HOME/stockroom" ]; then
  UPGRADE=1
  say ""
  say "Found an existing install. This is an upgrade."

  mkdir -p "$STOCKROOM_HOME/backups"
  dump="$STOCKROOM_HOME/backups/pre-upgrade-$(date +%Y%m%d-%H%M%S).sql"

  # pg_dump from inside the container, so no Postgres client is needed on the
  # host -- there isn't one on a machine installed this way, by design. If the
  # container is not running there is nothing to dump and nothing at risk.
  if docker compose -f "$STOCKROOM_HOME/docker-compose.yml" --env-file "$STOCKROOM_HOME/.env" ps --status running --quiet db 2>/dev/null | grep -q .; then
    say "  Taking a database dump first: $(basename "$dump")"
    if docker compose -f "$STOCKROOM_HOME/docker-compose.yml" --env-file "$STOCKROOM_HOME/.env" \
         exec -T db pg_dump -U postgres postgres > "$dump" 2>/dev/null; then
      ok "dumped $(wc -c < "$dump" | tr -d ' ') bytes"
    else
      rm -f "$dump"
      # Stop rather than continue. The dump is the only thing standing between
      # a bad migration and a lost inventory, and an upgrade that skips it
      # silently is exactly the trade nobody would agree to if asked.
      die "could not dump the database. Not upgrading. Start the database and try again, or pass --no-service and upgrade by hand."
    fi
  else
    warn "the database container is not running, so there is nothing to dump"
  fi
fi

# ---------------------------------------------------------------------------
# 3. Build: one binary with the UI and the migrations inside it
# ---------------------------------------------------------------------------
say ""
say "Building..."

# One definition of an installed JS tree, called from here as well as from
# `dev.sh up` and `dev.sh test` (CLAUDE.md §13, 2026-09-14). Duplicating the
# npm install here is how the nested-node_modules bug gets back in.
"$REPO/scripts/dev.sh" deps >/dev/null || die "dependency install failed; run ./scripts/dev.sh deps to see why"
ok "dependencies are up to date"

( cd "$REPO" && npm run build --workspace=web-app >/dev/null ) || die "the web UI failed to build"
[ -f "$REPO/web-app/dist/index.html" ] || die "the web UI built but produced no index.html"
ok "web UI built"

mkdir -p "$REPO/.build"
# -trimpath and a stripped binary: neither is load-bearing, both keep the
# artefact smaller and free of this machine's directory names.
( cd "$REPO" && go build -trimpath -ldflags "-s -w" -o "$REPO/.build/stockroom" ./server ) \
  || die "the server failed to build"
ok "server built ($(du -h "$REPO/.build/stockroom" | cut -f1) with the UI and migrations inside)"

# ---------------------------------------------------------------------------
# 4. The install directory
# ---------------------------------------------------------------------------
say ""
say "Installing into $STOCKROOM_HOME"
mkdir -p "$STOCKROOM_HOME"/{logs,uploads,backups,photo-backups,.cache}

cp "$REPO/deploy/docker-compose.yml" "$STOCKROOM_HOME/docker-compose.yml"
cp "$REPO/deploy/stockroom-run.sh"   "$STOCKROOM_HOME/stockroom-run.sh"
chmod +x "$STOCKROOM_HOME/stockroom-run.sh"

# ---------------------------------------------------------------------------
# 5. .env -- generated once, never overwritten
# ---------------------------------------------------------------------------
# Never overwritten on an upgrade, and that direction matters: the backup
# folder, the Drive remote and the photo-wall folder are first-boot seeds that
# app_settings owns from then on (CLAUDE.md §9). An installer that rewrote them
# every release would be the "a value an admin typed reverts on the next
# restart" failure the Phase 7 decision rules out.
ENV_FILE="$STOCKROOM_HOME/.env"
if [ -f "$ENV_FILE" ]; then
  ok "keeping the existing .env (settings live in the database now, not here)"
else
  # 32 characters out of /dev/urandom. postgres:postgres is correct for a
  # localhost dev stack and wrong on a machine in a closet that students walk
  # past; nothing ever shows this value to anybody.
  #
  # `dd` a fixed block and then `cut`, rather than the obvious
  # `tr -dc ... < /dev/urandom | head -c 32`. `head` exits the moment it has
  # its 32 bytes, which sends SIGPIPE to a `tr` reading an infinite file --
  # and under `set -o pipefail` that is a failed pipeline, so the script died
  # with status 141 and no message at all. `cut` reads its input to the end,
  # so nothing is ever signalled. 512 random bytes yield about 124 usable
  # characters, four times what is taken.
  PGPASS="$(dd if=/dev/urandom bs=512 count=1 2>/dev/null | LC_ALL=C tr -dc 'A-Za-z0-9' | cut -c1-32)"
  [ ${#PGPASS} -eq 32 ] || die "could not generate a database password (got ${#PGPASS} characters)"

  if [ -z "$ADMIN_NUMBER" ] && [ -t 0 ]; then
    say ""
    say "  The failsafe admin is the way back into the admin panel when"
    say "  everything else has gone wrong -- a forgotten password, a bad"
    say "  import, a database restored from empty. It is re-applied on every"
    say "  start, so it cannot be locked out. Leave it blank to skip it."
    printf "  Failsafe admin student number (digits only): "
    read -r ADMIN_NUMBER
    if [ -n "$ADMIN_NUMBER" ]; then
      printf "  Failsafe admin password (8 characters or more, hidden): "
      read -rs ADMIN_PASSWORD
      echo ""
    fi
  fi

  umask 077
  cat > "$ENV_FILE" <<ENVEOF
# Generated by scripts/install.sh on $(date '+%Y-%m-%d %H:%M:%S').
#
# Almost nothing lives here any more. Backups, the Drive remote and the
# photo-wall folder are configured in Admin -> Settings and stored in the
# database; the three values below marked SEED only fill an empty column the
# very first time the server starts against a fresh database, and are ignored
# on every start after that (CLAUDE.md §9).
#
# This file holds the database superuser password. Keep it at mode 600.

DATABASE_URL=postgresql://postgres:${PGPASS}@127.0.0.1:54322/postgres
POSTGRES_PASSWORD=${PGPASS}

SERVER_ADDR=${SERVER_ADDR}

# The failsafe admin (CLAUDE.md §7). Blank is allowed; the server logs a
# warning and starts anyway, and the backup screen says so out loud.
ADMIN_STUDENT_NUMBER=${ADMIN_NUMBER}
ADMIN_PASSWORD=${ADMIN_PASSWORD}

UPLOADS_DIR=${STOCKROOM_HOME}/uploads

# SEED. Absolute paths on purpose: a relative backup folder resolves against
# whatever directory the service happened to start in, which produces a
# complete archive, a manifest that verifies, every screen reporting health,
# and files nowhere anybody will look (CLAUDE.md §13, 2026-09-21).
BACKUP_DIR=${STOCKROOM_HOME}/backups
PHOTO_BACKUP_DIR=${STOCKROOM_HOME}/photo-backups
RCLONE_REMOTE=

SESSION_IDLE_MINUTES=10

# The sign-in photo wall is optional decoration and off by default; the remote
# is the switch (CLAUDE.md §9).
SIGNIN_PHOTOS_REMOTE=
SIGNIN_PHOTOS_FOLDER_ID=
SIGNIN_PHOTOS_DIR=${STOCKROOM_HOME}/.cache/signin-photos
SIGNIN_PHOTOS_COUNT=48
SIGNIN_PHOTOS_BATCH=16
SIGNIN_PHOTOS_TTL_MINUTES=15
SIGNIN_PHOTOS_MANIFEST_HOURS=168
ENVEOF
  chmod 600 "$ENV_FILE"
  ok "wrote .env with a generated database password"
  [ -n "$ADMIN_NUMBER" ] || warn "no failsafe admin configured; set ADMIN_STUDENT_NUMBER and ADMIN_PASSWORD in $ENV_FILE later"
fi

# ---------------------------------------------------------------------------
# 6. Database, then the binary
# ---------------------------------------------------------------------------
say ""
say "Starting the database..."

# Refuse a port somebody else is holding, by name, rather than letting compose
# report "bind: address already in use" -- which is true and useless. The
# overwhelmingly likely holder is the Supabase CLI stack on a development
# machine, which maps the same 54322 deliberately (deploy/docker-compose.yml),
# and "run supabase stop" is an instruction somebody can act on.
if ! ( cd "$STOCKROOM_HOME" && docker compose ps --status running --quiet db 2>/dev/null | grep -q . ); then
  # `awk NR==1` rather than `head -1`, for the same reason the password above
  # does not use `head -c`: head exits as soon as it has its line and SIGPIPEs
  # whatever is feeding it, which under `set -o pipefail` is a failed command
  # substitution and, under `set -e`, a script that stops with no message.
  # `|| true` because lsof exits 1 when nothing matches, which is the *good*
  # case here; without it `set -e` ends the script at the moment the port is
  # free. Redirecting stderr does not change an exit status.
  holder="$(lsof -nP -iTCP:54322 -sTCP:LISTEN -t 2>/dev/null | awk 'NR==1' || true)"
  if [ -n "$holder" ]; then
    err "something is already listening on port 54322: $(ps -o comm= -p "$holder" 2>/dev/null || echo "pid $holder")"
    err "On a development machine that is almost certainly the Supabase CLI stack, which uses the"
    err "same port on purpose. Run 'supabase stop' and try again, or install on a machine that is"
    err "not also a development machine."
    exit 1
  fi
fi

( cd "$STOCKROOM_HOME" && docker compose up -d ) || die "the database container did not start"
ok "postgres:17 is running on 127.0.0.1:54322"

# The binary is copied AFTER the dump and the database are both settled, so an
# interrupted run leaves the previous working install in place.
cp "$REPO/.build/stockroom" "$STOCKROOM_HOME/stockroom.new"
mv "$STOCKROOM_HOME/stockroom.new" "$STOCKROOM_HOME/stockroom"
chmod +x "$STOCKROOM_HOME/stockroom"
ok "binary installed"

# ---------------------------------------------------------------------------
# 7. The service
# ---------------------------------------------------------------------------
PLIST="$HOME/Library/LaunchAgents/com.stockroom.server.plist"
UNIT="/etc/systemd/system/stockroom.service"

stop_service() {
  case "$OS" in
    macos) [ -f "$PLIST" ] && launchctl bootout "gui/$(id -u)" "$PLIST" 2>/dev/null || true ;;
    linux) systemctl is-enabled stockroom.service >/dev/null 2>&1 && sudo systemctl stop stockroom.service || true ;;
  esac
}

if [ "$INSTALL_SERVICE" = 1 ]; then
  say ""
  say "Registering the service (it will start at boot and restart if it dies)..."
  stop_service

  case "$OS" in
    macos)
      mkdir -p "$HOME/Library/LaunchAgents"
      sed -e "s|__STOCKROOM_HOME__|$STOCKROOM_HOME|g" -e "s|__USER__|$(id -un)|g" \
        "$REPO/deploy/com.stockroom.server.plist.template" > "$PLIST"
      # bootstrap + kickstart rather than the deprecated `load`: `load` reports
      # success on a plist launchd then refuses, which is a service that is
      # registered and not running with nothing said about it.
      launchctl bootstrap "gui/$(id -u)" "$PLIST"
      launchctl kickstart -k "gui/$(id -u)/com.stockroom.server"
      ok "launchd agent installed at $PLIST"
      say "  NOTE: this starts at LOGIN, not at boot, because Docker Desktop only"
      say "  runs inside a user session. Set this Mac to log in automatically"
      say "  (System Settings -> Users & Groups -> Automatic login) or the machine"
      say "  will sit at the login window after a power cut and quietly stop"
      say "  backing up."
      ;;
    linux)
      sed -e "s|__STOCKROOM_HOME__|$STOCKROOM_HOME|g" -e "s|__USER__|$(id -un)|g" \
        "$REPO/deploy/stockroom.service.template" | sudo tee "$UNIT" >/dev/null
      sudo systemctl daemon-reload
      sudo systemctl enable --now stockroom.service
      ok "systemd unit installed at $UNIT"
      ;;
  esac
else
  warn "skipping service registration (--no-service)"
  say "  Start it by hand with: cd $STOCKROOM_HOME && ./stockroom-run.sh"
fi

# ---------------------------------------------------------------------------
# 8. Prove it
# ---------------------------------------------------------------------------
URL="http://$SERVER_ADDR"
if [ "$INSTALL_SERVICE" = 1 ]; then
  say ""
  say "Waiting for the server..."
  deadline=$(( $(date +%s) + 180 ))
  until curl -fsS -m 2 "$URL/health" >/dev/null 2>&1; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
      err "the server did not answer $URL/health within 3 minutes."
      err "The last 20 lines of $STOCKROOM_HOME/logs/server.log:"
      tail -20 "$STOCKROOM_HOME/logs/server.log" 2>/dev/null >&2 || true
      exit 1
    fi
    sleep 2
  done
  ok "$URL/health is answering"

  if [ "$OPEN_BROWSER" = 1 ]; then
    case "$OS" in
      macos) open "$URL" 2>/dev/null || true ;;
      linux) xdg-open "$URL" >/dev/null 2>&1 || true ;;
    esac
  fi
fi

say ""
if [ "$UPGRADE" = 1 ]; then
  say "Upgraded. Stockroom is at $URL"
else
  say "Installed. Stockroom is at $URL"
fi
say ""
say "  Everything lives in   $STOCKROOM_HOME"
say "  Logs                  $STOCKROOM_HOME/logs/server.log"
say "  Upgrade or repair     re-run this script"
say ""
if [ "$UPGRADE" = 0 ]; then
  say "  The database is empty. Sign in as the failsafe admin, then build your"
  say "  category tree and add your equipment from the admin panel."
  say ""
fi
