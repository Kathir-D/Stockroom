# CI

One GitHub Actions workflow, [`.github/workflows/tests.yml`](.github/workflows/tests.yml), runs on every pull request against `main` and every push to `main`. It has three jobs: `tests` (Ubuntu), `tests-postgres` and `package-linux`. A new push to a pull request cancels the run already in progress. [`TESTING.md`](TESTING.md) describes the suites themselves.

## `tests` (Ubuntu)

1. Check out and detect whether any code changed (see [Docs-only changes](#docs-only-changes)).
2. Set up Go, Node 22 and the Supabase CLI.
3. `supabase start`, which applies every migration and the seed.
4. `go vet ./...`, then `go test ./... -count=1 -p 1` with `STOCKROOM_REQUIRE_DB=1`.
5. `supabase test db` (pgTAP).
6. `npm ci` at the root, then `npm run check`, `npm test` and `npm run build` across the workspace.
7. `supabase stop`.

## `tests-postgres`

A matrix over the `postgres:14`, `15`, `16` and `17` service containers, one job each. It builds the binary, starts it against the empty database so it applies the migrations itself, waits for `/health`, loads `supabase/seed.sql` with `psql`, and runs `go test ./... -count=1 -p 1`. 14 is the oldest version Stockroom supports, and the `.deb` depends on `postgresql (>= 14)`.

## `package-linux`

A matrix of two, `ubuntu-24.04` and `debian-12`. Each builds two `.deb` packages with `goreleaser release --snapshot`. The second is the first plus a dummy migration. Then it runs [`packaging/linux/smoke.sh`](packaging/linux/smoke.sh), which:

1. installs the first package with apt,
2. runs `stockroom setup --non-interactive` with a failsafe admin, then again to check a second run repairs rather than reinstalls,
3. restarts the service and checks `/health`,
4. installs the second package over the first and checks a dump appeared in `backups/pre-migrate/`,
5. runs `stockroom doctor`, which must exit 0.

The `ubuntu-24.04` variant runs on the runner itself, a full VM with systemd, so it tests the real service. `debian-12` runs in a container with no systemd, so it uses `setup --no-service` and starts `stockroom serve` by hand. On a failure the ubuntu variant prints the service's journal.

To run it locally, build the packages the same way and use a throwaway container: `docker run --rm -v "$PWD/pkgs:/pkgs:ro" -v "$PWD/packaging/linux/smoke.sh:/smoke.sh:ro" debian:12 bash -c 'apt-get update && apt-get install -y curl procps && bash /smoke.sh /pkgs/first.deb /pkgs/second.deb --no-service'`.

## Releases

[`.github/workflows/release.yml`](.github/workflows/release.yml) runs on a `v*` tag. Its `build` job has a read-only token and runs `goreleaser release --clean --skip=publish`, which builds the binaries, both `.deb` files, `checksums.txt` and the Homebrew cask into `dist/`. It checks the Linux binary runs `stockroom version`. The `publish` job has `contents: write` and runs no third-party code, so dependency scripts never see a token that can write. That split protects the tokens, not the files: the binaries, packages and cask are all built in the job that ran `npm ci`, and what guards their contents is the hashes `package-lock.json` pins. It creates the GitHub release with `gh`, marking a tag with a hyphen (`v0.9.0-rc.1`) as a prerelease so `releases/latest` skips it, and commits the cask to [`Kathir-D/homebrew-tap`](https://github.com/Kathir-D/homebrew-tap), the tap it shares with Sonar and headless-spotify, for a full release.

The tap push needs `TAP_DEPLOY_KEY`, the private half of a deploy key on `Kathir-D/homebrew-tap` with write access. A deploy key reaches that one repository and nothing else. (`HOMEBREW_TAP_TOKEN` in the build job is a placeholder the GoReleaser cask template reads; it is never used to push.) Running the workflow by hand (`workflow_dispatch`) builds a snapshot and publishes nothing. Try the same locally with `goreleaser release --snapshot --clean`.

### Publishing a release, step by step

The tap, its deploy key and the `TAP_DEPLOY_KEY` secret are already set up. To replace the key: `ssh-keygen -t ed25519 -N "" -f key`, then `gh repo deploy-key add key.pub -R Kathir-D/homebrew-tap --allow-write --title "Stockroom release workflow"` and `gh secret set TAP_DEPLOY_KEY -R Kathir-D/Stockroom < key`, and delete both files. Remove the old key under the tap's Settings → Deploy keys.

1. **Try the workflow without publishing.** Actions → release → Run workflow on `main`. It builds a snapshot and publishes nothing. The `dist` artifact holds the cask it would commit.
2. **Tag a release candidate** from `main`: `git tag v0.9.0-rc.1 && git push origin v0.9.0-rc.1`. The release gets the four archives, both `.deb` files and `checksums.txt`, marked as a prerelease. The tap is not touched.
3. **Tag the full release** the same way, for example `v0.9.0`. The publish job then commits `Casks/stockroom.rb` to the tap. Check it with `brew install --cask kathir-d/tap/stockroom && stockroom version` on a Mac.

If the tap step fails, the GitHub release is already up. Fix the secret and re-run the failed job. The re-run finds the release, replaces its files, and goes on to the tap. A re-run with nothing new to commit succeeds without pushing.

The cask is unsigned. Its post-install hook clears the quarantine flag so macOS runs it. `xattr -l $(brew --prefix)/bin/stockroom` should print nothing.

## Docs-only changes

Every step after the change check is conditional on `dorny/paths-filter` reporting a code change. These paths count as code:

`go.mod`, `go.sum`, `internal/`, `server/`, `cmd/`, `supabase/`, `packages/`, `desktop-app/`, `web-app/`, `package.json`, `package-lock.json`, `scripts/`, `.githooks/`, `.github/workflows/`, `examples/`, `packaging/`, `.gitattributes`

`packaging/` is included because a Go test checks the packaged systemd unit matches the one setup writes. `.gitattributes` is included because it decides the line endings a checkout gets, and a Windows checkout with CRLF breaks that same test. `tests-postgres` and `package-linux` also count `.goreleaser.yaml`, and `package-linux` counts `deploy/camera/`, which the `.deb` ships.

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
- Require status checks to pass: `tests`, the four `tests-postgres (…)` jobs and the two `package-linux (…)` jobs
- Require branches to be up to date before merging

A check appears in the search box only after the workflow has run once.

A matrix job reports one check per entry, named like `tests-postgres (14)` and `package-linux (debian-12)`.

With `gh`, as classic branch protection:

```bash
gh api -X PUT repos/Kathir-D/Stockroom/branches/main/protection --input - <<'JSON'
{
  "required_status_checks": { "strict": true, "contexts": ["tests",
    "tests-postgres (14)", "tests-postgres (15)", "tests-postgres (16)", "tests-postgres (17)",
    "package-linux (ubuntu-24.04)", "package-linux (debian-12)"] },
  "enforce_admins": true,
  "required_pull_request_reviews": { "required_approving_review_count": 0 },
  "restrictions": null
}
JSON
```

Set `"enforce_admins": false` to allow repository admins to bypass it.
