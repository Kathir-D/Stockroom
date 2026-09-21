# Testing

The suite was cut down on 2026-09-13 to the smallest set that still catches a broken build, a broken database, and a permission gate that has quietly opened. What was cut is listed under [Planned](#planned), with the reason. `CI.md` covers how the suite runs in GitHub Actions.

Run everything with:

```bash
./scripts/dev.sh test
```

`.githooks/pre-commit` runs that same script before a commit is created, so the suite is the default rather than something you remember. `scripts/dev.sh deps` installs it by pointing `core.hooksPath` at `.githooks`. Docs-only commits skip it; `git commit --no-verify` or `STOCKROOM_SKIP_TESTS=1` skip it deliberately. See `CI.md`, "Pre-commit".

Or one layer at a time:

```bash
supabase start            # the Go and pgTAP suites need Postgres up
./scripts/dev.sh deps  # npm workspace + Go modules; ./scripts/dev.sh test runs this for you
go vet ./...
STOCKROOM_REQUIRE_DB=1 go test ./... -count=1 -p 1  # fail, rather than skip, when Postgres is down
supabase test db
npm run check             # svelte-check over packages/ui and both hosts
npm test                  # both hosts' Vitest suites
npm run build             # both hosts; catches what type-checking alone doesn't
```

Run every npm command from the **repo root**. It is a workspace: `npm install` or
`npm test --prefix desktop-app/frontend` creates a second copy of Svelte and Vite under that
directory, and the failure mode is silent — components render but their state never updates
(`docs/design/design-system.md` §2.2).

Go's database-backed tests skip themselves when Postgres is unreachable, so `go test ./...` still runs on a machine without Docker. `scripts/dev.sh test` and CI set `STOCKROOM_REQUIRE_DB=1` so a skip can never pass for a success.

**`-p 1` is not optional.** `go test ./...` runs packages concurrently, and `internal/stockroom` and `server` talk to the same live Postgres. That was merely untidy until the restore tests, whose whole job is to truncate every table and load a backup over the top: run alongside anything else, a neighbouring package's rows disappear mid-test and the failure surfaces on whichever test happened to be running rather than on the one that caused it. `dev.sh test`, `dev.ps1` and CI all pass it; a bare `go test ./...` is the flaky way to run this suite.

## What runs

The rule for the Go suites is one happy path per module plus, where the module has one, the admin-only or forbidden gate. The gate tests exist because every permission rule is enforced inside `internal/stockroom`, not the router, and a refactor there is the most likely way to open one without noticing.

### `internal/stockroom` (96 tests)

| File | Tests | Covers |
|---|---|---|
| `auth_test.go` | 3 | scan login with no password is a limited session until one is set; typed login; `RequireAdmin` |
| `users_test.go` | 2 | user functions refuse a student; create, get, list |
| `roster_test.go` | 3 | import refuses a student; one CSV with good and bad rows, a photo, a BOM and CRLF line endings; **`openFor` accepts only the photo extensions the upload path does**, because the copy it opens is published by `/files/` under a Content-Type the extension chose |
| `assets_admin_test.go` | 2 | every asset write refuses a student; `CreateAsset` |
| `categories_admin_test.go` | 2 | every category write refuses a student; `CreateCategory` |
| `custody_test.go` | 5 | checkout to self; a student cannot check out to someone else; scanning a checked-out item returns it; the custody lists refuse a student; a damage note lands on a **closed** event, and an open one is a conflict |
| `kits_test.go` | 4 | every kit write refuses a student while both reads answer; build a kit, read it back and take a unit out; a unit already in a kit is refused **naming that kit**; a kit return checks in only what is out and reports the rest, rather than failing because one unit was already back |
| `backup_test.go` | 5 | backup refuses a student; a run writes a restorable archive; `github_token` is redacted out of the export; a second concurrent run is a **skip**, not an error; no folder configured is `ErrNotConfigured` |
| `archive_test.go` | 3 | the encryption round-trip; a wrong passphrase is refused; an encrypted archive asks for one |
| `restore_test.go` | 9 | restore refuses a student; refuses without the typed confirmation; **a real truncate-and-reload round-trip against the live database**; a damaged archive is caught by its checksum; an archive whose rows point at absent parents is caught by the FK anti-join; `LocalCLIActor` satisfies `RequireAdmin` and `Resolve` never sets `trustedCLI`; `last_value`/`is_called` survive a round-trip (including the never-read, `is_called = false` case, where replaying with `setval`'s default would burn the first value); a descending or cycling sequence is refused outright rather than checked by a rule never designed for one; **a restore that had to be forced past the schema-version check says so on its report**, which is otherwise indistinguishable from one that needed no override |
| `settings_test.go` | 7 | settings are admin-only; a secret never comes back over the API; each bound is a readable message rather than a Postgres constraint name; a target cannot be enabled half-configured; `EnsureSettings` seeds a blank column **once**, so neither a value an admin typed nor one an admin cleared is taken back by a restart; **a failed connection test carries its reason** — "Test connection" exists to name a misconfiguration, and without a sentinel a wrong token answered 500 "internal error" while the log held "github 401 Unauthorized: Bad credentials"; **a backup folder must be a full path**, because a path that lost its leading separator on the way out of a Finder window is resolved against the server's working directory and produces a complete, valid backup somewhere nobody will ever look |
| `backup_status_test.go` | 6 | staleness is computed over the targets that are supposed to be running; **a target that has never succeeded is worded differently from one whose last success is old**, because the local archive always succeeds and so a misconfigured off-site target used to read as "Backups have not run in 0 hours" on every surface including every student's sign-in; the wording's five shapes; the no-failsafe-admin warning fires; the state file and log are written; dated folders are pruned |
| `photos_backup_test.go` | 4 | the mirror copies only what changed; a missing `uploads/` is a no-op rather than an error; a generation rolls over at the retention boundary; the live generation cannot be deleted |
| `photowall_test.go` | 11 | the wall is wiped at boot and adopts only a directory it owns, refusing a foreign one; the fill/take/invalidate lifecycle; a take is atomic and answers when empty; filling backs off; a stale generation is discarded; tile names give nothing away; the run loop stops with its context |
| `photowall_image_test.go` | 8 | a 3000×2000 JPEG comes out exactly 900×600; the ratio gate accepts 4:3 and 16:9 and turns away squares, panoramas and portraits; **EXIF orientation is applied before the gate**, so the same bytes are accepted untagged and rejected tagged orientation 6, and a stored portrait tagged 6 comes out actually turned; GPS and every other tag are gone from the tile; rubbish is rejected as unusable rather than as a fault; an unreadable orientation tag reads as upright rather than failing; **a pixel count over the ceiling is refused off the header**, since the byte ceiling bounds what arrives and not what it decodes to |
| `photowall_drive_test.go` | 13 | the manifest keeps only what the wall can show, at the size ceiling, and is written through; a failed listing leaves the working manifest alone and backs off; the manifest survives a restart and is dropped when it names a folder that is no longer live; `NextPhoto` retries past unusable files, gives up after four, and says *why* it has nothing; `SetFolder` discards the manifest and asks for a rebuild; a listing that lands after a folder switch is thrown away; an empty folder is reported as a sentence; every rclone command names its folder; the refresher stops with its context; the listing counts as it streams; **the manifest is not in the directory the tile route serves**, because it is a listing of the Drive folder and `http.FileServer` serves any file in a directory by name |
| `failsafe_test.go` | 1 | the failsafe admin is created, then updated in place |
| `sessions_test.go` | 1 | create and get |
| `config_test.go` | 3 | defaults, including the 10-minute idle timeout; the photo-wall defaults; a bad photo-wall number is refused rather than silently defaulted |
| `db_test.go` | 1 | open, ping, close |
| `password_test.go` | 1 | hash and check |
| `hashcost_test.go` | 1 | the shipped bcrypt cost stays at bcrypt.DefaultCost (10); the suite runs at the minimum cost and this is what stops that leaking into a build |
| `pgerr_test.go` | 1 | Postgres error codes map to `ErrConflict` / `ErrInvalid` / `ErrNotFound` |

`main_test.go` lowers the bcrypt cost for the whole package. `testdb_test.go` holds the fixtures: a test profile, a test asset, an open custody row, all deleted at cleanup.

### `server` (18 tests)

| File | Tests | Covers |
|---|---|---|
| `router_test.go` | 3 | `GET /health`; the CORS origin allow-list; the blocked-origin log is bounded, so a hostile page cannot grow it without limit |
| `json_test.go` | 1 | each sentinel error becomes the right status, and `ErrNotConfigured` is a 503 with its message intact |
| `auth_test.go` | 1 | scan in with no password, be refused on a full-only route, set a password, be a normal session |
| `custody_test.go` | 1 | `POST /checkout` round trip |
| `kits_test.go` | 2 | the kit round trip over HTTP — build, check the units out through the ordinary `POST /checkout`, return the kit in one press; every kit *write* answers 403 to a student while both reads and the return answer 200 |
| `admin_test.go` | 2 | the asset admin routes over HTTP; every admin route answers 403 to a student |
| `backup_test.go` | 3 | every backup, settings, restore and photo route answers 403 to a student; the settings and status routes over HTTP; **back up and then restore over HTTP**, the same path the admin panel takes |
| `photowall_test.go` | 5 | the sign-in photo wall's two routes: `GET /signin/photos` answers **200 with `[]`** when no wall was built, which is the common case and not an error; it is reachable with no session and no cookie, the only route on the server that is; the batch-then-fetch round trip, so the endpoint's URLs and the static mount's paths cannot disagree unnoticed; **`manifest.json`, the marker, an escaped `..%2F` and the bare directory are all unreachable** through the tile route, because the manifest is a listing of the Drive folder and `http.FileServer` serves any named file in a directory; `/files/` still works after `fileServer` took a prefix parameter |

### Database (4 pgTAP files)

| File | Covers |
|---|---|
| `010_structure` | every table, view, enum, index and trigger the Go row structs scan against, including the two kit indexes that hold the rules Go would otherwise be the only keeper of |
| `050_views` | `active_custody` and `overdue_custody`, which the sign-in warning and the checkout block both read |
| `080_seed` | `seed.sql` loads: the category tree from `Catagories.md`, the two accounts, an open custody row behind every checked-out asset, and the seeded kit — four units, all available, none of them in a second kit |
| `090_app_settings` | the single-row constraint, every default, and the three bounds the settings endpoint repeats in words (`keep_days >= 1`, `stale_hours >= 1`, `schedule_hour` 0–23) |

### Frontends

Replaced wholesale in Phase 6, as planned: `db.ts` and the supabase-js admin screen it covered are
deleted, and the 92 Vitest cases over them went with the code. What is left is two smoke tests per host
(4 total), each proving that `@stockroom/ui` resolves and compiles under *that* app's Vite config and
reaches the sign-in screen.

The second of each pair used to assert the app **made no request at all** before anyone signed in. The
sign-in photo wall makes one by design (`docs/design/signin-photo-wall.html` §6), so the assertion was
rewritten rather than dropped: the only call the sign-in screen makes is the wall's batch, *and* the field
still renders when that call fails — which it does in the test, exactly as it does on a machine with no
server. That is a stricter reading of §6's "never block first paint" than the original was, and it keeps
the property the original was protecting.

That is deliberately thin, and the reason is the same one that put every screen in one package: the two
hosts render the same component, so testing behaviour in both would be testing it twice. The behaviour
tests belong in `packages/ui`.

### `packages/ui` (37 tests)

`status.test.ts` (11) covers the five-status resolution and the viewer-aware custodian line.

`scanner.test.ts` (11) covers scan-vs-typed and the editing gestures that must never sign somebody in as a deleted number: a backspaced burst is typed, a chord or a caret key discards the pending burst, and a Backspace that empties the buffer resets rather than demoting the next card scan.

`kits.test.ts` (5) covers expanding a kit into cart lines: available units that are not already in the cart are added, a unit that is out blocks the whole kit and is named, status drift is read through `status.ts` rather than the column, and an empty kit is refused. The rule worth pinning is the one the screen cannot show — `checkable` was true when the list was fetched, so the press has to re-decide per unit or the cart silently accepts an item somebody else is carrying, and the failure only appears as a 409 at checkout.

`stores/kits.test.ts` (4) covers the one decision in the kits store: where a patched row lands. `patch` exists so an edit does not cost a list refetch, and that shortcut is only honest if the row ends up where a refetch would have put it — a rename is the case that separates the two.

`keep-alive.test.ts` (6), covering `attachKeepAlive`: it pings when someone has interacted and the connection
has gone quiet, and stays silent when nobody has, when requests are already flowing, when nobody is signed
in, while a ping is in flight, and after teardown.

The second of those is the one that matters and the one that caught a real bug: the first implementation
pinged on every tick when there had been *no* interaction at all, which would have kept an abandoned
closet PC signed in forever — the exact failure the idle timeout exists to prevent.

`scanner.test.ts`, covering `attachScanner`: a fast burst reads as a scan, a slow one as typed, and —
the nine that exist for a bug rather than for coverage — everything about the keystroke buffer keeping
step with what the field actually shows. The bug was that typing a student number, deleting it, and
pressing Enter signed you in as the *deleted* number, with an empty box as the only evidence anything
had happened, because the buffer only ever grew.

The first four cover Backspace and Escape moving the buffer. The other five cover the two ways the
first fix could still be walked around, both verified to fail against it: an idle Backspace on an empty
field left `fast` false forever, so the *next* card scan demanded a password from a user who has none
yet; and every editing gesture that is a chord or a caret move (Alt/Cmd+Backspace, Cmd+A then
overtype, arrow keys) bypassed the buffer entirely, leaving it fast and stale — the original bug, one
keystroke further out. The sign-in screen adds the belt to that brace: a burst only counts as a scan
when the field holds exactly what the buffer saw.

Still unwritten, and worth doing next: `groupByModel` (the browse list's shape), `resolveStatus` (the five
states and the 24h due-soon threshold), `dueInstant` (the 7-day cap's clamp), and the cart store's set
semantics. All are pure functions or plain stores with
no component and no server needed.

## Testing the backup by hand

Two scripts exist for the thing the suite cannot cover: whether a *real* target, with a real
account behind it, actually received the archive.

| Script | What it does |
|---|---|
| `scripts/backup-check.sh` | Signs in with the failsafe admin from `.env`, prints where backups are configured to go, optionally runs each target's connection check (`--test`), takes a real backup (`--run`), lists what is stored on every target (`--versions`), and verifies the newest archive. `--all` does all of it |
| `scripts/verify-archive.py` | Checks one archive the way a restore does, without a database: every SHA-256 in `manifest.json`, every table's row count, that `sequences.csv` and `RESTORE.md` are present, that each sequence carries `is_called`, and that no GitHub token survived redaction into `app_settings.csv` |

`verify-archive.py` takes any archive, so it is also how you prove a zip *fetched back* from Drive
or GitHub is byte-identical in the ways that matter. Exit status is 0 only when the archive would
pass a restore's own checks.

## Planned

Everything below existed on 2026-09-12 and was deleted, not disabled. `git log -- <file>` has each one if it is wanted back. The suite had grown to 237 Go cases and 297 pgTAP assertions for a backend that is still being reshaped. This branch alone folded `Auth` into `DB`, merged two category structures and changed six signatures, and every reshaping cost more in test edits than in code. The plan is to bring these back once the frontend is wired and the API stops moving.

**Edge and refusal cases, per module.** Unknown student number, bad credentials, blank hash, resolve reflecting a profile change, `Me` reporting overdue. User conflicts, update, delete refusals, sessions dropped on reset and delete. Roster: malformed file, blank photo keeps the old one, absolute paths, per-line parse errors, missing `UPLOADS_DIR`, a photo path escaping the uploads dir. Assets: create rejects, update leaves custody alone, delete and status refusals, photo upload and replacement. Categories: update (rename, move, renumber, depth and cycle refusals), delete refusals, edits showing up in the tree. Custody: cart deduplication, admin choosing a custodian, unknown custodian, due-date bounds, the overdue block and its admin override, the whole cart failing together, unknown asset, limited session refusals, check-in without a note, twice, on an unknown asset, check-in and scan following the open row rather than the status column, scan opening detail, unknown serial, the list contents, both history reads. Backup without a `BACKUP_DIR`. Failsafe rejecting bad config and running without any. Config: env overrides, `.env` discovery, precedence, bad idle minutes. `Open` rejecting a bad URL or an unreachable database.

**Race tests.** `sessions_concurrent_test.go` (concurrent create, mixed operations, one-way upgrade) and the photo lock test that ran two uploads of the same photo together. CI ran the Go suite under `-race` for these and no longer does, so the run is cheaper. The bcrypt cost knob in `main_test.go` still applies and still matters. Nothing now exercises the `SessionStore` mutex or the `photoStore` lock under contention, so a data race there would reach a release; bring `-race` back with these tests.

**The photo wall's component.** `photo-wall.svelte` has no test of its own. Its rules were checked in a
browser — the columns absent below 1280px, the input at the same pixel either way, `pointer-events: none`, no
node in the accessibility tree, the marquee moving at the 13.8 px/s its 110s cycle implies — and the host
smoke tests cover the one thing that could break silently in CI, which is the fetch. What is genuinely
untested is the strip builder: it pads a short column and then doubles it, and the doubling is what makes the
marquee seam invisible, so an off-by-one there is a visible jump once a cycle that nothing would catch. It is
a pure function and wants four cases; that needs `@testing-library/svelte` in `packages/ui`, which has only
logic tests today, or the function lifted out of the component.

Note that the doubling is only half of that geometry, and the half a test could reach. The other half is the
`.strip` padding, which exists so that half the strip height is exactly one cycle of the list; the two have to
agree, and a test of the builder alone would have passed throughout the period when they did not.

**Sessions.** The non-race `sessions_test.go` cases: idle timeout, zero idle never expiring, `Get` returning a copy, unique tokens, the sweep on create, upgrade then delete, `DeleteForProfile`. Also `TestSetUserPassword` and the sessions it drops.

**Pure unit files.** `errors_test.go` (sentinels distinct, messaged, surviving wrapping), `types_test.go` (JSON never leaks the password hash, nulls marshal as null), `photos_test.go` (rollback restores what it replaced, stale extensions dropped only on commit), the remaining `pgerr` and `password` tables, `TestGetenv`, `TestEnumConstantsMatchDatabase` and `TestConstraintMessagesMatchTheLiveSchema` (the Go constants against the live schema), `TestActiveCustodyFlattensEmbeddedEvent`, and `Ping` after close or with a cancelled context.

**HTTP.** `HEAD /health`, `/health` failing when the database is down, an unknown path, the three `writeError` cases (seeing through wrapping, hiding unknown detail, preferring the earliest matching case), login errors, token from header vs cookie, users CRUD, roster import over multipart and `text/csv`, wrong method on every route, the scan, check-in, list and history routes, the overdue block over HTTP, the photo upload round trip (including that `/files/` serves it with no session), the backup route with and without `BACKUP_DIR`, and the `decodeJSON` size limit and unknown-field rejection.

**pgTAP.** `020_constraints` (uniqueness, NOT NULL, every FK delete action), `030_bookings` (a table v1 does not use), `040_triggers` (`updated_at`, status-change logging), `060_search` (the GIN index and the `coalesce()` null guard), `070_privileges` (PostgREST roles, which nothing calls).

## Adding a test

Put it with the layer it covers. Go tests that touch the database call `requireTestDB` (package) or `testDeps` (server) and create their rows through the fixtures in `testdb_test.go` / `server/auth_test.go`, which clean up after themselves. pgTAP files go in `supabase/tests/NNN_topic.test.sql`, run inside a transaction that rolls back, and start with:

```sql
begin;
create extension if not exists pgtap;
select no_plan();
-- assertions
select * from finish();
rollback;
```

The workflow picks new files up automatically.
