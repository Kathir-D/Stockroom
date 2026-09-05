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

Everything here tests code that exists today. Nothing in `TODO.md` Phases 1–8 is
tested ahead of being written.

## Running individual suites

```bash
supabase start          # required by the database suites
go test ./...           # add STOCKROOM_REQUIRE_DB=1 to fail instead of skip
supabase test db
npm --prefix desktop-app/frontend test
```

Go's database-backed tests skip themselves when Postgres is unreachable so
`go test ./...` still works without Docker. `scripts/test-all.sh` sets
`STOCKROOM_REQUIRE_DB=1` whenever the database *is* up, so a skip can never be
mistaken for a pass. CI should set it unconditionally.

Coverage report for the frontend: `npm --prefix desktop-app/frontend run test:coverage`.

## What is covered

### Database (`supabase/tests/`)

The schema is the largest piece of real logic in the repo — constraints,
triggers and views all enforce behaviour the Go code will depend on.

- **`010_structure`** — every table, view, enum, primary key, index, trigger and
  function from the init migration; the column names and types that the Go row
  structs in `internal/stockroom/types.go` scan into; nullability.
- **`020_constraints`** — uniqueness (`asset_tag`, `email`, category/tag names),
  NOT NULL, enum input rejection, column defaults, and the full set of foreign
  key behaviours: what cascades when an asset/tag/kit is deleted, what is set to
  null when a category or location is deleted, and that a profile with custody
  history *cannot* be deleted.
- **`030_bookings`** — the two CHECK constraints and the GiST exclusion
  constraint: overlapping reservations are refused, back-to-back ones are
  allowed (the range is half-open), cancelled/returned bookings are exempt, and
  kit bookings are outside the constraint.
- **`040_triggers`** — `updated_at` is stamped on update and cannot be
  backdated by a client, is untouched on insert; `log_asset_status_change`
  writes exactly one row per real status change with `{from, to}`, logs nothing
  for same-value or non-status updates, and logs one row per asset on a bulk
  update (the shape a cart checkout will produce).
- **`050_views`** — `active_custody` shows exactly the open events with the
  asset name/tag joined; `overdue_custody` excludes future due dates, null due
  dates, and items returned late; checking an item in drops it from both.
- **`060_search`** — `idx_assets_search` is a GIN index over the tsvector
  expression, search matches name/description/serial with stemming, and — the
  regression this file exists for — an asset with a null description and null
  serial is still searchable, because of the `coalesce()` calls.
- **`070_privileges`** — `service_role` keeps full access, `anon` and
  `authenticated` have none (the Supabase REST API is still listening on
  54321), default privileges cover future tables, and RLS is off by design.
- **`080_seed`** — `supabase/seed.sql` loads coherently: all ten assets with
  serials/categories/locations, the category list, the nested closet location,
  the admin profile, and a spread of statuses.

Each file runs inside a transaction that is rolled back, so the suite leaves no
trace in the local database.

### Go

- **`config_test.go`** — defaults, environment overrides, `.env` loading and the
  upward search (nearest file wins, a directory named `.env` is ignored), real
  environment beating `.env`, `SESSION_IDLE_MINUTES` validation, and the
  non-obvious case that an exported-but-empty variable shadows the `.env` value.
- **`db_test.go`** — `Open` rejects malformed URLs and fails on an unreachable
  database rather than deferring the error to the first request; pool sizing;
  `Ping` errors (instead of panicking) on a closed pool and honours context
  cancellation.
- **`errors_test.go`** — the sentinels are distinct and survive both
  `fmt.Errorf("%w")` and `errors.Join`, which is what the HTTP mapping relies on.
- **`types_test.go`** — `Profile` JSON never contains the password hash;
  nullable columns marshal as `null`; `ActiveCustody` flattens its embedded
  event; and an integration test that compares the Go enum constants against
  `pg_enum` so the two cannot drift.
- **`server/json_test.go`** — the sentinel → status mapping for all eight
  errors, wrapped and joined errors, that an unknown error is a 500 whose body
  leaks nothing, request decoding (unknown fields, malformed JSON, wrong types,
  empty body) and the 1 MB size cap.
- **`server/router_test.go`** — `/health` returns the documented body against a
  live database, returns 500 when the database is gone, answers HEAD, and the
  router 405s the wrong method and 404s unknown paths.

### Desktop frontend

- **`src/lib/db.test.ts`** — every helper's PostgREST query shape (table,
  columns, filters, ordering) against a recording fake, the `{data, error}`
  unwrapping (errors become thrown `Error`s carrying the database message, null
  data becomes an empty list), and the `listAllAssetTags` grouping including
  null embedded rows, ordering, and empty payloads.
- **`src/lib/AssetBrowser.test.ts`** — the browse screen: category → subcategory
  filtering (including the stale-subcategory reset and uncategorised assets),
  empty/loading/error states, and the detail dialog (open by click or keyboard,
  close by button, backdrop, or Escape).
- **`src/App.test.ts`** — the admin screen: initial load, create/edit/delete
  asset flows, the edit/cancel state machine, delete confirmations (including
  declining), tag create trimming and blank rejection, and that the "add tag"
  dropdown hides tags the asset already has (which would otherwise violate the
  `asset_tags` primary key).

## Bug found and fixed

`App.svelte`'s "Add" tag button never worked. Svelte 5's legacy compiler turned
`addTagSelection[assetId] = ''` into an invalidation referencing the `asset`
each-block variable from outside its scope, so every add threw
`ReferenceError: asset is not defined` after the row had already been inserted —
the user saw an error and a stale list. Replacing the mutation with a
reassignment fixes it; `src/App.test.ts` covers it.

## Deliberately not tested

- **`web-app/`** — still the unmodified Vite/Svelte starter (a logo page and a
  counter). There is no application code to test yet; it gets a suite when
  Phase 6 gives it a real `lib/api.ts` and screens.
- **`server/main.go`** — process wiring only (signal handling, `ListenAndServe`,
  graceful shutdown). Testing it would mean testing the standard library.
- **`desktop-app/main.go` and `app.go`** — the Wails window host and the
  scaffold `Greet` method, both slated for removal in Phase 6.
- **`internal/stockroom/doc.go`, `types.go` struct definitions** — no behaviour
  beyond the JSON tags and enum values already asserted.
- **`supabase/migrations/20260826180000_grant_service_role.sql`'s
  `alter default privileges` for sequences and functions** — the tables case is
  covered; the other two have nothing reading them.
- **Everything in `TODO.md` Phases 1–8** — auth, sessions, checkout, check-in,
  scanning, the admin API and the backup CLI do not exist yet.
- **Barcode scanner input handling** — `lib/scanner.ts` is not written, and the
  keystroke-timing threshold needs real hardware to pin down (CLAUDE.md §10).
