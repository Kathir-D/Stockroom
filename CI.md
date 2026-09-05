# Tests & required checks

Quick reference. Full per-file detail is in [TESTING.md](TESTING.md).

## The test cases at a glance

| Suite | Where | Tool | Cases | What it protects |
|---|---|---|---|---|
| Database | `supabase/tests/*.test.sql` | pgTAP (`supabase test db`) | 267 | schema shape, constraints, FK cascades, triggers, views, search index, grants, seed |
| Go | `internal/stockroom/*_test.go`, `server/*_test.go` | `go test` | 74 | config/`.env` loading, pool + ping failures, error→HTTP mapping, JSON decoding, `/health` |
| Frontend | `desktop-app/frontend/src/**/*.test.ts` | Vitest + Testing Library | 92 | `db.ts` query shapes, category flattening and error unwrapping; the browse screen (filters, detail dialog) and the admin screen's flows |

**433 cases total.** Run everything locally:

```bash
./scripts/test-all.sh
```

Individually:

```bash
supabase start
go test ./... -count=1
supabase test db
npm --prefix desktop-app/frontend test
```

### File-by-file

**Database** — `010_structure` (tables/views/enums/indexes/triggers and the column types the Go structs scan), `020_constraints` (uniqueness, NOT NULL, defaults, every FK delete action), `030_bookings` (both CHECKs + the GiST exclusion constraint), `040_triggers` (`updated_at`, status-change logging), `050_views` (`active_custody` / `overdue_custody`), `060_search` (GIN tsvector index + the `coalesce()` null guard), `070_privileges` (`service_role` vs `anon`, RLS off by design), `080_seed` (`seed.sql` loads coherently).

**Go** — `config_test.go`, `db_test.go`, `errors_test.go`, `types_test.go`, `server/json_test.go`, `server/router_test.go`.

**Frontend** — `src/lib/db.test.ts`, `src/lib/AssetBrowser.test.ts`, `src/App.test.ts`.

---

## Making the tests block a merge

Two pieces: a workflow that runs them on every PR, and a branch protection rule that makes that workflow's check **required**.

### 1. The workflow (already committed)

[`.github/workflows/tests.yml`](.github/workflows/tests.yml) runs on every pull request targeting `main` and on pushes to `main`. It:

1. sets up Go, Node 22 and the Supabase CLI
2. runs `supabase start` (applies all migrations + the seed)
3. `go vet ./...`
4. `go test ./... -count=1` with `STOCKROOM_REQUIRE_DB=1`
5. `supabase test db` (pgTAP)
6. `npm ci`, `npm run check` (svelte-check), `npm test` (Vitest) for `desktop-app/frontend`
7. `npm ci`, `npm run check`, `npm run build` for `web-app` (no tests yet — it must still compile)

`STOCKROOM_REQUIRE_DB=1` matters: without it the Go integration tests *skip* when Postgres is unreachable, so a broken database would look green. In CI they must fail instead.

The job is named **`tests`** — that is the check name you require below.

### 2. Turn on branch protection

The workflow alone does not block anything; GitHub only enforces it once the check is marked required.

**In the web UI:** repo → **Settings** → **Rules** → **Rulesets** → **New branch ruleset**

- Target: **Default branch** (`main`)
- Enable **Require a pull request before merging** (so nothing lands by direct push)
- Enable **Require status checks to pass** → search for and add **`tests`**
- Enable **Require branches to be up to date before merging** (so a PR is re-tested against the latest `main`)
- Set **Enforcement status** to **Active**

> The `tests` check only appears in the search box after the workflow has run at least once. Open a throwaway PR first (or push the workflow to `main`), let it finish, then add the check.

**Or with the `gh` CLI** (classic branch protection — every field below is required by that API):

```bash
gh api -X PUT repos/Kathir-D/Stockroom/branches/main/protection --input - <<'JSON'
{
  "required_status_checks": { "strict": true, "contexts": ["tests"] },
  "enforce_admins": true,
  "required_pull_request_reviews": { "required_approving_review_count": 0 },
  "restrictions": null
}
JSON
```

Set `"enforce_admins": false` if you want to keep the ability to override on your own repo.

### 3. Check it works

```bash
git checkout -b test-ci
git commit --allow-empty -m "Check CI"
git push -u origin test-ci
gh pr create --fill
```

The PR should show the `tests` check running, and **Merge** should stay disabled until it goes green.

---

## Adding new test cases

Put each test with the layer it covers; the CI workflow picks up new files automatically.

### Database (pgTAP)

Add `supabase/tests/NNN_topic.test.sql`, following the existing numbering. The skeleton:

```sql
begin;
create extension if not exists pgtap;
select no_plan();

-- fixtures
insert into assets (id, asset_tag, name) values ('...', 'FIX-001', 'Fixture');

-- assertions
select is(status::text, 'available', 'a new asset defaults to available')
  from assets where asset_tag = 'FIX-001';
select throws_ok(
  $$insert into assets (asset_tag, name) values ('FIX-001', 'dup')$$,
  '23505', null, 'asset_tag is unique');

select * from finish();
rollback;
```

Rules of the road:
- Always wrap in `begin; … rollback;` so the suite leaves the local database untouched.
- Use `no_plan()` rather than `plan(N)`; the count stays out of your way.
- Use fixed UUID prefixes per file (`11111111-…` in `020`, `22222222-…` in `030`, …) so fixtures can't collide.
- Assert *behaviour* (insert and check what happens) over DDL text wherever you can.
- Common SQLSTATEs: `23505` unique, `23503` foreign key, `23502` not null, `23514` check, `23P01` exclusion, `22P02` bad enum value.

Run just this layer with `supabase test db`.

### Go

Add `*_test.go` next to the code, same package. For anything needing Postgres, call the existing helper so the test skips locally without Docker but is mandatory in CI:

```go
func TestSomething(t *testing.T) {
    db := requireTestDB(t) // internal/stockroom; server/ has openTestDB(t)
    // ...
}
```

Prefer table-driven subtests, and assert on the sentinel errors (`errors.Is(err, stockroom.ErrConflict)`) rather than on message text.

### Frontend

Add `*.test.ts` under `desktop-app/frontend/src/` (the Vitest `include` is `src/**/*.test.ts`).

- Logic in `lib/` → mock `./supabase` and assert the query chain, as `src/lib/db.test.ts` does.
- Components → `vi.mock('./lib/db', …)`, `render(Component)`, then drive it with `fireEvent` and assert through `screen` / `within`. Query by role and text, not CSS classes, except where a class is the only way to disambiguate.
- Anything async needs `await waitFor(...)` — the screen tears its form down while `loading` is true, so re-query elements after an action instead of holding a reference.

Run with `npm --prefix desktop-app/frontend test` (or `test:watch` while writing).

### Before opening the PR

```bash
./scripts/test-all.sh
```

Green locally means green in CI, with one exception: CI also runs `go vet` and `svelte-check`, so run those too if you touched Go or Svelte.
