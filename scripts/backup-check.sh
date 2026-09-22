#!/usr/bin/env bash
# backup-check.sh -- exercise and verify the backup system from the command line.
#
# A testing aid, not part of the product: everything it does is also a button in
# Admin -> Backup / Admin -> Settings. It exists because "did the backup work?"
# should be answerable in one command while you are setting a target up, and
# because it checks the one thing no screen shows you -- that every SHA-256 in
# the archive's manifest still matches the file it names, which is exactly what
# a restore verifies before it will touch the database.
#
# Usage:
#   scripts/backup-check.sh              status only, changes nothing
#   scripts/backup-check.sh --test       + run each enabled target's connection check
#   scripts/backup-check.sh --run        + run a real backup now, then re-read status
#   scripts/backup-check.sh --versions   + list the dated backups on every target
#   scripts/backup-check.sh --all        all of the above
#
# Credentials come from .env (ADMIN_STUDENT_NUMBER / ADMIN_PASSWORD); any admin
# account works via --number / --password.
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 1

DO_TEST=0; DO_RUN=0; DO_VERSIONS=0; NUMBER=""; PASSWORD=""
while [ $# -gt 0 ]; do
  case "$1" in
    --test) DO_TEST=1 ;;
    --run) DO_RUN=1 ;;
    --versions) DO_VERSIONS=1 ;;
    --all) DO_TEST=1; DO_RUN=1; DO_VERSIONS=1 ;;
    --number) NUMBER="${2:-}"; shift ;;
    --password) PASSWORD="${2:-}"; shift ;;
    -h|--help) sed -n '2,22p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown flag: $1"; exit 2 ;;
  esac
  shift
done

command -v jq >/dev/null || { echo "this script needs jq (brew install jq)"; exit 1; }

if [ -f .env ]; then
  # Trim surrounding whitespace, because the server's .env loader does too: a
  # leading space in ADMIN_PASSWORD makes the file and the running server
  # disagree about the password, and the only symptom is "bad credentials".
  trim() { sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//'; }
  [ -z "$NUMBER" ]   && NUMBER=$(sed -n 's/^ADMIN_STUDENT_NUMBER=//p' .env | trim)
  [ -z "$PASSWORD" ] && PASSWORD=$(sed -n 's/^ADMIN_PASSWORD=//p' .env | trim)
  ADDR=$(sed -n 's/^SERVER_ADDR=//p' .env | tr -d ' \r')
fi
BASE="http://${ADDR:-127.0.0.1:8080}"

# STATUS is the script's own answer. Every failure goes through bad(), so the
# exit code is set in one place and cannot drift from what was printed -- this
# is run from a terminal while somebody sets a target up, but also from a `&&`
# chain, and a script that prints FAIL and exits 0 is worse than no check.
STATUS=0

bold() { printf '\n\033[1m%s\033[0m\n' "$*"; }
ok()   { printf '  \033[32mOK\033[0m   %s\n' "$*"; }
bad()  { STATUS=1; printf '  \033[31mFAIL\033[0m %s\n' "$*"; }
warn() { printf '  \033[33mNOTE\033[0m %s\n' "$*"; }

bold "1. Is the server up?"
if curl -fsS --max-time 5 "$BASE/health" >/dev/null 2>&1; then
  ok "$BASE/health answers"
else
  bad "no answer from $BASE/health -- start it with ./scripts/dev.sh up"; exit 1
fi

bold "2. Signing in"
if [ -z "$NUMBER" ] || [ -z "$PASSWORD" ]; then
  bad "no admin credentials: set ADMIN_STUDENT_NUMBER and ADMIN_PASSWORD in .env"; exit 1
fi
LOGIN=$(curl -sS -X POST "$BASE/auth/password" -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg n "$NUMBER" --arg p "$PASSWORD" '{student_number:$n,password:$p}')")
TOKEN=$(printf '%s' "$LOGIN" | jq -r '.token // empty')
if [ -z "$TOKEN" ]; then
  bad "login failed for $NUMBER"
  printf '%s\n' "$LOGIN" | jq . 2>/dev/null || printf '%s\n' "$LOGIN"
  echo "  The failsafe admin is re-created on every server start; if this fails, check the"
  echo "  server log for a warning saying why it was rejected (digits only, password 8-72)."
  exit 1
fi
ok "signed in as $NUMBER"
AUTH=(-H "Authorization: Bearer $TOKEN")
api() { curl -fsS "${AUTH[@]}" "$@"; }

bold "3. Settings -- where backups are meant to go"
SETTINGS=$(api "$BASE/admin/settings") || { bad "could not read settings"; exit 1; }
printf '%s' "$SETTINGS" | jq -r '
  "  backup folder    " + (.backup_dir // "(not set - nothing will be written)"),
  "  photo mirror     " + (.photo_backup_dir // "(not set)"),
  "  schedule hour    " + (.schedule_hour|tostring),
  "  keep days        " + (.keep_days|tostring),
  "  stale after      " + (.stale_hours|tostring) + " h",
  "  encrypted        " + (if .archive_passphrase_set then "yes" else "no" end),
  "",
  "  Google Drive     " + (if .drive_enabled then "ENABLED  remote=" + (.drive_remote//"?") + " path=" + (.drive_path//"/") else "off" end),
  "  GitHub           " + (if .github_enabled then "ENABLED  repo=" + (.github_repo//"?") + "  token=" + (if .github_token_set then "set" else "MISSING" end) else "off" end)'

TARGETS=$(printf '%s' "$SETTINGS" | jq -r 'if .drive_enabled then "drive" else empty end, if .github_enabled then "github" else empty end')

if [ "$DO_TEST" = 1 ]; then
  bold "4. Test connection, per enabled target"
  if [ -z "$TARGETS" ]; then
    warn "no off-site target is enabled yet (Admin -> Settings)"
  else
    for t in $TARGETS; do
      BODY=$(mktemp)
      CODE=$(curl -sS -o "$BODY" -w '%{http_code}' -X POST "${AUTH[@]}" \
        -H 'Content-Type: application/json' -d "{\"target\":\"$t\"}" "$BASE/admin/settings/test")
      if [ "$CODE" = "200" ]; then ok "$t: connection works"
      else bad "$t: HTTP $CODE -- $(jq -r '.error // .' "$BODY" 2>/dev/null || cat "$BODY")"; fi
      rm -f "$BODY"
    done
  fi
fi

if [ "$DO_RUN" = 1 ]; then
  bold "5. Running a backup now (same code path as Admin -> Backup -> Backup Now)"
  BODY=$(mktemp)
  CODE=$(curl -sS -o "$BODY" -w '%{http_code}' -X POST "${AUTH[@]}" "$BASE/admin/backup")
  if [ "$CODE" = "200" ]; then
    ok "run finished -- a failed push is reported per target, it never fails the run"
    jq . "$BODY" 2>/dev/null | sed 's/^/  /' || cat "$BODY"
  else
    bad "HTTP $CODE"; jq -r '.error // .' "$BODY" 2>/dev/null | sed 's/^/  /' || cat "$BODY"
  fi
  rm -f "$BODY"
fi

bold "6. Status -- what the backup screen and every sign-in show"
# mktemp, not a fixed /tmp name: two copies of this script, or a stale file
# left by somebody else's run, would otherwise report on each other's status.
STATUS_JSON=$(mktemp)
trap 'rm -f "$STATUS_JSON"' EXIT
api "$BASE/admin/backup/status" > "$STATUS_JSON" 2>/dev/null || true
if [ -s "$STATUS_JSON" ]; then
  jq -r '
    (if ((.warnings // []) | length) == 0 then "  OK   no warnings"
     else ((.warnings // [])[] | "  NOTE " + (if type=="object" then (.message // tostring) else tostring end)) end),
    "",
    ((.targets // [])[] | "  " + ((.target // .name // "?")|tostring) + "  last success: " + ((.last_success // .last_success_at // "never")|tostring)
      + (if (.last_error // "") != "" then "\n      last error: " + (.last_error|tostring) else "" end))
  ' "$STATUS_JSON"
  echo
  jq -r '"  photo generations: " + (((.photo_generations // []) | length)|tostring)' "$STATUS_JSON"
  jq -r '(.log_tail // .log // "") | if type=="array" then .[-12:][] else (split("\n")[-12:][]) end' "$STATUS_JSON" 2>/dev/null \
    | sed '/^$/d; s/^/    /' | { grep . && echo || true; }
else
  warn "no status returned"
fi

if [ "$DO_VERSIONS" = 1 ]; then
  bold "7. What is actually stored on each target"
  for t in local $TARGETS; do
    echo "  --- $t ---"
    api "$BASE/admin/backup/versions?target=$t" 2>/dev/null \
      | jq -r 'if type=="array" then .[] else (.versions // .items // [])[] end
               | if type=="object" then "    " + ([.date,.name,.id,(.size|tostring)]|map(select(. != null and . != "null"))|join("  ")) else "    " + tostring end' \
      || echo "    (no answer)"
  done
fi

bold "8. The local archive, verified by hand"
DIR=$(printf '%s' "$SETTINGS" | jq -r '.backup_dir // empty')
if [ -z "$DIR" ]; then
  warn "no backup folder configured -- set one in Admin -> Settings, then re-run with --run"
elif [ ! -d "$DIR" ]; then
  bad "backup folder $DIR does not exist"
else
  LATEST=$(ls -1dt "$DIR"/*/ 2>/dev/null | head -1)
  if [ -z "$LATEST" ]; then
    warn "no dated folder in $DIR yet -- re-run with --run"
  else
    ok "newest dated folder: $LATEST"
    ls -1sh "$LATEST" | sed 's/^/     /'
  fi
  # The zip is the artefact a restore actually reads. The dated folder holds the
  # two readable reports (inventory.csv, accounts.csv) beside it; every table
  # CSV, the sequences, the manifest and RESTORE.md live inside the archive.
  ZIP=$(ls -1t "$DIR"/*/*.zip "$DIR"/*/*.zip.enc "$DIR"/*.zip "$DIR"/*.zip.enc 2>/dev/null | head -1)
  if [ -z "$ZIP" ]; then
    warn "no archive yet -- re-run with --run"
  elif [ "${ZIP##*.}" = "enc" ]; then
    ok "restorable archive: $ZIP ($(du -h "$ZIP" | cut -f1)) -- encrypted"
    warn "an encrypted archive cannot be checked from here; the restore verifies it with the passphrase"
  else
    ok "restorable archive: $ZIP ($(du -h "$ZIP" | cut -f1))"
    python3 scripts/verify-archive.py "$ZIP" || STATUS=1
  fi
fi

bold "Done."
echo "  List what the restore CLI can see:   go run ./cmd/restore --list"
echo "  Full round trip (REPLACES every record, then you sign in again):"
echo "    go run ./cmd/restore --yes <the zip above>"
exit "$STATUS"
