# CI

One GitHub Actions workflow, [`.github/workflows/tests.yml`](.github/workflows/tests.yml), runs on every pull request against `main` and every push to `main`. It has two jobs, `tests` (Ubuntu) and `tests-windows`. A new push to a pull request cancels the run already in progress. [`TESTING.md`](TESTING.md) describes the suites themselves.

## `tests` (Ubuntu)

1. Check out and detect whether any code changed (see [Docs-only changes](#docs-only-changes)).
2. Set up Go, Node 22 and the Supabase CLI.
3. `supabase start`, which applies every migration and the seed.
4. `go vet ./...`, then `go test ./... -count=1 -p 1` with `STOCKROOM_REQUIRE_DB=1`.
5. `supabase test db` (pgTAP).
6. `npm ci` at the root, then `npm run check`, `npm test` and `npm run build` across the workspace.
7. `supabase stop`.

## `tests-windows`

Differs from the Linux job in three ways:

1. It uses the runner's built-in PostgreSQL service instead of the Supabase CLI (user `postgres`, password `root`, port 5432).
2. It builds `stockroom.exe`, starts it against the empty database and waits for `/health`, so the server applies the schema itself as it does on a production install. It then loads `supabase/seed.sql` with `psql`.
3. It does not run pgTAP.

It also parses `scripts/dev.ps1` with PowerShell's parser.

## Docs-only changes

Every step after the change check is conditional on `dorny/paths-filter` reporting a code change. These paths count as code:

`go.mod`, `go.sum`, `internal/`, `server/`, `cmd/`, `supabase/`, `packages/`, `desktop-app/`, `web-app/`, `package.json`, `package-lock.json`, `scripts/`, `.githooks/`, `.github/workflows/`, `examples/`

`examples/` is included because `examples_test.go` imports every file in it. Any other change finishes in a few seconds with a green result.

The filter runs inside the job rather than through `on.push.paths`, so the job always reports a status. A required check that never reports would block merging.

The workflow has `contents: read` and `pull-requests: read` permissions. The second is needed for `dorny/paths-filter` to list a pull request's changed files.

## Pre-commit hook

`.githooks/pre-commit` runs `./scripts/dev.sh test` before each commit. `./scripts/dev.sh deps` installs it by setting `core.hooksPath` to `.githooks`.

The hook skips docs-only commits using the same path list as the workflow, and counts deletions as changes. Keep the hook's `code_paths` regex and the workflow's filter in sync.

To skip it, use `git commit --no-verify` or set `STOCKROOM_SKIP_TESTS=1`. CI still runs the suite on the pull request.

The hook fails if Postgres is not running. Run `supabase start` first.

## Branch protection

To require the checks, go to Settings → Rules → Rulesets → New branch ruleset, target the default branch, and enable:

- Require a pull request before merging
- Require status checks to pass (`tests` and `tests-windows`)
- Require branches to be up to date before merging

A check appears in the search box only after the workflow has run once.

With `gh`, as classic branch protection:

```bash
gh api -X PUT repos/Kathir-D/Stockroom/branches/main/protection --input - <<'JSON'
{
  "required_status_checks": { "strict": true, "contexts": ["tests", "tests-windows"] },
  "enforce_admins": true,
  "required_pull_request_reviews": { "required_approving_review_count": 0 },
  "restrictions": null
}
JSON
```

Set `"enforce_admins": false` to allow repository admins to bypass it.
