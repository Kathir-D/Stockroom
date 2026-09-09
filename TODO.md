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
- [x] Update `scripts/start-mac.sh` and `scripts/start-windows.ps1` to also launch `go run ./server` and kill it on exit (Windows script still untested on real Windows)

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
- [ ] `GetCategoryTree()`. Full tree in one call (Type → Category → Model) for the left-side filters
- [ ] `ListAssets(filter)`. Filter by any category node (includes descendants), status, free-text (existing GIN index); returns category path + photo URL per asset
- [ ] `GetAsset(id)`. Detail popup payload incl. current custodian (if checked out) and category path
- [ ] Static file serving: `GET /files/…` from `UPLOADS_DIR`
- [ ] HTTP: `GET /categories/tree`, `GET /assets?…`, `GET /assets/{id}`

## Phase 4: Core loop: scan, cart checkout, check-in (Week 6)
- [ ] `ScanItem(actor, serial)`. The one function behind every item scan:
  - asset `checked_out` → `CheckInAsset` immediately, return `{action: "checked_in", …}`
  - asset `available` → return `{action: "detail", asset}` (same payload as `GetAsset`)
  - asset `unavailable` → return `{action: "detail", asset}` with a flag so the UI can't add it to the cart
  - unknown serial → `ErrNotFound`
- [ ] `CheckOutAssets(actor, custodianID, assetIDs[], dueAt, overrideOverdue)`. Single transaction:
  - non-admin: `custodianID` must equal actor; admin may pick anyone
  - `dueAt` > now and ≤ now + 7 days (server-enforced)
  - refuse with `ErrOverdueBlocked` if custodian has any row in `overdue_custody`, unless admin passes `overrideOverdue`
  - every asset must be `available` (else `ErrConflict`, whole cart fails)
  - insert one `custody_events` row per asset (`checked_out_by = actor`), set `assets.status = 'checked_out'`
- [ ] `CheckInAsset(actor, assetID, damageNote?)`. Close the open `custody_events` row (`checked_in_by = actor`, `condition_in = note`), set status back to `available`; also callable directly by admin
- [ ] `ListActiveCustody()`, `ListOverdueCustody()`. Over the existing views, joined with custodian names
- [ ] `GetAssetHistory(assetID)`, `GetUserHistory(userID)`. Full custody trail (non-admin may only read their own)
- [ ] HTTP: `POST /scan`, `POST /checkout`, `POST /assets/{id}/checkin`, `GET /custody/active`, `GET /custody/overdue`, `GET /assets/{id}/history`, `GET /users/{id}/history`

## Phase 5: Admin panel API (Week 7)
- [ ] `CreateAsset`, `UpdateAsset`, `DeleteAsset` (block if open custody), `SetAssetStatus` (`available` ⇄ `unavailable`; cannot touch `checked_out`), asset photo upload → `UPLOADS_DIR/assets/`
- [ ] `CreateCategory`, `UpdateCategory`, `DeleteCategory` (block if it has children or assets), enforce max depth 3
- [ ] Overdue list is `ListOverdueCustody` (Phase 4). Just ensure it's admin-panel friendly (custodian name, student number, days overdue)
- [ ] `BackupNow()` → calls Phase 7's export, returns the folder written
- [ ] HTTP: `POST/PUT/DELETE /assets…`, `POST /assets/{id}/photo`, `POST/PUT/DELETE /categories…`, `POST /admin/backup`
- [ ] Validate everything via `curl`/a small Go test file before UI work starts

## Phase 6: Frontend wiring (Week 8, alongside UI build)
- [ ] `desktop-app/frontend/src/lib/api.ts` and `web-app/src/lib/api.ts`. Identical thin `fetch` wrappers, one function per endpoint, session token handling
- [ ] `lib/scanner.ts` in both. Keystroke buffer + scan-vs-typed detection (CLAUDE.md §10); routes to login or `/scan` depending on active screen
- [ ] Cart = frontend-only state (list of asset IDs); checkout calls `POST /checkout` once
- [ ] Sign-out prompt after successful checkout; idle-timeout handling on 401
- [ ] Delete `desktop-app/frontend/src/lib/supabase.ts` and `db.ts`; remove `@supabase/supabase-js` from `desktop-app/frontend/package.json`; strip `Greet` from `app.go`
- [ ] Wails `wails.json` / dev config: make sure the frontend can reach `http://127.0.0.1:8080` (CORS on the Go server for the Vite dev origins)

## Phase 7: Backup (Week 8)
- [ ] `ExportAllTablesToCSV(dir)` in `internal/stockroom/backup.go`. `COPY … TO STDOUT WITH CSV HEADER` per table via pgx, into `BACKUP_DIR/<yyyy-mm-dd>/`
- [ ] `cmd/backup/main.go`. Loads `.env`, runs the export, exits non-zero on failure
- [ ] Scheduling docs in README: Windows Task Scheduler entry; launchd plist / cron line for macOS
- [ ] Restore test (CLAUDE.md §11): scratch DB → migrations → load CSVs → row counts match

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
- [x] `scripts/start-mac.sh` tested and working; `start-windows.ps1` written but untested on real Windows
- [x] Product flow, auth model, roles, scope, and Go-backend architecture decided (CLAUDE.md §13, 2026-09-04)
