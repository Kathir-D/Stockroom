# CI

One GitHub Actions workflow, [`.github/workflows/tests.yml`](.github/workflows/tests.yml), one job named `tests`. It runs on every pull request against `main` and on every push to `main`. What it runs, and what it deliberately no longer runs, is in [TESTING.md](TESTING.md).

## What the job does

1. Checks out, then decides whether any code changed (see below).
2. Sets up Go, Node 22 and the Supabase CLI.
3. `supabase start`, which applies every migration and the seed.
4. `go vet ./...`, then `go test ./... -count=1` with `STOCKROOM_REQUIRE_DB=1`, so a Go test that would skip on a missing database fails instead.
5. `supabase test db` (pgTAP).
6. One root `npm ci` (locally, `./scripts/ensure-deps.sh` does the same job), then `npm run check`, `npm test` and `npm run build`, each of which fans out across the workspace (`packages/ui`, `web-app`, `desktop-app/frontend`). A per-app `npm ci --prefix` would give that app its own Svelte and Vite, which breaks reactivity silently (`docs/design/design-system.md` §2.2).
7. `supabase stop`, always, if it was started.

A push to a PR cancels the run already going for it.

## Docs-only changes

Every step from 2 onward carries `if: steps.changes.outputs.code == 'true'`. `dorny/paths-filter` sets that output when the PR (or the push) touches `go.mod`, `go.sum`, `internal/`, `server/`, `cmd/`, `supabase/`, `desktop-app/`, `web-app/`, `scripts/` or the workflow itself. Anything else, which in practice means Markdown, `LICENSE`, `docs/` and `Catagories.md`, is a docs-only change (`Catagories.md` only reaches the database when someone rewrites `seed.sql` from it by hand, and that edit is under `supabase/`): the job runs a single echo step and finishes green in a few seconds.

The workflow grants itself `contents: read` and `pull-requests: read`. The second is for `dorny/paths-filter`, which lists a PR's changed files through the API; without it the step fails with "Resource not accessible by integration" before any test runs.

Filtering inside the job rather than with `on.push.paths` matters for branch protection. A workflow that never triggers never reports a check, and a required check that never reports blocks the merge. A job that runs and skips its steps still reports `tests: success`.

## Branch protection

The workflow alone blocks nothing. The check has to be marked required.

In the web UI: Settings, Rules, Rulesets, New branch ruleset. Target the default branch. Turn on "Require a pull request before merging", "Require status checks to pass" (add `tests`), and "Require branches to be up to date before merging". Set enforcement to Active. The `tests` check only appears in the search box after the workflow has run once.

Or with `gh`, as classic branch protection, which needs every field:

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

Set `"enforce_admins": false` to keep the ability to override on your own repo.

## Running the same thing locally

```bash
./scripts/test-all.sh
```

It runs the same steps in the same order, skipping the database suites with a warning if Postgres is not up.
