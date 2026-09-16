# Stockroom: backend and functionality to-do

Backend/functionality work only (no UI/layout/styling; UI is planned separately). `CLAUDE.md` is the architecture/decision reference; this file is the ordered work list. Phases map to the Week 5 to 9 timeline in CLAUDE.md §12.

**Shape of the work.** All logic lives in Go, in `internal/stockroom`. `server/` exposes it as a localhost JSON API. Both the Wails desktop app and the web-app are thin Svelte UIs calling that API via `fetch` (`lib/api.ts`). No supabase-js, no DB credentials in TypeScript. Postgres keeps running inside the Supabase Docker stack; Go connects on port 54322.

**Target flow being built.** Scan ID → sign in · browse/filter → click → detail → add to cart · check out cart (due ≤ 7 days) · scan item → check in (if out) or show detail (if available) · admin panel for assets/categories/users/overdue/backup.

---

## Phase 0: Foundation (Week 5)
- [x] Move `go.mod` to the repo root (module `stockroom`); `desktop-app`, `server`, `cmd`, `internal` share one module. `wails dev`/`wails build` still run from `desktop-app/`
- [x] `internal/stockroom/` skeleton: `db.go` (pgxpool from `DATABASE_URL`), `types.go` (structs for every table + `active_custody`/`overdue_custody`), `errors.go` (`ErrNotFound`, `ErrForbidden`, `ErrConflict`, `ErrOverdueBlocked`, `ErrPasswordNotSet`, `ErrBadCredentials`)
- [x] `.env` loading (`godotenv`, searched upward from cwd) in `internal/stockroom/config.go` + `.env.example` with all vars from CLAUDE.md §9
- [x] `server/main.go`: `net/http` mux, JSON helpers, error→status mapping, `GET /health` hitting the DB; verify with `curl`
- [x] Update the start scripts (since consolidated into `scripts/dev.sh` / `dev.ps1`) to also launch `go run ./server` and kill it on exit (Windows script still untested on real Windows)

## Phase 1: v1 schema migration + seed (Week 5)
- [x] New migration (CLAUDE.md §6.2): `profiles` += `student_number text unique`, `first_name`, `last_name`, `photo_path`, `is_admin bool not null default false`; `email` nullable. `supabase/migrations/20260908100000_v1_flow.sql`
- [x] `assets` += `photo_path`; `create unique index idx_assets_serial on assets(serial_number)`
- [x] `alter type asset_status add value 'unavailable'`
- [x] Rewrite `supabase/seed.sql`: category tree from `Catagories.md` (Type → Category → Model, via `parent_id`), a handful of sample assets under Model nodes with serial numbers (incl. a `T7iBat-001`-style one), one admin + one non-admin profile with `student_number`. Sections of `Catagories.md` that list models directly under a type got a middle level (e.g. Lights → Studio Lights / Light Modifiers) so every branch is three deep
- [x] `EnsureFailsafeAdmin()`. On server start, upsert the `ADMIN_STUDENT_NUMBER` account with `is_admin = true` and bcrypt(`ADMIN_PASSWORD`). `internal/stockroom/failsafe.go`; bcrypt helpers in `password.go`
- [x] `supabase db reset` and confirm via Studio/psql. pgTAP `080_seed` now pins the seeded tree, accounts and custody rows

## Phase 2: Auth, sessions, users (Week 5)
- [x] bcrypt helpers (`golang.org/x/crypto/bcrypt`) for `profiles.password_hash`. `password.go` (landed with Phase 1); passwords are 8 to 72 characters
- [x] `LoginByScan(studentNumber)` → session token; if `password_hash` is null, return `needs_password: true` and a limited session that can only call `SetInitialPassword`. `auth.go`
- [x] `LoginByPassword(studentNumber, password)` → session token; `ErrPasswordNotSet` if hash is null. Unknown number reads as `ErrBadCredentials`
- [x] `SetInitialPassword(session, password)`. Only valid while hash is null (`ErrConflict` otherwise); upgrades the session in place
- [x] `Logout(session)`, `Me(session)` → profile + `has_overdue` flag (drives the sign-in warning). Login responses carry `has_overdue` too
- [x] In-memory session store with idle timeout (`SESSION_IDLE_MINUTES`); middleware attaches actor to request. `sessions.go`, `server/session.go`; token via `Authorization: Bearer` or the `stockroom_session` cookie. `Resolve` reloads the profile per request so admin changes and deletes apply immediately
- [x] `RequireAdmin(actor)` gate; wire into every admin-only function going forward. A limited session is never admin
- [x] Users: `ListUsers`, `GetUser`, `CreateUser`, `UpdateUser`, `DeleteUser` (admin; block delete if user has open custody), `SetUserPassword` (admin reset). `users.go`. Also refused: deleting yourself, removing your own admin flag, deleting anyone with custody history (the FK). No password is set here: a new account sets one at its first scan login. Resets and deletes drop the user's sessions, inside `internal/stockroom` so every caller gets the rule
- [x] `ImportRoster(csv)`. Columns `first_name,last_name,student_number,photo_path`; upsert by `student_number`; copy photo into `UPLOADS_DIR/profiles/<student_number>.<ext>` and store the relative path; return per-row results. `roster.go`. Columns match by header name in any order; `is_admin` and passwords survive re-import; a blank photo keeps the old one; a photo with no extension fails that row, and a new extension replaces the old copy
- [x] HTTP: `POST /auth/scan`, `POST /auth/password`, `POST /auth/set-password`, `POST /auth/logout`, `GET /me`, `/users…`, `POST /users/import`. `server/auth.go`, `server/users.go`. Import takes multipart (`file` + optional `photo_dir`) or a `text/csv` body

## Phase 3: Browse (Week 6)
- [x] `GetCategoryTree()`. Full tree in one call (Type → Category → Model) for the left-side filters. `categories.go`; the table is a few dozen rows, so it is read flat and nested in Go rather than with recursive SQL. Every level comes out in `categories.sort_order` order (`Catagories.md`'s document order), name breaking a tie. A row whose parent is missing, or which is its own parent, becomes a root instead of disappearing
- [x] `ListAssets(actor, filter)`. Filter by any category node (includes descendants, read off the in-memory category tree), status, free-text; returns category path, photo URL and current holder per asset, in browse order (see the addendum below). `assets.go`. Search matches two ways: `plainto_tsquery` over the existing GIN index for words and stemming, plus `ilike` over name/tag/description/serial for the partial identifiers a tsquery can't match (`T7iB` → `T7iBat-001`). `%` and `_` in the search text are escaped to literals. An unknown category id is `ErrNotFound` and an unknown status `ErrInvalid`, so a broken filter is never a silently empty list. `unavailable` assets are listed, not hidden
- [x] `GetAsset(actor, id)`. Detail popup payload incl. current custodian (if checked out) and category path. The open custody row is read whatever the status column says, so a drifted status still reports the real holder; custodian name falls back from first/last to `full_name` to the student number
- [x] Static file serving: `GET /files/…` from `UPLOADS_DIR`. `server/files.go`. No session: the desktop app renders photos in `<img>` tags, which cannot carry the bearer token. Directory listings are refused
- [x] HTTP: `GET /categories/tree`, `GET /assets?category=&status=&q=`, `GET /assets/{id}`. `server/assets.go`; all three need a full session, none is admin-only

**Phase 3 addendum (2026-09-12 grilling session, landed 2026-09-13).** Two decisions made after the phase shipped, plus what reviewing them turned up:
- [x] `AssetListItem` carries `custody *AssetCustody`, populated for every checked-out asset and visible to every actor. One `holdersByAsset` query per list, not one per row. `AssetDetail` is now an alias for `AssetListItem`: once a row carries the holder, detail knows nothing a row doesn't
- [x] Sort order. `categories` gains `sort_order` (migration `20260913090000_category_sort_order.sql`, seeded from `Catagories.md`'s document order), because nothing in the table recorded that order and Phase 5 lets an admin rename a Type, which a hardcoded list in Go would not survive. `GetCategoryTree` reads `order by sort_order, name`; `ListAssets` sorts in Go on the category path, then `available` before `checked_out` before `unavailable`, then name, then asset tag. The sort is in Go because its first key is the asset's place in the tree, which the one category read already knows
- [x] `AssetCustody.student_number` is admin-only (`forViewer`). The 2026-09-12 decision opens the current holder's **name** to everyone; the student number is the scan-login key, so a browse list carrying it would hand every student a list of other people's credentials. The name, the due date and the overdue flag stay open to all
- [x] `ListAssets` and `GetAsset` take the actor and call `RequireFullSession` themselves, matching Phase 4: the rule doesn't depend on the router
- [x] `GET /assets/{id}` on an unknown id answers `not found: no asset <id>` instead of leaking the internal op name, and `photoURL` resolves a stored `..` inside the uploads root

## Phase 4: Core loop: scan, cart checkout, check-in (Week 6)
- [x] `ScanItem(actor, serial)`. The one function behind every item scan. `internal/stockroom/custody.go`:
  - asset out (decided by the open custody row, not the status column, so drift can't answer "not checked out" to someone holding the item) → `CheckInAsset` immediately, return `{action: "checked_in", …}` with `returned_from` naming who had it
  - asset `available` → return `{action: "detail", asset}` (byte-for-byte the `GetAsset` payload, so the frontend can't tell a scan from a click) with `checkable: true`
  - asset `unavailable` → the same detail payload with `checkable: false`, which is the flag that closes the path to the cart. A just-checked-in item is `checkable: false` too: its dialog is a confirmation, not an add screen
  - unknown serial → `ErrNotFound`; a blank one → `ErrInvalid`. The serial is trimmed first, because a scanner sends a trailing Enter
- [x] `CheckOutAssets(actor, CheckoutInput)`. Single transaction:
  - non-admin: `custodian_id` must equal the actor (blank means the actor); an admin may pick anyone. A non-admin sending `override_overdue` is `ErrForbidden`, not a silently dropped field
  - `due_at` > now and ≤ now + `MaxCheckoutDays` (7), server-enforced. The error names the latest acceptable instant, because a date picker offering "seven days out" at end of day lands past the cap
  - refuses with `ErrOverdueBlocked` if the custodian has any row in `overdue_custody`, unless an admin passes `override_overdue`; the message names what to bring back
  - every asset must be `available`, checked under `for update` in id order (so two carts sharing an item can't deadlock) and including a guard on stray open custody rows. The whole cart fails together, naming which items weren't available
  - a duplicate id in the cart is one item, not two: a cart is a set
  - one `custody_events` row per asset (`checked_out_by = actor`) plus the `assets.status` flip, in the same transaction
- [x] `CheckInAsset(actor, assetID, damageNote?)`. Closes the open `custody_events` row (`checked_in_by = actor`, `condition_in` = the trimmed note) and sets the status back to `available`. Any signed-in user may return any item. The **open custody row decides**, not the status column, so an asset whose status drifted still returns correctly; an item that is not out is `ErrConflict`, never a second row
- [x] `ListActiveCustody()`, `ListOverdueCustody()`. Read through the `active_custody` / `overdue_custody` views so "overdue" has one definition, shared with the sign-in warning and the checkout block. **Admin-only**: the 2026-09-12 decision opens the *current holder of a named item* to everyone (via `ListAssets`/`GetAsset`/`ScanItem`), not the roster of who has what, which is an admin-panel screen. Rows carry custodian name, student number, serial, due date and `days_overdue`, which is what CLAUDE.md §8.7's overdue table needs
- [x] `GetAssetHistory(assetID)`. Full past-custodian trail for one asset. **Admin-only** (`ErrForbidden` for a non-admin regardless of whose item it is), distinct from the current-custodian visibility that is open to everyone
- [x] `GetUserHistory(userID)`. Full custody trail for one user. Non-admin may only read their own (`ErrForbidden` otherwise); admin may read anyone's
- [x] HTTP: `POST /scan`, `POST /checkout`, `POST /assets/{id}/checkin` (an empty body is a check-in with no note), `GET /custody/active`, `GET /custody/overdue`, `GET /assets/{id}/history`, `GET /users/{id}/history`. `server/custody.go`; all seven need a full session
- [x] A limited session (scan login, no password set) is refused by `ScanItem`, `CheckOutAssets`, `CheckInAsset` and `GetUserHistory` in the package via `RequireFullSession`, not only by the router, so the rule doesn't depend on the HTTP layer
- [x] Tested: `internal/stockroom/custody_test.go` (the rules, against the live database) and `server/custody_test.go` (statuses, routing, payload shape). Verified by hand with `curl` end to end as well: scan → detail, scan → check-in, cart checkout, the 7-day cap, the overdue block and its admin override, both history reads and their refusals

## Phase 5: Admin panel API (Week 7)
- [x] `CreateAsset`, `UpdateAsset`, `DeleteAsset` (refused for any asset with custody history, open or closed, because `custody_events` cascades and the trail is the point; the message says to mark it unavailable instead), `SetAssetStatus` (`available` ⇄ `unavailable`; cannot touch `checked_out`; "out" is the open custody row, not the column), asset photo upload → `UPLOADS_DIR/assets/<id>.<ext>`. `AssetInput` carries no `photo_path`: `SetAssetPhoto` is that column's only writer. A unit may file under any category node, not only a Model (`docs/adr/0001`)
- [x] `CreateCategory`, `UpdateCategory`, `DeleteCategory` (block if it has children or assets), enforce max depth 3. New nodes take `sort_order = max(sort_order) + 1` among their siblings, and a level can be renumbered by hand: it is what the browse screen and the filter tree sort on
- [x] Overdue list is `ListOverdueCustody` (Phase 4). Just ensure it's admin-panel friendly (custodian name, student number, days overdue)
- [x] `BackupNow()` → calls Phase 7's export, returns the folder written
- [x] HTTP: `POST/PUT/DELETE /assets…`, `POST /assets/{id}/photo`, `POST/PUT/DELETE /categories…`, `POST /admin/backup`
- [x] Validated by package and HTTP Go tests before UI work starts

## Backend deepening (2026-09-13, branch `refactor/backend-deepening`)
Done before the frontend is wired, while every signature is still free to change. CLAUDE.md §13 has the reasoning.
- [x] `Auth` folded into `DB`. `Open(ctx, url, Options{SessionIdle, UploadsDir, BackupDir})`; `server/` is `deps{db}`
- [x] `categoryIndex` + `categoryTreeShape` merged into one `categoryTree`; the browse filter is `= any(descendants)` rather than a recursive CTE
- [x] `FilesPrefix` and `photoURL` live in `photos.go`; `Profile` carries `photo_url` like `AssetListItem` does
- [x] `openCustodySQL` is the one definition of "out", used by `ScanItem`, the cart lock, `DeleteAsset`, `SetAssetStatus`
- [x] `ImportRoster`, `SetAssetPhoto`, `BackupNow`, `ExportAllTablesToCSV` read their directories from `DB`
- [x] `GetCategoryTree(ctx, actor)` requires a full session in the package, like every other read
- [x] `SESSION_IDLE_MINUTES` defaults to 5 (was 30 in code, 5 in CLAUDE.md). *Raised to 10 on 2026-09-14, and the window now tracks interaction rather than requests alone.*
- [x] Test suite cut to one happy path + one gate per module; the removed cases are listed in `TESTING.md`. CI skips docs-only changes while keeping the `tests` check green

## Phase 6: Frontend wiring + UI (Week 8)
Built as **one shared workspace package**, `packages/ui` (`@stockroom/ui`), not twice. `docs/design/design-system.md` §2.2 is the layout and §14 is the order it was built in. Both hosts render `<StockroomApp>` and nothing else: if either app grows a screen, something has leaked out of the package and the two have started to drift (§1.4, §14 step 10).
- [x] `packages/ui/src/lib/api/`. One `client.ts` (token, 401 hook, error mapping) and one function per endpoint in `index.ts`. No rule lives here: the 7-day cap, the overdue block and every permission check are enforced in `internal/stockroom` regardless of what this sends
- [x] `lib/scanner.ts`, once, in the package. Keystroke buffer + scan-vs-typed detection behind `SCAN_KEY_THRESHOLD_MS` (50ms) so Week 7 tuning is a one-line change, plus the `Ctrl+Shift+D` timing diagnostic (design doc §9.4). The sign-in screen says "that looks like an item barcode" rather than failing as a bad login (§15 Q4)
- [x] Cart = frontend-only ids in `sessionStorage`, so a reload doesn't clear it; only sign-out or an idle-timeout 401 does (§15 Q5). Checkout calls `POST /checkout` once
- [x] Sign-out prompt after checkout (the `<ScanResult>` confirm surface); the 401 hook in `app.svelte` clears session, cart, catalog and scan log together
- [x] Overdue users: `session.checkoutBlocked` disables Add and the dock the instant they sign in, *in addition to* `ErrOverdueBlocked` server-side (§15 Q6)
- [x] Screens: sign-in, browse + detail, cart page + checkout, scan result, my history, and the five admin tabs (assets, categories, users + roster import, overdue, backup). Shell is `<TopBar>` + `<Sidebar>` + `<CartDock>` over a hash router
- [x] Deleted `desktop-app/frontend/src/lib/{supabase,db}.ts`, `AssetBrowser.svelte` and their tests; `@supabase/supabase-js` was already gone; `Greet` stripped from `app.go` and its generated `wailsjs` binding removed
- [x] CORS on the Go server (`withCORS` in `server/router.go`) for the Wails webview and the two Vite dev origins. Every entry is a loopback address; this is not a step toward LAN access
- [x] **`POST /custody/{id}/note`** (`AnnotateCustodyEvent`). Not in the original plan: a scanned checked-out item is checked in *before* the surface offering "Add a note" renders (CLAUDE.md §1.5), so `CheckInAsset`'s note parameter is already gone by then and the field specced in design doc §8.6 had nowhere to write. Lands on the same `condition_in` column
- [x] Toolchain: `desktop-app/frontend` had a stale nested `node_modules` pinning Vite 7 / plugin-svelte 6 / vitest 3 against the root's 8/7/5 — exactly the "two Svelte copies, state silently stops updating" hazard §2.2 warns about. Removed so the workspace hoists one copy, and `dedupe: ['svelte']` added to both Vite configs. Its `svelte.config.js` moved from `svelte-preprocess` to `vitePreprocess`: svelte-preprocess rewrites `.svelte` files inside `node_modules` too and breaks bits-ui's rune detection

### Phase 6 packaging: one install, everywhere (2026-09-14)
The workspace only helps if every entry point installs it the same way. These landed together:
- [x] **`scripts/ensure-deps.sh`** (now `./scripts/dev.sh deps`) is the one definition of "the dependencies are up to date": it clears any *shadowing* nested `node_modules`, runs `npm install` (or `npm ci` with `--ci`, and automatically after clearing a nested copy, because npm hoists and the remaining tree can have gaps), then `go mod download` so a missing Go module fails with a readable message instead of three lines into the server log. `dev.sh up` and `dev.sh test` both call it; `dev.ps1` carries a PowerShell copy of the same steps
- [x] "Shadowing" means an installed **package**, not any nested `node_modules` at all. Vite writes its dep-optimisation cache to `<app>/node_modules/.vite` and `.vite-temp`, and npm puts workspace script binaries in `.bin`; a first cut of the check treated those as a stale install and reinstalled the world on every single run. Only non-dot entries count
- [x] **`scripts/test-all.sh` (now `./scripts/dev.sh test`) installs first.** It never did, so on a fresh clone every npm step failed with `vite: not found` while the Go suites passed — a half-green run that looked like a frontend bug
- [x] Both start scripts `exit 1` on a failed install instead of launching into a broken tree. Neither had `set -e`, so the failure was being swallowed
- [x] **CI installs once at the root.** It was running `npm ci --prefix desktop-app/frontend` and `--prefix web-app`, which is the nested-install bug in the one place nobody watches, and caching two per-app lockfiles that no longer exist
- [x] **CI's paths filter gained `packages/**`** (plus the root `package.json` / `package-lock.json`). Without it a change to `packages/ui` — now the entire frontend — was classified docs-only and skipped the build
- [x] **`.gitignore` covers npm, pnpm and yarn**, not just `node_modules/`: `.npm/`, `.pnpm-store/`, `.yarn/cache`, `.pnp.*`, every `*-debug.log`, and `web-app/dist/`. `pnpm-lock.yaml` and `yarn.lock` are ignored on purpose — one lockfile, and a second means two tools disagree about the tree. The `node_modules/` rule is unanchored so a nested one is caught too. `node_modules/` itself had been committed in `cc6a258` (15,404 files) and is untracked again
- [x] **`.githooks/pre-commit` runs the full suite before a commit is created** (2026-09-14), installed by `dev.sh deps` pointing `core.hooksPath` at the tracked directory rather than asking every contributor to remember a `git config` line. It reuses `scripts/dev.sh test`, so there is still one definition of "the suite", and skips itself on docs-only commits using the same path list as `.github/workflows/tests.yml`. `--no-verify` and `STOCKROOM_SKIP_TESTS=1` are the documented escapes; CI runs the same suite on the PR, so a skipped hook delays a failure rather than hiding it

### Still open in Phase 6
- [ ] Wire the **command palette** (`/` focuses search today; `Cmd/Ctrl+K` jump-to-asset is unbuilt, design doc §5.1)
- [ ] Below-900px behaviour is untested. §7.2 calls it a courtesy, not a target, but "make sure it isn't broken" is still unchecked
- [ ] Run the desktop app under `wails dev` against real hardware. Only the web host has been exercised in a browser so far

## Phase 7: Backup & restore (Week 8)
Specified in full in **`docs/design/backup.md`** (2026-09-14) after a grilling pass over the original three-line plan. The spec is the detail; this is the checklist. Section references below are to that file.

### What already works
- [x] `ExportAllTablesToCSV` in `internal/stockroom/backup.go`. `COPY … TO STDOUT WITH CSV HEADER` per table via pgx into `BACKUP_DIR/<yyyy-mm-dd>/`, one repeatable-read snapshot, staged and renamed into place. Verified against the live database: all 12 tables, 16 assets, 2 profiles, with `password_hash`, `student_number`, `is_admin`, `serial_number` and `asset_tag` present (§A.1)

### The ten gaps it does not cover (§A.2)
Nothing off-site · photos never backed up · `assets_asset_tag_seq` (`last_value` = 89) not captured · CSVs load in FK-violating alphabetical order · a restore would fire `trg_asset_status_log` · schema version not recorded · silent failure invisible · `backupReplace` is in-process only · no restore path at all · all config is `.env`.

### Settings out of `.env`, into the database (§C.2)
- [ ] Migration: single-row `app_settings` (`check (id)`) holding dirs, `keep_days`, `stale_hours`, `schedule_hour` and per-target config
- [ ] Bound the three numbers in the DDL **and** in the settings endpoint, so a bad value is a 400 naming the field, not a 500 from a constraint: `keep_days >= 1`, `stale_hours >= 1`, `schedule_hour between 0 and 23`. Zero is valid for the hour (midnight) and for neither of the others — a 0-day retention re-copies every photo nightly and prunes the backup it just wrote, and a 0-hour staleness threshold fires the sign-in warning permanently
- [ ] `internal/stockroom/settings.go`: load/save, `.env` as a first-boot seed only, `github_token` and `archive_passphrase` masked on read
- [ ] Optional **archive encryption** (§C.5), off by default: AES-256-GCM under a scrypt-derived `archive_passphrase`, applied before the zip reaches any target, prompted for on restore. Document that the passphrase lives outside both targets and that losing it loses the backup — it trades a confidentiality risk for an availability one, which is why it is opt-in
- [ ] **Redact secret columns from the export.** `app_settings` is in `public`, so `publicTables` sweeps `github_token` into `app_settings.csv` and pushes it to the repo it unlocks; GitHub secret scanning would auto-revoke it and kill backups silently. Restore must not overwrite the live value with the redacted null

### Export (§E.1)
- [ ] `sequences.csv` with **`last_value` and `is_called`**, read from each sequence relation directly — `pg_sequences` reports a null `last_value` for a never-read sequence and exposes no `is_called`, and replaying one with `setval`'s default `true` burns its first value (measured: `setval('s', 5, true)` on a fresh `start 5` sequence → `nextval` = 6); `schema_version` in `manifest.json` from `supabase_migrations.schema_migrations`
- [ ] `inventory.csv` (one readable row per item) and `accounts.csv` (one per account, `password_hash` included — plaintext is not recoverable, bcrypt is one-way). Reuse `loadCategoryTree`/`tree.pathOf` and `openCustodySQL` rather than a recursive CTE
- [ ] Zip the raw tables + sequences + manifest + an embedded `RESTORE.md`
- [ ] Cross-process lock via `pg_try_advisory_lock` on a dedicated connection, held for the whole run and released when that connection closes. **Not a lock file with a timeout**: too short and it is torn off a slow-but-live backup, letting a second process write the same dated folder; too long and a killed process wedges backups until someone deletes a file by hand. `false` is a skip, not an error

### Photos (§E.2)
- [ ] `photos_backup.go`: **one live folder, updated in place**, rolling to a new frozen generation every `keep_days`. Never deletes. A missing `uploads/` is a no-op, not an error
- [ ] `RestorePhotos` + a generation picker in the UI. Local only by decision — see §H for what that costs
- [ ] **Bound the mirror with an alert, not a purge**: `photo_min_free_gb` (default 5) and `photo_max_generations` (default 8, two years at the default `keep_days`), warning on the same surfaces staleness uses, with the backup screen listing each generation's size and a delete button. Never delete automatically — auto-purging the only copy of a deleted photo defeats the mirror. Without the bound the mirror grows until the disk fills, and because photos are mirrored in the same run that writes the database backup, what breaks is the backup itself

### Targets, both active when both are configured (§C.3, §E.3, §E.4)
- [ ] `BackupTarget` interface: `Push` / `Versions` / `Fetch` / `Test`
- [ ] Drive via `rclone` (`copy`, `lsjson`, `cat`, `lsd`), with retry/backoff and a UI-driven `rclone authorize` connect flow so a revoked token never needs a terminal
- [ ] GitHub via the REST API, no `git` binary: Git Data API for one atomic commit, commits list for versions, tarball for fetch, and an auto-written repo README
- [ ] Layout: Drive gets dated folders; GitHub overwrites a fixed `backup/` path so git history *is* the backup list and the repo grows by KB of text, not a zip a night
- [ ] **Bound the GitHub history at `keep_days` and purge it by rewriting the branch onto a fresh orphan root.** Overwriting `accounts.csv` does not remove its old versions, and that file is a credential roster — a deleted student stays a working scan-login in every commit predating the deletion. Document both caveats: unreachable objects survive until GitHub GCs them, so scrubbing one account means deleting and recreating the repo, and the force-update is the one irreversible write the app makes

### Restore — one code path, three sources (§E.5)
- [ ] `manifest.json` gains a **SHA-256 per file**, written by the export and verified by the restore *before* the transaction opens. A corrupted download then costs nothing, and it is the only check that catches a CSV truncated mid-quoted-field — one truncated on a line boundary shows up as a row count, one truncated mid-field does not
- [ ] `RestoreFromZip`: schema-version check, checksum check, `set local session_replication_role = replica` (kills the FK-order and trigger-pollution gaps in one line), truncate + `CopyFrom`, then **validate inside the transaction, then `setval` last** — returning an error there lets the deferred rollback leave the live database untouched, where verifying after the commit would only be a report on a half-restored database that is already live. Commit, then clear all sessions
- [ ] Those pre-commit checks are three, not one: **row counts** against the manifest; **referential integrity**, by resetting `session_replication_role` to `origin` in the same transaction and running an anti-join per FK enumerated from `pg_constraint where contype = 'f'` (never hand-listed, so a later migration's FK is covered without anyone remembering); and **every sequence's effective next value** (`last_value + increment_by` when `is_called`, else `last_value`) **strictly above its column's max**, an empty table constraining nothing. Capture `increment_by`/`cycle` from `pg_sequences` and **refuse a descending or cycling sequence with `ErrInvalid`** rather than run a check that was never designed for one — nothing in this schema is either, and a check that quietly passes on a sequence it cannot reason about is the bug the step exists to prevent. Replica mode is what makes the FK check necessary — nothing on the way in rejects an `assets` row pointing at a category the archive does not contain, and unchecked that turns a clean failure into a permanently inconsistent database. No separate trigger-state check is needed: `activity_log` and `updated_at` are restored from the backup by design, and the row-count check already covers them
- [ ] Upload, Drive date-pick and GitHub date-pick all funnel into it — the GitHub fetch repackages its tarball into the same zip
- [ ] `cmd/restore/main.go`, calling the **same** `RestoreFromZip` — for when the server will not start, and for when a wiped database has no account to sign in with
- [ ] **`setval` runs after every rollback-triggering check, never before**, because `setval` is not transactional — verified: a `setval` inside an explicit `rollback` survives it. Written first, a failed validation would roll the tables back while leaving the sequences advanced: a database that looks untouched and hands out colliding keys. Capture each sequence's prior `last_value`/`is_called` beforehand and replay them if the commit fails; that compensation is non-transactional too, so log a failure to compensate loudly and name it in the error
- [ ] `LocalCLIActor() Actor` so the CLI **satisfies** `RequireAdmin` instead of being exempt from it: `Actor{ID: "cli", IsAdmin: true, trustedCLI: true}`, where `trustedCLI` is **unexported**, so `server/` cannot construct one and `Resolve` never sets one — no request can forge it. Dropping the gate would open the HTTP path; a second CLI-only restore would be emergency code first run during an emergency. Log which of `cli` or a named admin ID performed a restore
- [ ] **Zero-account recovery test**, in this order: `supabase db reset` (reseeds), sign in as the seeded admin and confirm the "no failsafe admin" warning fires — it lives on an admin-only screen, so it is unobservable once the accounts are gone — *then* `truncate profiles`, confirm every HTTP restore route is refused, restore end to end through `cmd/restore`, and confirm the backed-up accounts sign in again. Test the exported API: assert `LocalCLIActor()` yields an actor `RequireAdmin` accepts and that `Resolve` returns `trustedCLI` unset for every session (that second one carries the security claim). An external `Actor` literal naming `trustedCLI` failing to compile is a compile-time fact — record it in a comment, not a test that pretends to check it. Without it the CLI path is first exercised during an actual disaster
- [ ] **The failsafe admin is best-effort, so the panel route is conditional.** An unfilled `.env` restores into zero accounts and makes the admin panel unreachable exactly when it is needed. Do not make `.env` a startup requirement (2026-09-08 decision stands); instead have `BackupStatus` report whether a failsafe admin exists and warn on the backup screen when it does not

### Scheduling, in-server (§E.6)
- [ ] Goroutine in `server/main.go`: fires at `schedule_hour`, and **runs immediately on boot if the last success is stale**, which is the whole of "back up first thing when the machine is available"
- [ ] Replaces Task Scheduler and launchd entirely, and with them the `findDotEnv` problem (it walks up from CWD; a Windows task starts in `System32`)

### Failure visibility (§E.7)
- [ ] `BackupStatus` + `.last-success.json` per target + rotating `backup.log`
- [ ] `LoginResult`/`MeResult` gain `BackupWarning`, beside the existing `HasOverdue`
- [ ] Three surfaces: admin sign-in, **every** user's sign-in (naming the admins to tell, names only — never student numbers, per CLAUDE.md §7), and a banner on the backup screen

### UI (§E.9) — no admin ever edits a file
- [ ] New **Settings** tab: folders with a validator, retention, schedule hour, a card per target with Connect / Test connection
- [ ] **Backup** tab gains the staleness banner, per-target last-run table, restore-by-upload, restore-by-date, restore-photos, and a `backup.log` tail

### Docs
- [ ] `docs/BACKUP-SETUP.md` from §F: the click-by-click Drive and GitHub walkthroughs, including the "Google hasn't verified this app" screen and the fine-grained-PAT permission
- [ ] README backup section; CLAUDE.md §11 kept in step

### Verification (§G)
- [ ] Unit tests incl. the token-redaction case
- [ ] **The round-trip**: `supabase db reset` → restore → row counts match the manifest, `assets_asset_tag_seq` resumes at **the captured value, not 1** (read it before backing up; it was 89 when Phase 7 was specced and 130 the next day, so never hard-code it), no spurious `activity_log` rows, seeded accounts still sign in. Cover both sequence states — a used sequence over a populated table, and a never-read one (`is_called = false`) over an empty table, where `nextval` must return the start value and not start + 1. Closes CLAUDE.md §11's outstanding restore test
- [ ] Both targets at once; break one, confirm the other still succeeds and the status names which failed
- [ ] A config-only run-through from a fresh clone, without opening a text editor

## Phase 8: Kits (Week 9, only if everything above is done)
- [ ] `ListKits`, `CreateKit`, `UpdateKit`, `DeleteKit`, `AddAssetToKit`, `RemoveAssetFromKit`
- [ ] Cart can add a kit → expands to its assets; `CheckOutAssets` handles it unchanged (one custody row per asset)
- [ ] `CheckInKit` convenience (check in every asset in the kit)

---

## Deferred / out of scope (tables stay, nothing built)
Bookings & double-booking prevention · locations · tags · saved filters · `custom_fields` · LAN/multi-station access · email/SMS · GPS. See CLAUDE.md §2.

## Already done (for reference)
- [x] Postgres schema applied (12 tables, 2 views, 4 enums). `supabase/migrations/`
- [x] Sample seed data. `supabase/seed.sql` (rewritten in Phase 1: 63-node category tree, 12 assets, admin + student)
- [x] Proof-of-chain admin screen via supabase-js in the Wails app (`desktop-app/frontend/src/lib/db.ts`). To be deleted in Phase 6
- [x] The macOS start script tested and working; the Windows one written but untested on real Windows (both now `scripts/dev.sh` / `dev.ps1`)
- [x] Product flow, auth model, roles, scope, and Go-backend architecture decided (CLAUDE.md §13, 2026-09-04)
