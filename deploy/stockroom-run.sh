#!/usr/bin/env bash
# The service entry point: one script that both launchd and systemd start.
#
# It exists so there is ONE definition of "start Stockroom" rather than a
# macOS copy and a Linux copy that drift -- the same reasoning that collapsed
# four dev scripts into scripts/dev.sh (CLAUDE.md §13, 2026-09-15).
#
# Its whole job is three steps:
#
#   1. Wait for the Docker daemon. On macOS a login agent starts within seconds
#      of login while Docker Desktop takes the better part of a minute, so the
#      daemon is *reliably* absent at the moment this first runs.
#   2. Bring the database container up. Idempotent, so a restart is free.
#   3. exec the server, so the service manager supervises the real process
#      rather than this shell -- without exec, a crashed server leaves a live
#      wrapper and the service manager sees nothing wrong.
#
# The server then waits up to two minutes for Postgres itself (server/main.go),
# which is the other half of the same cold-start problem.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$HERE"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }

# Docker Desktop and Docker Engine both put the CLI somewhere a login shell
# finds and a service manager does not: launchd hands a process a PATH of
# /usr/bin:/bin:/usr/sbin:/sbin and nothing else. Adding the usual homes here
# is cheaper than teaching two service definitions about it.
export PATH="$PATH:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin"

if ! command -v docker >/dev/null 2>&1; then
  log "ERROR: docker is not on PATH. Stockroom's database runs in a container."
  exit 1
fi

# Wait for the daemon, not the binary. `docker info` is the only check that
# distinguishes "Docker is installed" from "Docker is running", and the
# difference is the entire failure mode on a machine that has just booted.
deadline=$(( $(date +%s) + 180 ))
until docker info >/dev/null 2>&1; do
  if [ "$(date +%s)" -ge "$deadline" ]; then
    log "ERROR: Docker did not start within 3 minutes."
    exit 1
  fi
  log "waiting for Docker to start..."
  sleep 5
done

log "starting the database container"
docker compose up -d

log "starting the Stockroom server"
exec ./stockroom
