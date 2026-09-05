# Testing

Three suites, one per layer. Run them all with:

```bash
./scripts/test-all.sh
```

| Layer | Tool | Location | Count |
|---|---|---|---|
| Database schema | pgTAP via `supabase test db` | `supabase/tests/*.test.sql` | 267 assertions |
| Go (config, pool, HTTP) | `go test` | `internal/stockroom/*_test.go`, `server/*_test.go` | 74 cases |
| Desktop frontend | Vitest + Testing Library | `desktop-app/frontend/src/**/*.test.ts` | 92 cases |

Everything here tests code that exists today. Nothing in `TODO.md` Phases 1 to 8 is tested ahead of being written.

## Running individual suites

```bash
supabase start          # required by the database suites
go test ./...           # add STOCKROOM_REQUIRE_DB=1 to fail instead of skip
supabase test db
npm --prefix desktop-app/frontend test
```

Go's database-backed tests skip themselves when Postgres is unreachable, so `go test ./...` still works without Docker. `scripts/test-all.sh` sets `STOCKROOM_REQUIRE_DB=1` whenever the database is up, so a skip can never pass for a success. CI sets it unconditionally.

Frontend coverage report: `npm --prefix desktop-app/frontend run test:coverage`.

## What is covered

### Database (`supabase/tests/`)

The schema is the largest piece of real logic in the repo. Constraints, triggers and views enforce behaviour the Go code will depend on.

**`010_structure`.** Every table, view, enum, primary key, index, trigger and function from the init migration. The column names and types that the Go row structs in `internal/stockroom/types.go` scan into. Nullability.

**`020_constraints`.** Uniqueness (`asset_tag`, `email`, category and tag names), NOT NULL, enum input rejection, column defaults, and the full set of foreign key behaviours. What cascades when an asset, tag or kit is deleted, what is set to null when a category or location is deleted, and that a profile with custody history cannot be deleted.

**`030_bookings`.** The two CHECK constraints and the GiST exclusion constraint. Overlapping reservations are refused, back-to-back ones are allowed because the range is half-open, cancelled and returned bookings are exempt, and kit bookings sit outside the constraint.

**`040_triggers`.** `updated_at` is stamped on update, cannot be backdated by a client, and is untouched on insert. `log_asset_status_change` writes exactly one row per real status change with `{from, to}`, logs nothing for same-value or non-status updates, and logs one row per asset on a bulk update, which is the shape a cart checkout will produce.

**`050_views`.** `active_custody` shows exactly the open events with the asset name and tag joined. `overdue_custody` excludes future due dates, null due dates, and items returned late. Checking an item in drops it from both.

**`060_search`.** `idx_assets_search` is a GIN index over the tsvector expression. Search matches name, description and serial with stemming. An asset with a null description and null serial is still searchable, which is the `coalesce()` regression this file exists for.

**`070_privileges`.** `service_role` keeps full access. `anon` and `authenticated` have none, which matters because the Supabase REST API is still listening on 54321. Default privileges cover future tables for `service_role`. RLS is off by design.

**`080_seed`.** `supabase/seed.sql` loads coherently. All ten assets with serials, categories and locations, the category list, the nested closet location, the admin profile, and a spread of statuses.

Each file runs inside a transaction that is rolled back, so the suite leaves no trace in the local database.

### Go

**`config_test.go`.** Defaults, environment overrides, `.env` loading and the upward search (nearest file wins, a directory named `.env` is ignored), real environment beating `.env`, `SESSION_IDLE_MINUTES` validation, and the non-obvious case that an exported-but-empty variable shadows the `.env` value.

**`db_test.go`.** `Open` rejects malformed URLs and fails on an unreachable database rather than deferring the error to the first request. Pool sizing. `Ping` returns an error instead of panicking on a closed pool and honours context cancellation.

**`errors_test.go`.** The sentinels are distinct and survive both `fmt.Errorf("%w")` and `errors.Join`, which is what the HTTP mapping relies on.

**`types_test.go`.** `Profile` JSON never contains the password hash. Nullable columns marshal as `null`. `ActiveCustody` flattens its embedded event. An integration test compares the Go enum constants against `pg_enum` so the two cannot drift.

**`server/json_test.go`.** The sentinel to status mapping for all eight errors, wrapped and joined errors, an unknown error becoming a 500 whose body leaks nothing, request decoding (unknown fields, malformed JSON, wrong types, empty body) and the 1 MB size cap.

**`server/router_test.go`.** `/health` returns the documented body against a live database, returns 500 when the database is gone, answers HEAD, and the router 405s the wrong method and 404s unknown paths.

### Desktop frontend

**`src/lib/db.test.ts`.** Every helper's PostgREST query shape (table, columns, filters, ordering) against a recording fake. The `{data, error}` unwrapping, where errors become thrown `Error`s carrying the database message and null data becomes an empty list. The category self-join embed and how it flattens into `category_name` and `subcategory_name`. The `listAllAssetTags` grouping, including null embedded rows, ordering, and empty payloads.

**`src/lib/AssetBrowser.test.ts`.** The browse screen. Category to subcategory filtering, including the stale-subcategory reset and uncategorised assets. Empty, loading and error states. The detail dialog opens by click or keyboard and closes by button, backdrop, or Escape.

**`src/App.test.ts`.** The admin screen. Initial load, create, edit and delete asset flows, the edit and cancel state machine, delete confirmations including declining, tag create trimming and blank rejection, and the "add tag" dropdown hiding tags the asset already has, which would otherwise violate the `asset_tags` primary key.

## Bugs found and fixed

The "Add" tag button in `App.svelte` never worked. Svelte 5's legacy compiler turned `addTagSelection[assetId] = ''` into an invalidation that referenced the `asset` each-block variable from outside its scope, so every add threw `ReferenceError: asset is not defined` after the row had already been inserted. The user saw an error and a stale list. Replacing the mutation with a reassignment fixes it, and `src/App.test.ts` covers it.

`listAssets` in the UI import embedded the category parent with a `!categories_parent_id_fkey` hint, which PostgREST rejects, so no assets loaded at all. The self-join has to be the column-as-embed form `parent:parent_id(name)`. `src/lib/db.test.ts` pins the query shape.

## Not tested, and why

`web-app/` has no application code yet beyond a placeholder shell. It gets a suite when Phase 6 gives it a real `lib/api.ts` and screens. CI type-checks and builds it in the meantime.

`server/main.go` is process wiring only (signal handling, `ListenAndServe`, graceful shutdown). Testing it would mean testing the standard library.

`desktop-app/main.go` and `app.go` are the Wails window host and the scaffold `Greet` method, both slated for removal in Phase 6.

`internal/stockroom/doc.go` and the struct definitions in `types.go` have no behaviour beyond the JSON tags and enum values already asserted.

The grant migration's `alter default privileges` for sequences and functions has nothing reading it yet; the tables case is covered.

Everything in `TODO.md` Phases 1 to 8 (auth, sessions, checkout, check-in, scanning, the admin API, the backup CLI) does not exist yet.

Barcode scanner input handling. `lib/scanner.ts` is not written, and the keystroke-timing threshold needs real hardware to pin down (CLAUDE.md §10).
