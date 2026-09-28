#!/usr/bin/env bash
# Installs a Stockroom .deb on this machine and checks it end to end, then
# installs a second .deb over it and checks the upgrade. CI's package-linux
# jobs run it (CI.md); it wants a throwaway machine or container, as root.
#
#   packaging/linux/smoke.sh FIRST.deb SECOND.deb [--no-service]
#
# SECOND.deb must carry one migration FIRST.deb doesn't, so the upgrade has to
# dump the database before applying it. With --no-service (a container with no
# systemd) setup skips the service and this script runs `stockroom serve`
# itself.
set -euo pipefail

first=$1 second=$2 mode=${3:-}
config=/etc/stockroom/stockroom.env
pwfile=$(mktemp)
echo password123 > "$pwfile"
export DEBIAN_FRONTEND=noninteractive

step() { printf '\n=== %s\n' "$*"; }
fail() { printf 'smoke: %s\n' "$*" >&2; exit 1; }

health() {
  for _ in $(seq 120); do
    curl -fs http://127.0.0.1:8080/health && { echo; return 0; }
    sleep 1
  done
  fail "no answer from /health"
}

# serve_by_hand starts the server the way the unit would, for --no-service.
serve_by_hand() {
  pkill -f "stockroom serve" 2>/dev/null || true
  sleep 1
  runuser -u stockroom -- /usr/bin/stockroom serve --config "$config" >> /tmp/stockroom.log 2>&1 &
}

step "install $first"
apt-get update -q
apt-get install -y -q "$first"

step "setup"
if [ "$mode" = --no-service ]; then
  stockroom setup --non-interactive --no-open --no-service --service-user stockroom \
    --admin-number 900100 --admin-password-file "$pwfile"
  serve_by_hand
else
  # Setup runs the service as the account that ran sudo, as it would for a
  # teacher. CI's runner account is that account here.
  SUDO_USER=${SUDO_USER:-$(logname 2>/dev/null || echo runner)} \
    stockroom setup --non-interactive --no-open \
    --admin-number 900100 --admin-password-file "$pwfile"
  [ "$(systemctl is-enabled stockroom)" = enabled ] || fail "the service isn't enabled"
fi
health

step "setup again repairs rather than reinstalls"
if [ "$mode" = --no-service ]; then
  stockroom setup --non-interactive --no-open --no-service --service-user stockroom
else
  stockroom setup --non-interactive --no-open
fi
health

if [ "$mode" != --no-service ]; then
  step "restart"
  systemctl restart stockroom
  health
fi

step "upgrade to $second"
dumps=/var/lib/stockroom/backups/pre-migrate
apt-get install -y -q --allow-downgrades --reinstall "$second" || dpkg -i "$second"
[ "$mode" = --no-service ] && serve_by_hand
health
ls -l "$dumps"
ls "$dumps"/pre-migrate-*.sql >/dev/null 2>&1 || fail "the upgrade wrote no pre-migrate dump"

step "doctor"
if [ "$mode" = --no-service ]; then
  runuser -u stockroom -- stockroom doctor
else
  stockroom doctor
fi
