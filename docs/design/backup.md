# Backup & restore

**Status: specified, not yet built.** Everything below Part A is the design
agreed on 2026-09-14; it was built as Phase 7 (`CLAUDE.md` §11).
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
admin panel to restore it. This is mostly solved already, by accident:
`EnsureFailsafeAdmin` (`failsafe.go`) runs on **every server start** and recreates
the `.env` admin. The recovery sequence is:

1. Fresh or wiped Postgres → `supabase db reset` applies migrations, schema only
2. Start the Go server → the failsafe admin exists
3. Sign in → Admin → Backup → restore from an upload, or pick a date from Drive/GitHub

**"Mostly" is doing real work in that sentence, and the gap has to be closed
rather than glossed.** The failsafe is deliberately best-effort: per `CLAUDE.md`
§7 and the 2026-09-08 decision, unset, malformed or rejected `.env` values are
logged as a warning and the server starts *without* a failsafe admin, because a
typo in `.env` must not take the whole API down. That decision is right and stays.
But it means step 2 is conditional, and the condition is invisible until the
morning somebody needs it: a site that never filled in `ADMIN_STUDENT_NUMBER`
restores a wiped database into zero accounts and finds the admin panel — the
entire documented restore route — unreachable.

Two things close it, and both are required:

- **`cmd/restore` is the guaranteed path, not the narrow one.** It connects with
  `DATABASE_URL` and takes no session, because its trust boundary is shell access
  to the closet PC, which is already strictly more access than any account grants.
  It therefore works with zero accounts in the database, and it is what the
  disaster procedure in `F.3` leads with. It calls the same `RestoreFromZip` as
  the panel (§E.5) rather than reimplementing a restore — a second restore
  implementation used only in emergencies is a restore that has never been tested
  at the moment it is needed.
- **The app says so while it still can.** `BackupStatus` (§E.7) reports whether a
  failsafe admin is actually configured, and the backup screen warns when it is
  not: *"No failsafe admin is configured. If the database is lost you will not be
  able to sign in to restore it."* The check costs one comparison and turns a
  silent, latent unrecoverability into a visible setup defect.

### C.2 Settings move into the database

New migration, single-row table using the standard `check (id)` trick so a second
row is impossible:

```sql
create table app_settings (
  id               boolean primary key default true check (id),
  backup_dir       text,
  photo_backup_dir text,
  keep_days        integer not null default 90  check (keep_days    >= 1),
  stale_hours      integer not null default 48  check (stale_hours  >= 1),
  schedule_hour    integer not null default 2   check (schedule_hour between 0 and 23),
  drive_enabled    boolean not null default false,
  drive_remote     text,
  drive_path       text default 'stockroom',
  github_enabled   boolean not null default false,
  github_repo      text,
  github_token     text,
  archive_passphrase text,                          -- null = unencrypted (§C.5)
  photo_min_free_gb  integer not null default 5  check (photo_min_free_gb     >= 0),
  photo_max_generations integer not null default 8 check (photo_max_generations >= 1),
  updated_at       timestamptz not null default now()
);
insert into app_settings (id) values (true);
```

**Zero is valid for `schedule_hour` and `photo_min_free_gb`, and invalid for the
other three — `keep_days`, `stale_hours` and `photo_max_generations`.** The split
is not arbitrary: a zero *threshold* means "never warn me", which is a coherent
thing to ask for, while a zero *interval* or *count* means "do this every time",
which is the always-on failure those three are bounded to avoid. The settings endpoint rejects the same values the constraints do, so a bad
number is a 400 naming the field rather than a 500 from a constraint violation:

| Column | Accepts | Why the bound is there |
|---|---|---|
| `keep_days` | `>= 1` | It is a rollover *interval*. At 0 the photo mirror starts a fresh generation on every run — a full copy of every photo, nightly, which is the one behaviour the generational design exists to avoid — and local dated-folder pruning would delete the backup that had just been written. |
| `stale_hours` | `>= 1` | At 0 the last run is stale the instant it finishes, so the sign-in warning fires at every sign-in forever and the scheduler's catch-up runs on every boot. A warning that is always on is a warning nobody reads. |
| `photo_min_free_gb` | `>= 0` | A disk-headroom warning threshold (§E.2). 0 is meaningful — it turns the free-space alert off for someone who watches the disk another way — where a negative is not. |
| `photo_max_generations` | `>= 1` | A count of frozen photo generations before the alert fires. At 0 the alert is on from the first rollover and never off, which is the same always-on-warning failure as `stale_hours = 0`. |
| `schedule_hour` | `0`–`23` | An hour of the local clock, so 0 is midnight and is ordinary. Anything outside the range names a time that does not exist; without the constraint it would just never match and backups would silently never fire. |

Negative values are refused throughout by the same constraints: there is no
reading of a negative retention, staleness threshold, generation count or disk
headroom.

`.env` becomes a **bootstrap fallback only**: on first start, a null column whose
matching environment variable is set is seeded from it. That preserves today's
behaviour and lets the installer pre-fill, without making the environment the
source of truth.

> **The token must not end up in the backup.** `app_settings` lives in the
> `public` schema, so `publicTables` picks it up — meaning `github_token` would be
> written into `app_settings.csv` and pushed to the very repository it grants
> write access to. GitHub's secret scanning would spot the `github_pat_` prefix
> and revoke it, silently killing backups a few hours after the first successful
> push. **The export nulls `github_token`, `archive_passphrase`, and any secret
> column added later, on the way out.** Restore leaves the live value alone rather
> than overwriting it with the null it finds. `archive_passphrase` is on that list
> for a blunter reason than the token: a passphrase written inside the archive it
> encrypts protects nothing at all.

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

### C.5 Optional archive encryption

Off by default, and specified here because the risk it addresses is real even
though the default is a deliberate decision rather than an oversight.

What leaves the machine is a credential file. `accounts.csv` carries student
numbers, which sign their owner in by scan with no password (§B.1), and
`password_hash`, which is bcrypt — one-way, so nobody reads a password out of it,
but offline-crackable at leisure by anyone holding the file, which a private
repository or an unshared Drive account is exactly one compromised account away
from being. The project's owner was shown this and accepted it for a
localhost-only school deployment; the access controls on the target are the
control, and they are load-bearing rather than defence in depth.

For a site that needs more than that, setting `archive_passphrase` in the admin
panel encrypts the zip with AES-256-GCM, the key derived with `scrypt`, before it
reaches any target. Restore prompts for the passphrase. Two consequences to state
plainly rather than bury:

- **The passphrase must be stored outside both targets** — a password manager, a
  sealed envelope in the department office. Written in a file next to the backup
  it protects, it is decoration.
- **It trades a confidentiality risk for an availability risk.** A lost passphrase
  is a lost backup, with no recovery path, where the unencrypted default always
  restores. That trade is why it is opt-in: for this deployment, losing the backup
  is the likelier and worse outcome.

Omitting `password_hash` instead was considered and rejected. It would force a
password reset for every account after a restore while protecting the *weaker*
credential in the file — the student number sitting beside it is a working
password-free login, so the archive stays exactly as sensitive and the restore
gets worse. Encrypt the whole archive or accept the whole archive; there is no
useful position between them.

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
`manifest.json` (row counts, `ran_at`, `schema_version`, and a **SHA-256 per
file**) · `inventory.csv` · `accounts.csv` · `RESTORE.md`.

The digests are what make a row count mean anything. A zip that came back down
from Drive or was rebuilt from a GitHub tarball can be truncated or corrupted in
transit, and a CSV truncated on a line boundary loads without complaint — the
count is simply lower, so it is caught, but a CSV truncated mid-quoted-field, or
one whose bytes were mangled while the line count survived, is not. Each file's
digest is checked as it is read out of the zip, **before** anything is loaded, so
a damaged archive is refused without the database being touched at all.

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

**The history is bounded, and pruning it is a real operation rather than an
overwrite.** Overwriting `backup/accounts.csv` does not remove last month's copy
— it adds a commit, and every prior version stays reachable forever. That matters
here more than it would in most repositories: `accounts.csv` is a credential file
(§B.1), so a student who leaves and is deleted from the database is still a
working scan-login in every commit made before the deletion. "Deleted" that does
not propagate is not deleted.

So the GitHub target keeps a **retention window of `keep_days`** over its history,
the same knob that governs the local folders and the photo mirror, and enforces it
by **rewriting the branch to a fresh root**: build an orphan commit containing only
the current `backup/` tree, force-update `refs/heads/main` to it, and every older
commit becomes unreachable. It runs on the first backup after the window elapses,
not nightly, so the picker keeps a useful span of dates.

Two honest caveats, both of which belong in the operator's head and in `F.2`:

- Unreachable is not immediately erased. GitHub garbage-collects unreachable
  objects on its own schedule, and until it does they remain fetchable by SHA to
  anyone who already knows one. Deleting and recreating the repository is the only
  way to be *certain*, and it is the documented procedure when an account has to
  be scrubbed rather than merely aged out.
- A force-update is destructive by design. It is the one write the app makes that
  cannot be undone, which is why it is bounded by a setting, logged like a backup
  run, and never triggered by anything but the retention clock.

---

## Part E — Implementation

### E.1 `backup.go` — extend the export

Additions go inside the existing snapshot transaction so every artifact describes
one instant. The staging-and-rename logic is untouched.

- **`sequences.csv`** — `last_value` and `is_called` from each sequence relation
  directly (`select last_value, is_called from <seq>`), joined with `increment_by`,
  `min_value`, `max_value` and `cycle` from `pg_sequences`. Neither source alone is
  enough: `pg_sequences` has the shape but not `is_called`, and the relation has
  `is_called` but not the shape. Closes gap 3.

  Both halves are load-bearing, and `pg_sequences` cannot supply them. Verified on
  the live stack: for a sequence that has never been read, `pg_sequences.last_value`
  is **null** while the relation itself holds `last_value = <start>, is_called =
  false`, meaning the next `nextval` returns `start` — so an export reading
  `pg_sequences` records nothing at all for an unused sequence and a restore has
  nothing to write. `is_called` then decides what the recorded number *means*:
  `setval(seq, n)` defaults to `is_called = true`, so `nextval` returns `n + 1`.
  Replaying a captured `is_called = false` sequence with the default therefore
  burns its first value — measured, not theorised: `setval('s', 5, true)` on a
  fresh `start 5` sequence yields `nextval` = 6, skipping 5.

  Skipping is survivable for an ascending sequence behind a unique column, since
  it only ever moves forward, but it is a silent off-by-one in exactly the piece
  of state gap 3 is about. Recording the flag and replaying it as
  `setval(name, last_value, is_called)` costs one boolean and removes the question.
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
- **A cross-process lock**, held for the lifetime of the run: `select
  pg_try_advisory_lock(<constant key>)` on its own dedicated connection, released
  by `pg_advisory_unlock` (and, whatever happens, by that connection closing).
  `false` means another process is mid-backup, which is a skip, not an error.
  Closes gap 8.

  Not a lock file with a timeout. `backup_dir/.lock` broken after 30 minutes was
  the obvious design and it is wrong in both directions: a backup slower than the
  timeout — a first photo mirror, a slow uplink, a stalled `rclone` — has its lock
  torn out from under it while it is still running, so a second process starts
  writing the same dated directory and pushing to the same target, which is the
  exact race the lock exists to stop. Go the other way and raise the timeout, and
  a process killed mid-run leaves a lock nothing collects, so backups stop until
  somebody deletes a file they have never heard of. A Postgres advisory lock has
  no timeout to get wrong: it lives with the connection, so it survives a run of
  any length and a crashed or `kill -9`'d process drops it the moment the socket
  closes.

  Scope worth naming: the lock is held in the database, so it excludes anything
  else talking to that database — the server's scheduler, a second server, a
  `POST /admin/backup` arriving mid-run, `cmd/restore`. It does not exclude two
  servers on two different databases pointed at one `backup_dir`, which is not a
  configuration this system has.

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
6. **Measure the mirror and the free space on its disk, and report both.** Step 4
   means the mirror only ever grows: a frozen generation is a full copy, one
   arrives every `keep_days`, and nothing removes any of them. Left unbounded that
   ends one way — the disk fills, and because photos are mirrored in the same run
   that writes the database backup, the thing that breaks is *the backup*, at the
   moment its output stops fitting. A backup system whose failure mode is silently
   not backing up is the failure this whole phase exists to remove.

`MirrorPhotos` therefore carries a bound, and the bound is an **alert, not an
automatic purge**: two settings, `photo_min_free_gb` (default 5) and
`photo_max_generations` (default 8, which is two years at the default
`keep_days`), each of which raises a warning on the same three surfaces staleness
uses (§E.7) when crossed. Nothing is deleted for the reason step 4 gives — an
automatic purge of the only copy of a deleted photo defeats the mirror — so the
remedy is a human deleting a named generation, which the backup screen lists with
its size and a delete button. The bound applies to *photo generations only*; the
local dated database folders are pruned automatically past `keep_days` (§D.1),
because those are redundant with the off-site copies and photo generations are
not.

`RestorePhotos(ctx, actor, generation)` copies a chosen generation back into
`uploads/`, preserving anything it would overwrite.

### E.3 `target_drive.go`

`rclone copy <backup_dir> <remote>:<path> --exclude *.log`, with
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

1. `RequireAdmin`, which the CLI satisfies with a **trusted local actor** rather
   than by being exempt from the check — see below.
2. Read `manifest.json`; compare `schema_version` to live. A mismatch is
   `ErrConflict` unless `opts.Force`.
3. **Verify every file's SHA-256 against the manifest before opening the
   transaction.** A mismatch is `ErrInvalid` naming the file. Doing this first
   means a corrupted download costs nothing — no truncate, no transaction, no
   lock — and it is the only check that can distinguish "this archive is damaged"
   from "this archive is fine and your data really did change".
4. One transaction, opening with `set local session_replication_role = replica`.
   **That single line closes gaps 4 and 5**: foreign keys are not checked, so load
   order stops mattering, and `trg_asset_status_log` stays quiet, so
   `activity_log` restores clean instead of gaining junk rows.
5. `truncate` all 12 tables, then `pgx.CopyFrom` each `tables/*.csv`, keeping the
   row count `CopyFrom` returns for each.
6. **Verify, still inside the transaction and before `Commit` — and before any
   sequence is written.** On any failure return an error, which leaves the
   deferred `tx.Rollback()` to put every *table* back as it was. Verifying after a
   commit would be a report, not a guard — there would be nothing left to roll
   back to, and the half-restored database would already be the live one. Three
   checks, all of which must pass:

   a. **Row counts** per table against `manifest.json`.

   b. **Referential integrity.** Step 4 turned foreign-key enforcement *off*, so
      nothing on the way in rejected an `assets` row pointing at a `category_id`
      that is not in `categories.csv`. Left unchecked, replica mode converts a
      clean failure into a database that is quietly and permanently inconsistent
      — which is worse than the load order problem it was turned on to solve. So
      reset `session_replication_role` to `origin` inside the same transaction
      and run one anti-join per foreign key, enumerated from `pg_constraint where
      contype = 'f'` rather than hand-listed, so a future migration's FK is
      covered without anyone remembering to add it here.

   c. **Sequences past their tables**, checked against the values *in the archive*
      rather than against anything already written, because nothing has been
      written yet (step 7). The invariant is on the **effective next value**:

      ```
      next = is_called ? last_value + increment_by : last_value
      ```

      which for an ascending sequence must be **strictly greater** than the
      maximum value now in the column that owns it, or the next insert collides on
      a unique index. Two details that a naive `last_value + 1` gets wrong:

      - **`is_called` decides whether to add anything at all.** Comparing the raw
        `last_value` is wrong in the `is_called = false` case, which is exactly the
        case the flag exists to distinguish.
      - **The step is `increment_by`, not 1.** `pg_sequences` carries
        `increment_by`, `min_value`, `max_value` and `cycle`, so the real number is
        available for the cost of reading it. A **descending** sequence
        (`increment_by < 0`) inverts the comparison — its next value must be
        strictly *less* than the column's minimum — and a `cycle` sequence has no
        such invariant at all, since it is permitted to wrap onto values already
        in the table.

      Nothing in this schema is descending or cycling: the one sequence is
      `assets_asset_tag_seq`, ascending by 1. So rather than write and never
      exercise two more branches, the restore **captures the metadata and refuses
      what it cannot reason about** — a descending or cycling sequence in the
      archive is an explicit `ErrInvalid` naming it, not a silently skipped check.
      A refusal is recoverable; a check that quietly passes on a sequence it was
      never designed for is the bug this whole step exists to prevent.

      An empty table has no maximum and so constrains nothing; that is a pass, not
      a skip to be confused with a missing check. This is gap 3 caught a second
      time, at the point where it would actually bite.

   Trigger-derived state needs no separate check: the triggers suspended in step 4
   are `trg_assets_updated_at` and `trg_asset_status_log`, and both are silenced
   deliberately because `activity_log` and `updated_at` are *restored from the
   backup* rather than regenerated. Check (a) already covers `activity_log`.

7. **Only now, every check having passed, write the sequences** —
   `setval(name, last_value, is_called)`, replaying the captured flag rather than
   defaulting it. A sequence absent from `sequences.csv` (one added by a migration
   after the backup was taken) is left at its declared start rather than guessed
   at.

   **Sequences are written last because `setval` is not transactional, and this is
   the one place the "just roll back" story does not hold.** Verified against the
   live stack:

   ```sql
   begin; select setval('rb_probe', 500, true); rollback;
   select last_value from rb_probe;   -- 500. The rollback did not undo it.
   ```

   So the ordering is load-bearing rather than tidiness: had the sequences been
   written before the checks, a validation failure would roll the *tables* back to
   their original contents while leaving every sequence advanced to the archive's
   values — a database that looks untouched and silently hands out colliding keys.
   Doing it after the checks shrinks the exposure to the gap between step 7 and the
   commit.

   That gap is not zero, so it is compensated rather than ignored: the original
   `last_value`/`is_called` of every sequence is read **before** step 7 and, if the
   commit fails, replayed to put them back. The compensation is best-effort by
   nature — it is itself non-transactional — so a failure to compensate is logged
   loudly and named in the error, because a restore that reports success while
   leaving sequences in a third state is worse than one that reports what happened.

8. Commit, then `db.Sessions.Clear()` — every existing token now points at a
   profile that may no longer exist.

**What "rolled back" precisely means here.** For the 12 tables it is exact: the
transaction aborts and their contents are what they were. For sequences it is not
the transaction doing the work but the compensation above, which is why the
sequence writes are ordered after every check that can trigger a rollback. Saying
the database is left "exactly as it was" without that step would be a claim
Postgres does not support.

Restoring is destructive, so the request carries `confirm=RESTORE`.

**The CLI actor.** `RestoreFromZip` gates on `RequireAdmin`, and `cmd/restore`
exists precisely for the case where there are no accounts to be an admin of (§C.1).
Left as written those two are contradictory, and the tempting resolutions are both
wrong: dropping the gate would open the HTTP path, and giving the CLI a second
restore implementation that skips it recreates the untested-emergency-code problem
§C.1 rejects.

Instead the package exports one constructor:

```go
// LocalCLIActor is the actor for a process that already has shell access to the
// machine and the database URL — strictly more access than any account grants,
// so there is nothing left for an authorization check to protect.
//
// This function is exported and any package may call it; what no other package
// can do is produce a trustedCLI Actor *any other way*. The field is unexported,
// so an Actor literal naming it does not compile outside internal/stockroom, and
// no HTTP handler, JSON body or session lookup can set it. Calling this is a
// deliberate act by code already running on the machine; forging it is not
// possible.
func LocalCLIActor() Actor { return Actor{ID: "cli", IsAdmin: true, trustedCLI: true} }
```

The unexported field is the whole mechanism, and the guarantee is worth stating
precisely: *this constructor is callable from anywhere; `trustedCLI` is settable
from nowhere else.* `Actor` is built from a session by `Resolve`, which never sets
it, so a forged request cannot reach this state no matter what it sends. `RequireAdmin` needs no change: the actor is an admin.
`trustedCLI` exists to keep the two apart **in the log** — a restore records
whether it came from `cli` or from a named admin's ID, because "who restored the
database" is the first question anyone asks afterwards.

Verification owns this too: Part G step 10 restores with zero rows in `profiles`, which is
the only test that exercises the path the CLI exists for.

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
`{per_target: {last_success, age, last_error}, stale, worst_age, failsafe_admin_configured, photo_mirror: {bytes, free_bytes, generations}}`.

Three conditions raise a warning, not one. Staleness is the loudest, but a backup
system can also fail by filling its disk or by leaving nobody able to sign in and
use it, and both of those are silent until the day they matter:

- **`LoginResult` and `MeResult` gain `BackupWarning *BackupWarning`.** Both
  already carry `HasOverdue` for exactly this shape (`auth.go:49`, `auth.go:171`),
  so this follows an established pattern rather than inventing one.
- **Admins** see `Backups have not run in 12 days. Open Admin → Backup.`
- **Non-admins** see `Backups have not run in 12 days — please tell Admin Admin or
  Jane Smith.` Admin **names only**, never student numbers, consistent with
  `CLAUDE.md` §7's custodian-visibility rule.
- **The backup screen** shows a banner with the age, per-target status and last
  error.
- **No failsafe admin configured** (§C.1) warns on the backup screen:
  *"No failsafe admin is configured. If the database is lost you will not be able
  to sign in to restore it."* Admin-facing only — it names a `.env` fix no student
  can act on.
- **The photo mirror is near its bound** (§E.2) warns on the backup screen with
  the mirror's size, the disk's free space and the generation list, each
  generation carrying its size and a delete button. Nothing is deleted
  automatically.

### E.8 Endpoints

| Route | Who | Notes |
|---|---|---|
| `GET/PUT /admin/settings` | admin | the settings screen; `github_token` returns masked |
| `POST /admin/settings/test` | admin | `{target}` → runs `Test()`, returns a readable pass/fail |
| `POST /admin/google/connect` | admin | starts `rclone authorize`, returns the URL (was `/admin/drive/connect` until 2026-09-25) |
| `POST /admin/google/finish` | admin | `done: false` until Google calls back, then writes the remote; a pasted token also works |
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
4. **The app will not start, or the database came back with no accounts in it.**
   `stockroom-restore backup-2026-09-14.zip --yes`, documented in the `RESTORE.md`
   inside every zip. This is the route that needs no session and therefore no
   surviving account, so it is the one that works when routes 1 to 3 cannot be
   reached at all (§C.1). It calls the same `RestoreFromZip`, with the same
   checksum, row-count and referential-integrity gates, so it is not a second,
   less-tested restore.

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
   `assets_asset_tag_seq` resumes at **the value the backup captured and not at
   1** (read it before backing up rather than hard-coding a number — it was 89
   when this was written and 130 a day later), `activity_log` gains no spurious
   rows, and both seeded accounts still sign in with `password`. This closes
   `CLAUDE.md` §11's outstanding restore test, as a repeatable command rather than
   a checklist item.
   Cover **both sequence states and both table states**, since `is_called` is
   exactly what distinguishes them: a used sequence over a populated table, and a
   freshly-created sequence that has never been read (`is_called = false`,
   `pg_sequences.last_value` null) over an empty one. After restoring the second,
   `nextval` must return the sequence's start value, not start + 1 — that
   off-by-one is what replaying the flag prevents, and it is invisible to a row
   count.
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
9. **Corruption guards, each proved by breaking one thing.** Flip a byte inside a
   `tables/*.csv` and expect a checksum failure naming the file, with no
   transaction ever opened. Separately, hand-edit `categories.csv` to drop a row
   that `assets.csv` references and expect the referential-integrity check to fail
   the restore and roll back — that one matters most, because it is the failure
   replica mode is specifically unable to catch on its own. Truncate a file
   mid-quoted-field and confirm the checksum catches what the row count cannot.
10. **Bootstrap, the zero-account recovery test.** Order matters here, because
    two of the assertions need an account and the rest need there to be none:

    a. Blank `ADMIN_STUDENT_NUMBER` in `.env`, then `supabase db reset` — which
       reseeds, so the seeded admin still exists.
    b. **Sign in as the seeded admin and confirm the backup screen's "no failsafe
       admin" warning fires.** This has to happen now: the warning lives on an
       admin-only screen, so once the accounts are gone there is no way to
       observe it at all.
    c. `truncate profiles`, leaving **no accounts**.
    d. Confirm every HTTP restore route is refused — there is no session to be
       had.
    e. Confirm `cmd/restore` nonetheless restores the zip end to end through
       `LocalCLIActor` and `RestoreFromZip`, after which the backed-up accounts
       sign in again.

    Step (e) is the one test that covers the path `cmd/restore` exists for;
    without it the CLI is code first exercised during an actual disaster.

    Test the **exported API**, not an imagined inaccessibility of it:
    `LocalCLIActor()` is exported and any package may call it, so assert that
    calling it yields an actor `RequireAdmin` accepts, and that `Resolve` returns
    an actor with `trustedCLI` **unset** for every session including an admin's —
    that second assertion is the one carrying the security claim. That an external
    `Actor` literal cannot name `trustedCLI` is a compile-time fact and cannot be
    asserted at runtime; record it with a commented snippet, not a test that
    pretends to check it.
11. **Retention**: backdate the GitHub history past `keep_days` and confirm the
    purge leaves a single root commit holding the current backup, that the picker
    still works afterwards, and that the pruned commits are gone from the list.
    Cross a photo bound and confirm the alert fires and that **nothing was
    deleted**.
12. **A configuration-only run-through**: from a fresh clone, set up both targets
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
- **A backup that leaves the machine is a credential file that leaves the
  machine.** See §B's privacy note: `accounts.csv` carries student numbers, which
  sign their owner in by scan with no password, and `password_hash`, which is
  bcrypt and therefore offline-crackable at leisure by anyone who obtains the
  file. Private repository and unshared Drive account are load-bearing, not
  hygiene. §C.5 specifies optional archive encryption for sites that need the
  stronger guarantee.
- **Frozen photo generations are never deleted automatically**, by decision, so
  the mirror only grows. §E.2 bounds this with a disk-headroom alert rather than
  an automatic purge — the alert is what keeps "never deletes" from meaning
  "silently fills the disk and stops backing up".
