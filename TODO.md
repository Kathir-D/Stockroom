# Stockroom — Backend/Functionality To-Do

Tracks backend/functionality work only (no UI/layout/styling — UI is being planned separately). See `CLAUDE.md` for full architecture/schema reference.

**Architecture direction:** move all business logic into a shared Go package (`internal/stockroom`), used by two thin callers — the Wails-bound desktop app, and a new standalone Go HTTP server (`server/`) that `web-app`'s TypeScript calls over localhost. Frontend TS shrinks to pass-through wrappers; no Supabase JS client or direct Postgres/PostgREST access from either frontend once this is done.

## Target user flow (functionality this backs)

1. **Sign in** by scanning an ID card (barcode encodes a list of numbers = an ID number) — no password needed on a scan. If someone instead types that same number in by hand, a password is required. Distinguishing "scanned" vs "typed" is an input-timing detail the UI layer owns; the backend just exposes two distinct entry points and trusts which one the frontend calls (acceptable on a single trusted local machine per CLAUDE.md's auth model).
2. Once signed in, see a **filterable list of assets** (type / category / subcategory / year, filters on the left, results on the right — exact filter set may change once UI is finalized).
3. **Click an item** → detail popup → **"Add to Cart"**. Cart = a pending set of assets the frontend is holding, not checked out yet.
4. **Check out the cart** → every item in it transitions to checked-out together.
5. Every physical item has a **barcode = its serial number**. Scanning an item's barcode is the single "track it" action:
   - if that item is currently **checked out** → scanning it **checks it back in immediately**
   - if it's currently **available** → scanning it opens the same detail popup / add-to-cart path as clicking it
6. **Admin panel**, restricted to select accounts (role-gated), can add/edit/remove assets, categories, and subcategories directly.

---

## Phase 0 — Foundation
- [ ] Restructure repo to a single root Go module (or `go.work`) so `desktop-app`, `server/`, and `internal/stockroom` share code
- [ ] Create `internal/stockroom` skeleton: `db.go`, `types.go`, `errors.go` (`ErrNotFound`, `ErrForbidden`, `ErrConflict`, `ErrBookingConflict`)
- [ ] Add `pgx/v5` + `pgxpool`; connect to local Supabase's direct Postgres port (54322) via env var with a local default; verify with a trivial query

## Phase 1 — Schema updates for the new flow
- [ ] Add a scan-id column to `profiles` (e.g. `id_barcode`, unique, nullable) so an ID-card scan can look someone up without a password
- [ ] Add a unique index on `assets.serial_number` — it becomes the primary scan/lookup key for items (barcode → serial number → asset), separate from `asset_tag`
- [ ] **Open decision:** does "Type" (from the filter mockup) become a third level on top of the existing `categories.parent_id` hierarchy (Type → Category → Subcategory, no schema change needed), or a new column on `assets`? Decide before Phase 3/6 category work — revisit once UI filters are finalized
- [ ] "Year" filter can be derived from `assets.purchase_date` — no schema change needed unless a distinct "model year" field turns out to be wanted

## Phase 2 — Auth, sessions, role enforcement
- [ ] `LoginByScan(idBarcode)` — no password check; used when the frontend flags the input as a barcode scan
- [ ] `LoginByPassword(idBarcode_or_email, password)` — used when the same number (or email) is typed manually; bcrypt-hash `profiles.password_hash` (currently unused)
- [ ] `Logout`, `CurrentProfile` — in-memory session map for HTTP server; in-struct current-actor for Wails
- [ ] `RequireRole(actor, minRole)` central gate (`owner` > `executive_producer` > `producer` > `member`, per CLAUDE.md §7) — also the gate for admin-panel access
- [ ] `CreateProfile`, `UpdateProfileRole`, `ListProfiles`, `DeleteProfile` (owner-only)
- [ ] Wire `RequireRole` into every mutating function going forward

## Phase 3 — Browse & filter (read side of the core screen)
- [ ] `ListAssets(filter)` — status/type/category/subcategory/year/free-text filters (full-text via existing GIN index); powers the right-hand item list
- [ ] `ListCategories` — returned as a tree (via `parent_id`) so the left-side Type/Category/Subcategory dropdowns can be populated from one call
- [ ] `GetAsset(id)` — full detail for the click-to-popup view
- [ ] Bind above in `app.go`; verify via `wails dev`
- [ ] Stand up minimal `server/main.go` with the same read endpoints; verify via `curl`

## Phase 4 — Scan-to-track + cart checkout (core loop)
- [ ] `ScanItem(serialNumber)` — the single function behind every item scan:
  - if asset is currently `checked_out` → runs check-in immediately and returns the check-in result
  - otherwise → returns asset detail (equivalent to `GetAsset`, for the popup/add-to-cart path)
- [ ] `CheckOutAssets(actor, custodianID, assetIDs[], dueAt, notes)` — bulk transaction: takes the whole cart at once, inserts one `custody_events` row per asset, sets each `assets.status = 'checked_out'`
- [ ] `CheckInAsset(assetID)` — single-item check-in transaction (used internally by `ScanItem`, also callable directly for admin correction)
- [ ] `ListActiveCustody` / `ListOverdueCustody` — wrappers over existing views (in-app alert mechanism, no email/SMS)
- [ ] `GetAssetCustodyHistory` — full audit trail per asset

## Phase 5 — Admin panel functionality
- [ ] `CreateAsset`, `UpdateAsset`, `RetireAsset` (status change), `DeleteAsset` (hard delete, blocked if open custody/bookings) — all role-gated
- [ ] `CreateCategory`, `UpdateCategory`, `DeleteCategory` — same functions serve category *and* subcategory (subcategory = a category with a `parent_id`)
- [ ] `ListLocations`, `CreateLocation`, `UpdateLocation`, `DeleteLocation`
- [ ] Tag CRUD if still needed alongside the type/category/subcategory filters: `CreateTag`, `RenameTag`, `DeleteTag`, `AddTagToAsset`, `RemoveTagFromAsset`
- [ ] Bind + expose via Wails and HTTP; validate via CLI/curl

## Phase 6 — Bookings/reservations (fast-follow, not in the flow above yet)
- [ ] `CreateBooking` (role-gated; members restricted to `reserved_by = self`); catch Postgres exclusion-violation (`23P01`) → `ErrBookingConflict`
- [ ] `UpdateBooking`, `CancelBooking`, `ListBookings` (by asset/kit/user/date range/status)
- [ ] `MarkOverdueBookings` sweep (no cron yet — run on startup/periodic timer)
- [ ] Wire booking status transitions into Phase 4's checkout/check-in once bookings are in play

## Phase 7 — Kits, activity log, saved filters
- [ ] `ListKits`, `CreateKit`, `UpdateKit`, `DeleteKit`, `AddAssetToKit`, `RemoveAssetFromKit`, `ListKitItems`
- [ ] `CheckOutKit`/`CheckInKit` — one `custody_events` row per asset in the kit
- [ ] `LogActivity` generic insert helper (supplements the existing status-change trigger)
- [ ] `ListActivityForAsset`, `ListRecentActivity`
- [ ] `ListSavedFilters`, `CreateSavedFilter`, `DeleteSavedFilter` (per user)

## Phase 8 — Backup/export
- [ ] `ExportAllTablesToCSV` in Go (`internal/stockroom/backup.go`), replacing the sketched PowerShell script
- [ ] Wails-bound "Backup Now" method
- [ ] Separate `cmd/backup/main.go` CLI for Windows Task Scheduler
- [ ] Full restore test per CLAUDE.md §11 (wipe scratch DB, reapply migrations, reload CSVs, confirm row counts)

## Phase 9 — Cleanup of legacy TS logic
- [ ] Delete `desktop-app/frontend/src/lib/supabase.ts` (remove hardcoded service_role key from TS)
- [ ] Shrink `desktop-app/frontend/src/lib/db.ts` to thin wrappers over Wails-generated bindings (keep function names/signatures so UI call sites don't change)
- [ ] Create `web-app/src/lib/api.ts` — thin `fetch` wrappers over `server/`'s endpoints
- [ ] Remove `@supabase/supabase-js` from `desktop-app/frontend/package.json`
- [ ] Update `scripts/start-mac.sh` / `start-windows.ps1` to also launch `server/` alongside `supabase start` and `wails dev`

---

## Already done (for reference)
- [x] Postgres schema fully applied (12 tables, 2 views, 4 enums) — `supabase/migrations/`
- [x] Sample seed data (10 assets, 5 categories, 2 locations, 1 profile) — `supabase/seed.sql`
- [x] Asset + tag CRUD working end-to-end via TS/Supabase (`desktop-app/frontend/src/lib/db.ts`) — to be replaced by Phases 3/5/9 above
- [x] `scripts/start-mac.sh` tested and working; `start-windows.ps1` written but untested on real Windows
