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
STOCKROOM_REQUIRE_DB=1 go test ./... -count=1  # fail, rather than skip, when Postgres is down
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

## What runs

The rule for the Go suites is one happy path per module plus, where the module has one, the admin-only or forbidden gate. The gate tests exist because every permission rule is enforced inside `internal/stockroom`, not the router, and a refactor there is the most likely way to open one without noticing.

### `internal/stockroom` (24 tests)

| File | Tests | Covers |
|---|---|---|
| `auth_test.go` | 3 | scan login with no password is a limited session until one is set; typed login; `RequireAdmin` |
| `users_test.go` | 2 | user functions refuse a student; create, get, list |
| `roster_test.go` | 2 | import refuses a student; one CSV with good and bad rows, a photo, a BOM and CRLF line endings |
| `assets_admin_test.go` | 2 | every asset write refuses a student; `CreateAsset` |
| `categories_admin_test.go` | 2 | every category write refuses a student; `CreateCategory` |
| `custody_test.go` | 4 | checkout to self; a student cannot check out to someone else; scanning a checked-out item returns it; the custody lists refuse a student |
| `backup_test.go` | 2 | backup refuses a student; the export writes every table with a header and a second run replaces the day's folder |
| `failsafe_test.go` | 1 | the failsafe admin is created, then updated in place |
| `sessions_test.go` | 1 | create and get |
| `config_test.go` | 1 | defaults, including the 10-minute idle timeout |
| `db_test.go` | 1 | open, ping, close |
| `password_test.go` | 1 | hash and check |
| `hashcost_test.go` | 1 | the shipped bcrypt cost stays at bcrypt.DefaultCost (10); the suite runs at the minimum cost and this is what stops that leaking into a build |
| `pgerr_test.go` | 1 | Postgres error codes map to `ErrConflict` / `ErrInvalid` / `ErrNotFound` |

`main_test.go` lowers the bcrypt cost for the whole package. `testdb_test.go` holds the fixtures: a test profile, a test asset, an open custody row, all deleted at cleanup.

### `server` (6 tests)

| File | Tests | Covers |
|---|---|---|
| `router_test.go` | 1 | `GET /health` |
| `json_test.go` | 1 | each sentinel error becomes the right status, and `ErrNotConfigured` is a 503 with its message intact |
| `auth_test.go` | 1 | scan in with no password, be refused on a full-only route, set a password, be a normal session |
| `custody_test.go` | 1 | `POST /checkout` round trip |
| `admin_test.go` | 2 | the asset admin routes over HTTP; every admin route answers 403 to a student |

### Database (3 pgTAP files)

| File | Covers |
|---|---|
| `010_structure` | every table, view, enum, index and trigger the Go row structs scan against |
| `050_views` | `active_custody` and `overdue_custody`, which the sign-in warning and the checkout block both read |
| `080_seed` | `seed.sql` loads: the category tree from `Catagories.md`, the two accounts, an open custody row behind every checked-out asset |

### Frontends

Replaced wholesale in Phase 6, as planned: `db.ts` and the supabase-js admin screen it covered are
deleted, and the 92 Vitest cases over them went with the code. What is left is two smoke tests per host
(4 total), each proving that `@stockroom/ui` resolves and compiles under *that* app's Vite config and
reaches the sign-in screen without touching the network.

That is deliberately thin, and the reason is the same one that put every screen in one package: the two
hosts render the same component, so testing behaviour in both would be testing it twice. The behaviour
tests belong in `packages/ui`.

### `packages/ui` (17 tests)

`keep-alive.test.ts`, covering `attachKeepAlive`: it pings when someone has interacted and the connection
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

## Planned

Everything below existed on 2026-09-12 and was deleted, not disabled. `git log -- <file>` has each one if it is wanted back. The suite had grown to 237 Go cases and 297 pgTAP assertions for a backend that is still being reshaped. This branch alone folded `Auth` into `DB`, merged two category structures and changed six signatures, and every reshaping cost more in test edits than in code. The plan is to bring these back once the frontend is wired and the API stops moving.

**Edge and refusal cases, per module.** Unknown student number, bad credentials, blank hash, resolve reflecting a profile change, `Me` reporting overdue. User conflicts, update, delete refusals, sessions dropped on reset and delete. Roster: malformed file, blank photo keeps the old one, photo extensions, absolute paths, per-line parse errors, missing `UPLOADS_DIR`, a photo path escaping the uploads dir. Assets: create rejects, update leaves custody alone, delete and status refusals, photo upload and replacement. Categories: update (rename, move, renumber, depth and cycle refusals), delete refusals, edits showing up in the tree. Custody: cart deduplication, admin choosing a custodian, unknown custodian, due-date bounds, the overdue block and its admin override, the whole cart failing together, unknown asset, limited session refusals, check-in without a note, twice, on an unknown asset, check-in and scan following the open row rather than the status column, scan opening detail, unknown serial, the list contents, both history reads. Backup without a `BACKUP_DIR`. Failsafe rejecting bad config and running without any. Config: env overrides, `.env` discovery, precedence, bad idle minutes. `Open` rejecting a bad URL or an unreachable database.

**Race tests.** `sessions_concurrent_test.go` (concurrent create, mixed operations, one-way upgrade) and the photo lock test that ran two uploads of the same photo together. CI ran the Go suite under `-race` for these and no longer does, so the run is cheaper. The bcrypt cost knob in `main_test.go` still applies and still matters. Nothing now exercises the `SessionStore` mutex or the `photoStore` lock under contention, so a data race there would reach a release; bring `-race` back with these tests.

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
