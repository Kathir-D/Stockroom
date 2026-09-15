# Backup & restore

**Status: specified, not yet built.** Everything below Part A is the design
agreed on 2026-09-14; `TODO.md` Phase 7 is the checklist that implements it.
What exists today is the CSV export described in Part A.1 and nothing else.

`CLAUDE.md` §11 is the one-paragraph summary; this file is the detail.

---

## Part A — Where things stand

### A.1 What works today

`ExportAllTablesToCSV` (`internal/stockroom/backup.go`), reachable from
`POST /admin/backup` and the admin panel's **Backup now** button. Verified
against the live database on 2026-09-14: all 12 tables, 16 assets, 2 profiles,
with `password_hash`, `student_number`, `is_admin`, `serial_number` and
`asset_tag` all present.

It lists base tables from `information_schema` rather than a list in Go, so a
table added by a later migration is included without anyone remembering to. Each
table streams out through `COPY … TO STDOUT (format csv, header)`, the whole run
sits in one `repeatable read, read only` transaction so every file describes one
snapshot, and the output is written to a `.partial-*` staging directory that is
renamed into place at the end — a crashed run leaves yesterday's folder intact
rather than a half-written one.

### A.2 What is missing, and how each was verified

| # | Gap | Evidence |
|---|---|---|
| 1 | Nothing goes off-site | `cmd/backup/` does not exist; `RCLONE_REMOTE` is absent from `.env.example` and unread by `config.go` |
| 2 | Photos never backed up | `uploads` is not referenced anywhere in `backup.go` |
| 3 | Sequence state lost | `assets_asset_tag_seq` has `last_value` = 89; it is not captured, so a restore would restart at 1 and collide on the unique `asset_tag` |
| 4 | CSVs load in FK-violating order | Alphabetical: `assets.csv` sorts before `categories.csv`, which it references |
| 5 | A restore would fire triggers | `trg_asset_status_log` on `assets` would write junk `activity_log` rows on top of the ones being restored |
| 6 | Schema version not recorded | `supabase_migrations.schema_migrations` is outside the `public` schema, so `publicTables` skips it |
| 7 | Silent failure is invisible | No log, no staleness signal anywhere |
| 8 | Cross-process race | `backupReplace` is an in-process mutex; a separate CLI process would not honour it |
| 9 | No restore path at all | `psql` is not installed on the dev machine, and Postgres 17.6 runs inside Docker, so server-side `COPY FROM` cannot see host files |
| 10 | All configuration is `.env` | Admins would have to edit a file to change anything |

---

## Part B — Decisions

- **Two targets, both active when both are configured.** Google Drive and
  GitHub. Configure one and it pushes to one; configure both and it pushes to
  both, tracking success per target.
- **Everything is configured in the UI.** The one-time software install (Go,
  Docker, Supabase CLI, `rclone`) is the installer's job. After that no admin or
  user edits a file. Settings live in the database, not `.env`.
- **The Go server schedules itself.** No Task Scheduler, no launchd, no scripts.
- **Restore is a file upload in the admin panel**, plus in-app browsing of past
  backups on *both* targets. A `cmd/restore` CLI covers the disaster case.
- **Photos are local only, one live folder, generational rollover.** Not a copy
  per day.
- **`accounts.csv` carries `password_hash`.** Plaintext passwords are not
  recoverable — bcrypt is one-way and the server never stores them. Restoring the
  hash is equivalent for the user: their existing password keeps working.
- **Retention defaults to 90 days**, editable in the UI.

### B.1 Privacy, flagged and accepted

`accounts.csv` and `profiles.csv` hold student numbers, and per `CLAUDE.md` §7 a
student number *is* a working credential — scanning it signs its owner in with no
password. The folder pushed off-machine is therefore a roster of usable logins.
This was raised and accepted on 2026-09-14. It is why the GitHub repository
**must be private** and why the Drive account should not be a shared one.

---

## Part C — Architecture

### C.1 Why upload-to-restore works at all

If the database is wiped there are no accounts, so nobody can sign in to the
admin panel to restore it. This is already solved, by accident:
`EnsureFailsafeAdmin` (`failsafe.go`) runs on **every server start** and recreates
the `.env` admin. The recovery sequence is:

1. Fresh or wiped Postgres → `supabase db reset` applies migrations, schema only
2. Start the Go server → the failsafe admin exists
3. Sign in → Admin → Backup → restore from an upload, or pick a date from Drive/GitHub

`cmd/restore` exists for the narrower case where the *server itself* will not start.

### C.2 Settings move into the database

New migration, single-row table using the standard `check (id)` trick so a second
row is impossible:

```sql
create table app_settings (
  id               boolean primary key default true check (id),
  backup_dir       text,
  photo_backup_dir text,
  keep_days        integer not null default 90,
  stale_hours      integer not null default 48,
  schedule_hour    integer not null default 2,     -- 0-23, local time
  drive_enabled    boolean not null default false,
  drive_remote     text,
  drive_path       text default 'stockroom',
  github_enabled   boolean not null default false,
  github_repo      text,
  github_token     text,
  updated_at       timestamptz not null default now()
);
insert into app_settings (id) values (true);
```

`.env` becomes a **bootstrap fallback only**: on first start, a null column whose
matching environment variable is set is seeded from it. That preserves today's
behaviour and lets the installer pre-fill, without making the environment the
source of truth.

> **The token must not end up in the backup.** `app_settings` lives in the
> `public` schema, so `publicTables` picks it up — meaning `github_token` would be
> written into `app_settings.csv` and pushed to the very repository it grants
> write access to. GitHub's secret scanning would spot the `github_pat_` prefix
> and revoke it, silently killing backups a few hours after the first successful
> push. **The export nulls `github_token`, and any secret column added later, on
> the way out.** Restore leaves the live value alone rather than overwriting it
> with the null it finds.

### C.3 The target interface

```go
// internal/stockroom/target.go
type BackupTarget interface {
    Name() string
    Push(ctx context.Context, dir string, m Manifest) error
    Versions(ctx context.Context) ([]BackupVersion, error) // newest first
    Fetch(ctx context.Context, id string) (io.ReadCloser, error) // a zip stream
    Test(ctx context.Context) error // drives the UI's "Test connection"
}
```

`driveTarget` and `githubTarget` both implement all four.
`activeTargets(settings)` returns whichever are enabled; that is the whole of
"push to both when both are set up".

### C.4 Dependencies

| Need | Drive | GitHub |
|---|---|---|
| Go modules | none | none |
| External binary | **`rclone`** (installer step) | **none** |
| Secret | OAuth token in rclone's own config | fine-grained PAT, in `app_settings` |
| Restore needs | nothing extra | nothing extra |

Everything is Go standard library: `archive/zip`, `archive/tar`, `compress/gzip`,
`encoding/csv`, `net/http`. **No new `go.mod` entries.**

GitHub uses the REST API rather than the `git` binary deliberately: one secret,
nothing to install, no credential helper, no SSH keys, and the same token serves
both the push and the browse-history feature.

---

## Part D — Output shapes

### D.1 Local, always

```
backup_dir/
  .last-success.json        per target: time, rows, ref pushed, last error
  backup.log                rotating at 5 MB, one line per run
  2026-09-14/
    inventory.csv           one row per item, Excel-readable
    accounts.csv            one row per user, Excel-readable
    backup-2026-09-14.zip   the restorable archive
```

Zip contents: `tables/*.csv` (the 12 raw tables) · `sequences.csv` ·
`manifest.json` (row counts, `ran_at`, `schema_version`) · `inventory.csv` ·
`accounts.csv` · `RESTORE.md`.

Local dated folders are pruned past `keep_days`.

### D.2 Google Drive

`rclone copy` mirrors the dated folders to `<remote>:<drive_path>/`. The remote
keeps everything — a few hundred KB a night means 15 GB free lasts years. The
version list comes from `rclone lsjson`, the fetch from `rclone cat`.

### D.3 GitHub — a fixed path plus git history

```
stockroom-backup/                 ← private repository
  README.md                       auto-written by the app; Part F.4
  backup/
    inventory.csv                 overwritten each night
    accounts.csv
    sequences.csv
    manifest.json
    tables/*.csv
```

**One fixed path, overwritten nightly, one commit per run. No zips and no dated
folders in git.** CSVs are text, so git stores night-to-night deltas of a few KB.
Dated zips would add a full incompressible blob every night forever, with no way
to prune without rewriting history.

The **git history is the dated backup list**. Commit messages read
`Backup 2026-09-14 02:00 — 16 assets, 2 accounts, 63 categories`, and that is
what the in-app version picker lists.

---

## Part E — Implementation

### E.1 `backup.go` — extend the export

Additions go inside the existing snapshot transaction so every artifact describes
one instant. The staging-and-rename logic is untouched.

- **`sequences.csv`** — `select sequencename, last_value from pg_sequences where
  schemaname = 'public'`. Closes gap 3.
- **`schema_version`** in the manifest, read from
  `supabase_migrations.schema_migrations`. Closes gap 6. Restore refuses on a
  mismatch unless the admin explicitly overrides.
- **Secret redaction** on `app_settings` (C.2).
- **`inventory.csv`** — reuse `loadCategoryTree(ctx, q)` (`categories.go:74`, which
  takes a `querier`, so it runs on the transaction) and `tree.pathOf`
  (`categories.go:237`) for the category path rather than a recursive CTE, keeping
  `CLAUDE.md` §13's "the browse list is sorted in Go, not in SQL". Reuse
  `openCustodySQL` (`custody.go:33`) so "out" keeps one definition.
  Columns: `name, serial_number, asset_tag, type, category, model, status,
  condition, held_by, student_number, checked_out_at, due_at, overdue, photo_path`.
- **`accounts.csv`** — `student_number, first_name, last_name, full_name, email,
  is_admin, password_hash, has_password, created_at, photo_path`.
- **Zip assembly** via `archive/zip`; `RESTORE.md` embedded with `go:embed`.
- **A cross-process lock**, `backup_dir/.lock` via `O_CREATE|O_EXCL`, broken if
  stale after 30 minutes. Closes gap 8.

### E.2 `photos_backup.go` — the generational mirror

```
photo_backup_dir/
  .current                  names the live generation (a file, not a symlink — Windows)
  gen-2026-09-14/           live: updated in place every night
    profiles/…  assets/…
  gen-2026-06-16/           frozen at rollover; kept until deleted by hand
```

`MirrorPhotos(ctx)`:

1. Read `.current`; create a generation if there is none.
2. If that generation is older than `keep_days`, **roll over** — create
   `gen-<today>`, full-copy the current generation into it, repoint `.current`.
   The old generation is never written to again.
3. Walk `uploads/`, copying into the live generation only where size or mtime
   differs.
4. **Never delete.** A photo removed from `uploads/` stays in the mirror, so an
   accidental delete is recoverable, which is the point of a backup.
5. A missing `uploads/` is "nothing to do", not an error — it does not exist on a
   machine where no photo has been uploaded yet.

`RestorePhotos(ctx, actor, generation)` copies a chosen generation back into
`uploads/`, preserving anything it would overwrite.

### E.3 `target_drive.go`

`rclone copy <backup_dir> <remote>:<path> --exclude .lock --exclude *.log`, with
retry and backoff (1/2/5/15/30 min). `Versions` is `rclone lsjson`, `Fetch` is
`rclone cat` of the chosen dated zip, `Test` is `rclone lsd`. A missing binary is
`ErrNotConfigured` naming the install command, not a crash.

**Connect and reconnect from the UI**: the server runs `rclone authorize "drive"`,
captures the URL it prints, shows it as a clickable link, accepts the pasted token
back, and writes the remote with `rclone config create`. Without this the single
most likely failure — a revoked Google token — would need a terminal, which the
"no admin touches a file" rule forbids.

### E.4 `target_github.go`

Plain `net/http` against `api.github.com`, bearer token from `app_settings`.

- **Push** — the Git Data API, which is the only way to commit several files
  atomically: `POST /git/blobs` per file → `POST /git/trees` → `POST /git/commits`
  → `PATCH /git/refs/heads/main`. One commit, all or nothing.
- **Versions** — `GET /repos/{owner}/{repo}/commits?path=backup/&per_page=100`.
- **Fetch** — `GET /repos/{owner}/{repo}/tarball/{sha}`, extract `backup/`, and
  repackage in memory as the same zip the local path produces. **All three restore
  sources — upload, Drive, GitHub — therefore converge on one code path.**
- **Test** — `GET /repos/{owner}/{repo}` plus a permission check, so the UI can say
  "the token works but is read-only" rather than failing at 2 a.m.
- Writes the repository `README.md` on every push (Part F.4).

### E.5 `restore.go`

`RestoreFromZip(ctx, actor, r io.ReaderAt, size int64, opts)`:

1. `RequireAdmin`.
2. Read `manifest.json`; compare `schema_version` to live. A mismatch is
   `ErrConflict` unless `opts.Force`.
3. One transaction, opening with `set local session_replication_role = replica`.
   **That single line closes gaps 4 and 5**: foreign keys are not checked, so load
   order stops mattering, and `trg_asset_status_log` stays quiet, so
   `activity_log` restores clean instead of gaining junk rows.
4. `truncate` all 12 tables, then `pgx.CopyFrom` each `tables/*.csv`.
5. `setval` every sequence from `sequences.csv`.
6. Commit, then verify live row counts against the manifest. **A mismatch rolls
   back.**
7. `db.Sessions.Clear()` — every existing token now points at a profile that may
   no longer exist.

Restoring is destructive, so the request carries `confirm=RESTORE`.

### E.6 `scheduler.go` — in-server, replacing OS scheduling

A goroutine started by `server/main.go`:

- On boot, if `.last-success.json` is older than `stale_hours`, **run immediately.**
  That is the whole of "back up first thing when the computer becomes available":
  the server starts with the machine, so a missed night is caught within seconds
  of power-on.
- Then a ticker firing at `schedule_hour`, local time.
- Settings are reloaded each tick, so changing the hour in the UI takes effect
  without a restart.
- Skips if a run is already in progress, via the E.1 lock.
- Writes `.last-success.json` per target and appends to `backup.log`.

This removes the need for Task Scheduler, launchd, a `cmd/backup` CLI, and the
`.env`-lookup problem OS scheduling would have caused: `findDotEnv`
(`config.go:69`) walks up from the working directory, and a Windows scheduled task
starts in `System32`, which is not inside the repository.

### E.7 Staleness alerts

`BackupStatus(ctx)` returns
`{per_target: {last_success, age, last_error}, stale, worst_age}`.

- **`LoginResult` and `MeResult` gain `BackupWarning *BackupWarning`.** Both
  already carry `HasOverdue` for exactly this shape (`auth.go:49`, `auth.go:171`),
  so this follows an established pattern rather than inventing one.
- **Admins** see `Backups have not run in 12 days. Open Admin → Backup.`
- **Non-admins** see `Backups have not run in 12 days — please tell Admin Admin or
  Jane Smith.` Admin **names only**, never student numbers, consistent with
  `CLAUDE.md` §7's custodian-visibility rule.
- **The backup screen** shows a banner with the age, per-target status and last
  error.

### E.8 Endpoints

| Route | Who | Notes |
|---|---|---|
| `GET/PUT /admin/settings` | admin | the settings screen; `github_token` returns masked |
| `POST /admin/settings/test` | admin | `{target}` → runs `Test()`, returns a readable pass/fail |
| `POST /admin/drive/connect` | admin | starts `rclone authorize`, returns the URL |
| `POST /admin/drive/finish` | admin | accepts the pasted token, writes the remote |
| `POST /admin/backup` | admin | existing; now also mirrors photos and pushes to every enabled target |
| `GET /admin/backup/status` | admin | E.7 |
| `GET /admin/backup/versions?target=` | admin | the dated list from Drive or GitHub |
| `POST /admin/restore` | admin | multipart `file` + `confirm=RESTORE`, 200 MB cap; follows the `POST /users/import` pattern in `server/users.go:97` |
| `POST /admin/restore/remote` | admin | `{target, id, confirm}` |
| `GET /admin/photos/generations`, `POST /admin/photos/restore` | admin | the photo mirror |

### E.9 Admin UI

A new **Settings** tab: folder paths with a "check this folder" validator,
retention days, stale threshold, schedule hour, and a card per target — Google
Drive with a **Connect** button and status, GitHub with repository and token
fields and **Test connection**. Every value named in Part F is set here.

The **Backup** tab gains: the staleness banner · **Back up now** · a per-target
last-run table · **Restore** (upload a zip) · **Restore from Drive/GitHub** (date
picker) · **Restore photos** (generation picker) · a tail of `backup.log`.

---

## Part F — Setup walkthroughs

These become `docs/BACKUP-SETUP.md` when the feature ships, written for someone
who is not technical. Steps F.1.1 and F.1.2 are the installer's one-time job;
everything else happens in the UI.

### F.1 Google Drive

**Step 1 — install rclone** *(installer, once)*

- *Windows*: `winget install Rclone.Rclone`. Failing that, download the Windows
  zip from `https://rclone.org/downloads/`, extract it to `C:\rclone`, then
  Start → "Edit the system environment variables" → Environment Variables →
  select `Path` → Edit → New → `C:\rclone`.
- *macOS*: `brew install rclone`
- Verify with `rclone version`.

**Step 2 — connect the Google account** *(from the UI)*

1. Admin → Settings → Google Drive → **Connect**
2. The app shows a link. Open it and sign in to Google.
3. Google shows **"Google hasn't verified this app."** This is expected — rclone
   is open-source software, not a Google product. Click **Advanced**, then
   **Go to rclone (unsafe)**, then **Continue**. Nothing unsafe is happening;
   that is Google's blanket wording for any app not submitted to its review.
4. Copy the code Google returns, paste it back into the app, **Save**.
5. Press **Test connection**; expect "Connected to Google Drive".

> **Use a personal Google account, not the school one.** A school Workspace
> administrator can block third-party applications, which would revoke the token
> and stop backups with no error anyone sees.

**Step 3 — set the folder.** Settings → `stockroom` (or any name) → **Save**.

### F.2 GitHub

No software to install; the app talks to GitHub over the web.

**Step 1 — create the repository**

1. `github.com` → **+** (top right) → **New repository**
2. Name it `stockroom-backup`
3. **Select Private.** Not optional: the files contain student numbers, which are
   login credentials (B.1).
4. Leave "Add a README file" **unchecked** — Stockroom writes its own.
5. **Create repository**

**Step 2 — create an access token**

1. Your avatar → **Settings**
2. Bottom of the left sidebar → **Developer settings**
3. **Personal access tokens** → **Fine-grained tokens** → **Generate new token**
4. Token name: `stockroom-backup`
5. Expiration: **No expiration**. A token that expires stops backups on a date
   nobody wrote down. If school policy forbids that, the staleness warning in E.7
   catches it within 48 hours.
6. Repository access → **Only select repositories** → `stockroom-backup`
7. Permissions → Repository permissions → **Contents** → **Read and write**.
   Change nothing else.
8. **Generate token**, then copy it — GitHub shows it **once**.

**Step 3 — paste it in.** Admin → Settings → GitHub → repository
`your-username/stockroom-backup`, the token, **Test connection**, **Save**.

### F.3 Restoring — four routes

1. **From a date on Drive or GitHub (the normal one).** Admin → Backup →
   **Restore from Drive/GitHub** → pick a date → type `RESTORE`. Nothing is
   downloaded by hand.
2. **From a file.** Admin → Backup → **Restore** → choose a `.zip`.
3. **Photos.** Admin → Backup → **Restore photos** → pick a generation.
4. **The app will not start.** `stockroom-restore backup-2026-09-14.zip --yes`,
   documented in the `RESTORE.md` inside every zip.

### F.4 The repository README Stockroom writes

Refreshed on every push, so it always matches reality:

```markdown
# Stockroom backup

Automatic nightly backup of the media department's equipment checkout system.
**Do not edit these files by hand.**

Last backup: 2026-09-14 02:00 — 16 items, 2 accounts

## What's here
| File | What it is |
|---|---|
| `backup/inventory.csv` | Every item, readable in Excel |
| `backup/accounts.csv`  | Every user account |
| `backup/tables/*.csv`  | Raw database tables — used to restore |
| `backup/manifest.json` | Row counts + schema version, checked during restore |

## Older backups
This folder always holds the most recent night. Previous nights are in the commit
history: click **backup/** then the clock icon, or
[view all backups](../../commits/main/backup).

## How to restore
Open Stockroom → sign in as an admin → **Admin → Backup → Restore from GitHub**,
then pick a date. Nothing needs to be downloaded.

If Stockroom will not start, see `RESTORE.md` inside any backup zip.
```

---

## Part G — Verification

1. `go test ./internal/stockroom -run 'Backup|Restore|Photo|Settings|Schedul'` —
   export shape, sequences captured, schema version recorded, **`github_token`
   redacted from `app_settings.csv`**, admin gates hold, the mirror copies only
   changed files, a generation rolls at the retention boundary, scheduler
   catch-up fires.
2. Trigger a backup from the UI against the live stack. Confirm the dated folder
   holds exactly `inventory.csv`, `accounts.csv` and `backup-<date>.zip`, and that
   `inventory.csv` names the right custodian on the checked-out assets.
3. Run it twice; the second run re-copies no photos.
4. **The round-trip, which is the only real proof.** `supabase db reset`, sign in
   as the failsafe admin, restore. Confirm every row count matches the manifest,
   `assets_asset_tag_seq` resumes at **89 and not 1**, `activity_log` gains no
   spurious rows, and both seeded accounts still sign in with `password`. This
   closes `CLAUDE.md` §11's outstanding restore test, as a repeatable command
   rather than a checklist item.
5. Schema-mismatch guard: edit `manifest.json`'s `schema_version` and expect a 409
   with the database untouched.
6. Both targets enabled: confirm one commit on GitHub *and* files on Drive from a
   single run, then break one target's credentials and confirm the other still
   succeeds and the status screen names which one failed.
7. GitHub: push twice and confirm the second commit's diff is a few KB of text,
   which is the whole justification for the D.3 layout. Then restore from the
   *first* commit through the picker.
8. Staleness: backdate `.last-success.json` by five days and confirm all three
   surfaces fire — the admin warning, the non-admin warning naming the admins, and
   the panel banner.
9. **A configuration-only run-through**: from a fresh clone, set up both targets
   start to finish without opening a text editor. If any step needs one, the
   requirement in Part B is not met.

---

## Part H — Known limits

- **Photos have no off-machine copy.** Decided deliberately (Part B). A failed
  drive loses `uploads/` and its mirror together. Pointing `photo_backup_dir` at a
  second physical disk is the only mitigation, and it is a setting rather than a
  code change.
- **The in-server scheduler does not run when the server does not.** Acceptable:
  if the server is down nobody is using Stockroom either, and the boot catch-up
  covers the gap. An OS-level task remains available as a later backstop.
- **`rclone` is still an installed dependency** for the Drive target. Only GitHub
  is install-free.
