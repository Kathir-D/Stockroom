# Testing

Four suites, one per layer. Run them all with:

```bash
./scripts/test-all.sh
```

| Layer | Tool | Location | Count |
|---|---|---|---|
| Database schema | pgTAP via `supabase test db` | `supabase/tests/*.test.sql` | 297 assertions |
| Go (config, pool, auth, sessions, users, roster, the core loop, HTTP) | `go test` | `internal/stockroom/*_test.go`, `server/*_test.go` | 237 cases |
| Desktop frontend | Vitest + Testing Library | `desktop-app/frontend/src/**/*.test.ts` | 92 cases |
| Web app | Vitest + Testing Library | `web-app/src/**/*.test.ts` | 2 cases |

Everything here tests code that exists today. Nothing in `TODO.md` Phases 5 to 8 is tested ahead of being written; see [Testing features that don't exist yet](#testing-features-that-dont-exist-yet) for how each phase gets covered when it lands.

## Running individual suites

```bash
supabase start          # required by the database suites
go test ./...           # add STOCKROOM_REQUIRE_DB=1 to fail instead of skip
go test ./... -race     # the session store is shared mutable state; CI runs this
supabase test db
npm --prefix desktop-app/frontend test
npm --prefix web-app test
```

Go's database-backed tests skip themselves when Postgres is unreachable, so `go test ./...` still works without Docker. `scripts/test-all.sh` sets `STOCKROOM_REQUIRE_DB=1` whenever the database is up, so a skip can never pass for a success. CI sets it unconditionally.

Frontend coverage report: `npm --prefix desktop-app/frontend run test:coverage`.

## What is covered

### Database (`supabase/tests/`)

The schema is the largest piece of real logic in the repo. Constraints, triggers and views enforce behaviour the Go code will depend on.

**`010_structure`.** Every table, view, enum, primary key, index, trigger and function from the init migration and the v1 migration (`student_number`, `is_admin`, `photo_path`, the unique serial index, the `unavailable` label). The column names and types that the Go row structs in `internal/stockroom/types.go` scan into. Nullability, including that `email` is now optional.

**`020_constraints`.** Uniqueness (`asset_tag`, `serial_number`, `student_number`, `email`, category and tag names), that many assets may still have a null serial, NOT NULL, enum input rejection, column defaults (`is_admin` false), and the full set of foreign key behaviours. What cascades when an asset, tag or kit is deleted, what is set to null when a category or location is deleted, and that a profile with custody history cannot be deleted.

**`030_bookings`.** The two CHECK constraints and the GiST exclusion constraint. Overlapping reservations are refused, back-to-back ones are allowed because the range is half-open, cancelled and returned bookings are exempt, and kit bookings sit outside the constraint.

**`040_triggers`.** `updated_at` is stamped on update, cannot be backdated by a client, and is untouched on insert. `log_asset_status_change` writes exactly one row per real status change with `{from, to}`, logs nothing for same-value or non-status updates, and logs one row per asset on a bulk update, which is the shape a cart checkout will produce.

**`050_views`.** `active_custody` shows exactly the open events with the asset name and tag joined. `overdue_custody` excludes future due dates, null due dates, and items returned late. Checking an item in drops it from both.

**`060_search`.** `idx_assets_search` is a GIN index over the tsvector expression. Search matches name, description and serial with stemming. An asset with a null description and null serial is still searchable, which is the `coalesce()` regression this file exists for.

**`070_privileges`.** `service_role` keeps full access. `anon` and `authenticated` have none, which matters because the Supabase REST API is still listening on 54321. Default privileges cover future tables for `service_role`. RLS is off by design.

**`080_seed`.** `supabase/seed.sql` loads coherently. The eight types from `Catagories.md`, a tree exactly three levels deep with `Primes` pinned as the only branch that stops short of the Model level, the Lenses categories keeping the names `Catagories.md` gives them, every seeded asset attached to a Model node, the three v1 statuses, the admin (bcrypt hash, `is_admin`) and student (no password, no email) accounts, and an open custody row behind every `checked_out` asset with nothing overdue.

Each file runs inside a transaction that is rolled back, so the suite leaves no trace in the local database.

### Go

**`config_test.go`.** Defaults, environment overrides, `.env` loading and the upward search (nearest file wins, a directory named `.env` is ignored), real environment beating `.env`, `SESSION_IDLE_MINUTES` validation, and the non-obvious case that an exported-but-empty variable shadows the `.env` value.

**`db_test.go`.** `Open` rejects malformed URLs and fails on an unreachable database rather than deferring the error to the first request. Pool sizing. `Ping` returns an error instead of panicking on a closed pool and honours context cancellation.

**`errors_test.go`.** The sentinels are distinct and survive both `fmt.Errorf("%w")` and `errors.Join`, which is what the HTTP mapping relies on.

**`types_test.go`.** `Profile` JSON never contains the password hash. Nullable columns marshal as `null`. `ActiveCustody` flattens its embedded event. An integration test compares the Go enum constants against `pg_enum` so the two cannot drift.

**`password_test.go`.** `HashPassword` produces a salted bcrypt hash and enforces the length limits (at least 8 characters, at most 72 bytes because that is all bcrypt reads). `CheckPassword` distinguishes a wrong password (`ErrBadCredentials`) from an account that has never set one (`ErrPasswordNotSet`) and from a corrupt hash. `NormalizeStudentNumber` trims, keeps leading zeros, rejects anything that is not digits, and bounds the length at `MaxStudentNumberLength`.

**`failsafe_test.go`.** `EnsureFailsafeAdmin` creates the account on first run, and on later runs rotates the password, forces `is_admin` back on, and leaves the operator's name edits alone without duplicating the row. Bad config (`ErrInvalid`) is refused before touching the database, and blank config is reported separately as `ErrFailsafeNotConfigured` — the case the server logs and starts through, rather than a failure.

**`sessions_test.go`.** Unit tests with an injected clock. Tokens are 64 hex characters and unique. A session expires after the idle period with no requests and every request extends it; a zero idle time never expires. `Upgrade` clears the limited flag, `Delete` and `DeleteForProfile` end the right sessions, and expired entries are swept on every `Create` so the map stays bounded.

**`auth_test.go`.** Against the database. A scan by an unknown number is `ErrNotFound`. A scan by an account with no password, null or blank, yields a limited session: `RequireAdmin` refuses it even for an admin, `SetInitialPassword` upgrades it, works only once, and enables typed login. Typed login covers right password, wrong password, unknown number (same error as wrong, so numbers are not enumerable) and no password set. Logout kills the token. `Resolve` reflects a promotion or a deletion on the next request. `Me` and the login response report `has_overdue` once something is past due.

**`users_test.go`.** Every user function refuses a non-admin. Create trims fields, derives `full_name`, nulls blank optionals, leaves the account without a password, and maps duplicate student numbers to `ErrConflict`. Update never touches the password and refuses self-demotion. Delete works for a plain account, is refused while items are checked out, is refused (as `ErrConflict`, not a 500) for anyone with custody history, and refuses self-delete. `SetUserPassword` replaces the hash, and both a reset and a delete drop that user's sessions here in the package, not in the handler.

**`roster_test.go`.** Non-admins are refused. A file with no header or without the required columns is `ErrInvalid`. A mixed file (BOM, Windows line endings, reordered and extra columns, a blank line, a bad number, a missing photo, a nameless row) reports the right action and line number per row, writes the good rows, copies the photo to `uploads/profiles/<number>.jpg`, and leaves `is_admin` and the password of an updated account alone. Re-importing with a blank photo keeps the old one; re-importing with a different extension replaces the stored copy instead of orphaning it; a photo with no extension fails its row; absolute photo paths work.

**`custody_test.go`.** Phase 4, the core loop, against the live database. `CheckOutAssets`: a cart of two lands exactly one open custody row and one status flip per asset and comes back in cart order; a duplicate id is one item; a non-admin cannot name another custodian or send `override_overdue`; an admin can do both, and the row records the custodian and the admin who handed it over separately. The due date is bounded at both ends, with the cap's exact instant quoted in the message. An overdue custodian is refused with the blocking items named, the override is the admin's alone and only with the flag, and an item due later today is not overdue. The all-or-nothing rule gets its own test per blocking status plus one for an unknown id: the available item in the same cart must end with zero custody rows and an untouched status, which is the partial-checkout failure the whole transaction exists to prevent. `CheckInAsset`: anyone signed in can return anyone's item, the damage note lands trimmed on `condition_in` (and a blank one stays null), a second check-in is `ErrConflict` rather than a duplicate row, and an asset whose status drifted to `available` while its row is open still returns. `ScanItem`: a checked-out serial returns on the spot even when the scanner isn't the holder, an available one yields the same payload `GetAsset` gives (asserted against it, so the two paths can't diverge), an unavailable one yields it with `checkable` false, surrounding whitespace from the scanner's Enter is trimmed, an item whose status drifted away from its open custody row still returns, and an unknown serial is `ErrNotFound`. The lists and both history reads cover the permission split from CLAUDE.md §7 — lists and the asset trail admin-only, a user's own history not — plus `days_overdue` counting to now while out and to the return afterwards, and an item dropping off the overdue list when it comes back. `checkDueAt` and `normalizeCartIDs` are pure and tested as tables, no database needed.

**`server/custody_test.go`.** The seven Phase 4 routes over the wire. Which sentinel becomes which status: 404 on an unknown serial or asset, 400 on a blank serial, an empty cart, a missing or over-cap due date, a malformed uuid and an unknown JSON field, 409 on a cart whose items are already out and on the overdue block, 403 on a non-admin naming a custodian or overriding. That `POST /assets/{id}/checkin` accepts an empty body, because a check-in with no damage note is the common case. That the custody lists and the asset trail are 403 for a non-admin and carry resolved names and `days_overdue` for an admin, while a user's own history is 200 for themselves and 403 for someone else. And that adding `GET /users/{id}/history` didn't shadow `GET /users/{id}`.

**`server/auth_test.go`.** The routes end to end with a live database: scan login returning a limited token that can reach `/me` but gets `403 needs_password` elsewhere, set-password (too short is 400, second time is 409), then a full session. Login error statuses. The token is accepted from the bearer header or the cookie, an `Authorization` header in another scheme falls through to the cookie, logout clears both, and bad or missing credentials are 401. User CRUD over HTTP including 409 on duplicates, 400 on a malformed id, that a password reset or delete signs the user out, and that every user route is 403 for a non-admin. Roster import as multipart with `photo_dir` and as a raw `text/csv` body, plus the 400 cases, including every other content type.

**`server/json_test.go`.** The sentinel to status mapping for all eight errors, wrapped and joined errors, an unknown error becoming a 500 whose body leaks nothing, request decoding (unknown fields, malformed JSON, wrong types, empty body) and the 1 MB size cap.

**`server/router_test.go`.** `/health` returns the documented body against a live database, returns 500 when the database is gone, answers HEAD, and the router 405s the wrong method and 404s unknown paths. `server/auth_test.go` adds the same method checks for the auth routes.

**`pgerr_test.go`.** `mapPgError` decides which Postgres failures are the caller's fault and which are the server's, so each SQLSTATE is pinned directly: unique and foreign key violations become `ErrConflict`, a malformed uuid or enum becomes `ErrInvalid`, and anything else stays a 500 carrying the operation name. `constraintMessage` covers every hand-written phrase and both fallbacks, so the admin panel never renders an empty string. A live-database test then provokes a duplicate student number, a duplicate email and a delete blocked by custody history for real, because those constraint names are hardcoded strings: a migration that renames one would silently downgrade the message to a raw identifier, and only a test against the real schema catches that.

**`sessions_concurrent_test.go`.** The session store is the one piece of mutable state every request shares. These exist to be run under `-race`: concurrent `Create` hands out unique tokens and loses no writes, every method survives being called at once, `Upgrade` only ever moves a session limited to full (the reverse would bounce a user who just set a password back to the set-password screen), and `Get` returns a copy a caller cannot mutate back into the store. The assertions alone would pass on an unsynchronised map; the detector is the point.

**`hashcost_test.go`.** Pins the production bcrypt work factor. The suites lower the live cost from `TestMain` (see the speed budget below), so this asserts on `DefaultPasswordHashCost`, the value a released binary starts from, and checks that a hash written at that cost still verifies after the cost has been lowered. Without it, the speedup could quietly become a downgrade to real password storage.

**`main_test.go` (both packages).** `TestMain` lowers the bcrypt work factor to `bcrypt.MinCost` for the whole package, and the server's also silences handler logging.

### Desktop frontend

**`src/lib/db.test.ts`.** Every helper's PostgREST query shape (table, columns, filters, ordering) against a recording fake. The `{data, error}` unwrapping, where errors become thrown `Error`s carrying the database message and null data becomes an empty list. The category self-join embed and how it flattens into `category_name` and `subcategory_name`. The `listAllAssetTags` grouping, including null embedded rows, ordering, and empty payloads.

**`src/lib/AssetBrowser.test.ts`.** The browse screen. Category to subcategory filtering, including the stale-subcategory reset and uncategorised assets. Empty, loading and error states. The detail dialog opens by click or keyboard and closes by button, backdrop, or Escape.

**`src/App.test.ts`.** The admin screen. Initial load, create, edit and delete asset flows, the edit and cancel state machine, delete confirmations including declining, tag create trimming and blank rejection, and the "add tag" dropdown hiding tags the asset already has, which would otherwise violate the `asset_tags` primary key.

These three files cover `db.ts`, `supabase.ts` and the supabase-js admin screen, all of which Phase 6 deletes. Keep them green while the code is still in use, but do not extend them; they go when the code goes.

### Web app

**`src/App.test.ts`.** Two smoke tests over the placeholder shell: the heading renders and the two links point where they claim. The value here is the wired harness, not the assertions — a fresh clone runs `npm --prefix web-app test` and gets a real pass, so the first genuine screen has somewhere to land.

## Bugs found and fixed

The "Add" tag button in `App.svelte` never worked. Svelte 5's legacy compiler turned `addTagSelection[assetId] = ''` into an invalidation that referenced the `asset` each-block variable from outside its scope, so every add threw `ReferenceError: asset is not defined` after the row had already been inserted. The user saw an error and a stale list. Replacing the mutation with a reassignment fixes it, and `src/App.test.ts` covers it.

`listAssets` in the UI import embedded the category parent with a `!categories_parent_id_fkey` hint, which PostgREST rejects, so no assets loaded at all. The self-join has to be the column-as-embed form `parent:parent_id(name)`. `src/lib/db.test.ts` pins the query shape.

## Not tested, and why

`web-app/` has no application code yet beyond a placeholder shell. It gets a suite when Phase 6 gives it a real `lib/api.ts` and screens. CI type-checks and builds it in the meantime.

`server/main.go` is process wiring only (signal handling, `ListenAndServe`, graceful shutdown). Testing it would mean testing the standard library.

`desktop-app/main.go` and `app.go` are the Wails window host and the scaffold `Greet` method, both slated for removal in Phase 6.

`internal/stockroom/doc.go` and the struct definitions in `types.go` have no behaviour beyond the JSON tags and enum values already asserted.

The grant migration's `alter default privileges` for sequences and functions has nothing reading it yet; the tables case is covered.

Phase 3 (browse) is written but deliberately untested for now: `GetCategoryTree`, `ListAssets`, `GetAsset`, the three browse routes and `/files/`. It was verified by hand against the seeded database (every filter level, search, the detail payload, the error statuses, and that `/files/` serves a photo but not a directory listing). The seam list below still describes the coverage it needs when tests are added back.

The rest of `TODO.md` Phases 5 to 8 (the admin asset and category API, the backup CLI, kits) does not exist yet.

Barcode scanner input handling. `lib/scanner.ts` is not written, and the keystroke-timing threshold needs real hardware to pin down (CLAUDE.md §10).

## Testing features that don't exist yet

The project is roughly a third of the way through `TODO.md`. Most of the remaining testing value is in how Phases 3 to 8 get covered *as they land*, not in more assertions on today's code. This section is that plan.

### The seam rule

Test at `internal/stockroom` function boundaries (`ListAssets`, `ScanItem`, `CheckOutAssets`, `CheckInAsset`, `ImportRoster`, …) and at HTTP routes. Never at the SQL-string level — pgTAP already owns the schema, and a Go test that re-asserts a `WHERE` clause just breaks twice when the query is rewritten without changing behaviour.

### Which layer gets which test

- **pgTAP** (`supabase/tests/`). Invariants the database itself enforces: uniqueness, foreign key behaviour, the booking exclusion constraint, trigger-written columns, view contents. If removing a migration line would make a pgTAP file fail, it belongs there.
- **Go against the live database** (`internal/stockroom/*_test.go`). Everything that isn't a database invariant: permission checks (admin vs. non-admin), business rules that live in Go (due-date bounds, overdue blocking, cart-wide atomicity), and error-sentinel-to-Postgres-error mapping.
- **`server/*_test.go`**. Wire format, HTTP status codes, session modes (limited vs. full), and routing. Not business logic — that's already covered one layer down; these tests exist to catch a handler that maps a sentinel to the wrong status or forgets a route.

### Per-phase seam list

**Phase 3 (browse), written but not yet tested.** `ListAssets`: category-tree filtering at each of the three levels, free-text search matching name/description/serial, `unavailable` assets still listed (they're not hidden, just not checkable-out), empty-filter returns everything.

**Phase 4 (cart checkout, scan, check-in — the core loop), landed and covered** by `custody_test.go` and `server/custody_test.go` (above). The seams this section called for, kept here as the record of what they are. `CheckOutAssets`: a non-admin can only check out to themselves, `dueAt` is bounded (`now < dueAt <= now + 7 days`, enforced server-side regardless of what the client sends), `ErrOverdueBlocked` is returned unless the actor is an admin overriding, the whole cart fails together if any one asset isn't `available` (no partial checkout), exactly one `custody_events` row is written per asset, and `assets.status` flips for every item in the same transaction as the custody rows. `ScanItem`: a `checked_out` serial checks in immediately regardless of who checked it out; an `available` serial returns the same payload the click-to-open-detail path returns, so the frontend can't tell scan and click apart; an unknown serial is `ErrNotFound`. `CheckInAsset`: any signed-in user can check in any item, an optional damage note lands on `condition_in`, checking in something already checked in is a no-op error, not a duplicate row.

**Phase 5 (admin panel, backup).** Asset/category CRUD follows the same admin-only pattern already pinned for users. `overdue_custody` view consumption: the admin list and the sign-in warning must show the same rows. Backup CLI: a round-trip test — export, wipe a scratch schema, reapply migrations, reload from CSV, assert row counts match per table — is worth more than unit-testing the CSV writer.

**Phase 6 (frontend wiring).** No new backend seams; this deletes `desktop-app/frontend/src/lib/{db,supabase}.ts` and the tests that cover them (Section "Desktop frontend" above already flags which ones).

**Phase 7 (kits, if time allows).** `CheckOutKit`/`CheckInKit` would need the same cart-atomicity treatment as Phase 4: every asset in the kit moves together or none do.

### The speed budget

Target: the full local suite (`go test`, pgTAP, both frontends) finishes in well under 15 seconds, excluding `supabase start`. Rules that protect that number as Phases 3 to 8 add tests:

- Hash fixtures at `bcrypt.MinCost` via `TestMain` (`internal/stockroom/main_test.go`, `server/main_test.go`), never `DefaultPasswordHashCost`. This is what keeps `-race` affordable — see `hashcost_test.go` for the guardrail that stops it from leaking into production.
- No `time.Sleep` to wait out a due date or an idle timeout. Inject a clock the way `sessions_test.go` does.
- Fixtures via `t.Cleanup`, not a full `supabase db reset` per test. DB-backed tests should be additive and share one database within a run.
- New Go test files register themselves for `-race` for free — nothing to opt into. If a new file introduces shared mutable state (a cache, a rate limiter), give it the same concurrent-access treatment `sessions_concurrent_test.go` does, including a mutation-verify pass (temporarily break the synchronization, confirm the detector actually fires) before trusting the test.

### Frontend plan (deliberately shallow)

Frontend code is still expected to change substantially, so don't invest ahead of Phase 6. Once `lib/api.ts` exists:

- Test `lib/api.ts` against a fake `fetch` — request shape and response parsing, one test per endpoint it wraps.
- Test `lib/scanner.ts`'s scan-vs-typed keystroke-timing detection as a pure function of a keystroke-timestamp array. It's hardware-independent and the one piece of frontend logic with a real bug surface (CLAUDE.md §10).
- Everything else (screens, dialogs, cart state) stays at the smoke level `web-app/src/App.test.ts` already demonstrates: does it render, do the obvious interactions not throw. Don't chase coverage on UI that's likely to be rewritten.
