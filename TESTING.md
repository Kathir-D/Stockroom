# Testing

## Running the tests

```bash
./scripts/dev.sh test
```

This runs every suite in the same order as CI. The `.githooks/pre-commit` hook runs the same command before each commit (installed by `./scripts/dev.sh deps`). Docs-only commits skip it. Use `git commit --no-verify` or `STOCKROOM_SKIP_TESTS=1` to skip it manually.

To run one layer at a time, from the repository root:

```bash
supabase start                                       # Go and pgTAP suites need Postgres
./scripts/dev.sh deps                                # npm workspace + Go modules
go vet ./...
STOCKROOM_REQUIRE_DB=1 go test ./... -count=1 -p 1
supabase test db                                     # pgTAP
npm run check                                        # svelte-check
npm test                                             # Vitest
npm run build
```

Notes:

- **`-p 1` is required.** `internal/stockroom` and `server` share one database, and the restore tests truncate every table. Running packages in parallel causes unrelated failures.
- **`STOCKROOM_REQUIRE_DB=1`** makes database tests fail instead of skip when Postgres is unreachable. `dev.sh test` and CI always set it.
- **Run npm from the repository root.** Installing or testing inside an app directory creates a second copy of Svelte.

CI also runs the suite on Windows against a plain PostgreSQL, with the schema applied by the server binary at startup. pgTAP runs on Linux only. See [`CI.md`](CI.md).

## Test layout

### Go: `internal/stockroom`

Each module has a happy-path test and, where it has one, a test that the admin-only gate refuses a student.

| Area | Files |
|---|---|
| Auth and accounts | `auth_test.go`, `users_test.go`, `roster_test.go`, `failsafe_test.go`, `sessions_test.go`, `password_test.go`, `hashcost_test.go`, `student_number_test.go`, `setup_test.go`, `envfile_test.go` |
| Catalogue | `assets_admin_test.go`, `categories_admin_test.go`, `imports_test.go`, `examples_test.go`, `barcode_test.go` |
| Custody and kits | `custody_test.go`, `kits_test.go` |
| Backup and restore | `backup_test.go`, `backup_status_test.go`, `export_test.go`, `archive_test.go`, `restore_test.go`, `settings_test.go`, `photos_backup_test.go` |
| Activity log and closet camera | `activity_test.go` (one row per action and per scan, append-only, admin-only, the log outliving a deleted account), `camera_test.go` (a fake detector: walk-in and walk-out, blips, outages, a forgotten visit, retention and keep; the Frigate connector against Frigate 0.18's real response shapes) |
| Sign-in photo wall | `photowall_test.go`, `photowall_drive_test.go`, `photowall_image_test.go`, `photowall_admin_test.go` |
| Infrastructure | `db_test.go`, `config_test.go`, `migrate_test.go`, `pgerr_test.go` |

`main_test.go` lowers the bcrypt cost for the package. `testdb_test.go` holds shared fixtures that clean up after themselves.

### Go: `server`

HTTP round trips for auth, checkout, kits, admin routes, imports, backup and restore, export, the photo wall routes, the activity and camera routes (`activity_test.go`), CORS, error-to-status mapping and the embedded UI. Each group includes a test that admin routes return 403 to a student.

### Database: `supabase/tests`

| File | Covers |
|---|---|
| `010_structure` | Tables, views, enums, indexes and triggers the Go code depends on |
| `050_views` | `active_custody` and `overdue_custody` |
| `080_seed` | `seed.sql` loads the category tree, accounts, custody rows and the example kit |
| `090_app_settings` | The single-row constraint, defaults and value bounds |

### Frontend

`packages/ui` has Vitest tests for status resolution (`status.test.ts`), scan-vs-typed detection (`scanner.test.ts`), kit expansion (`kits.test.ts`), the kits store (`stores/kits.test.ts`), the session keep-alive (`keep-alive.test.ts`), the activity timeline's URL filter (`stores/router.test.ts`) and student-number filtering (`student-number.test.ts`).

`web-app` and `desktop-app/frontend` each have smoke tests that the shared package compiles under that host and renders the sign-in screen.

## Manual backup checks

| Script | Description |
|---|---|
| `scripts/backup-check.sh` | Signs in as the failsafe admin, shows the backup configuration, and optionally tests each target (`--test`), runs a backup (`--run`), lists stored versions (`--versions`) and verifies the newest archive. `--all` runs everything |
| `scripts/verify-archive.py` | Verifies one archive without a database: checksums, row counts, sequences, `RESTORE.md`, and that no GitHub token is present |

## Manual camera checks

The detector itself is not in CI: Frigate is a 6 GB image. Run it against sample footage on a development machine (`docs/design/closet-camera.md` §5 and §6). Each loop of the EPFL clip gives the same fourteen visits. Then stop the container (`docker stop stockroom-camera-frigate-1`) to check that sign-in still answers and the timeline shows one offline row and one "back online after" row.

## Not yet covered

- Edge and refusal cases for most modules (bad input, conflicts, not-found)
- Concurrency tests under `-race` for the session store and photo uploads
- Most HTTP routes beyond one round trip per group
- pgTAP tests for constraints and triggers
- Frontend unit tests for `groupByModel`, `dueInstant` and the cart store
- A component test for the photo wall's strip builder

## Adding a test

Put the test beside the code it covers. Database-backed Go tests call `requireTestDB` (package) or `testDeps` (server) and use the fixtures in `testdb_test.go` or `server/auth_test.go`.

pgTAP files go in `supabase/tests/NNN_topic.test.sql`:

```sql
begin;
create extension if not exists pgtap;
select no_plan();
-- assertions
select * from finish();
rollback;
```

New test files are picked up automatically.
