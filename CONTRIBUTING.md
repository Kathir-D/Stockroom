# Contributing to Stockroom

Issues and pull requests are welcome. Before you start on something new, read [ROADMAP.md](ROADMAP.md) for the open work and the [docs/decisions.md](docs/decisions.md) entry for the area you're changing. [CLAUDE.md](CLAUDE.md) is the technical reference, and [CONTEXT.md](CONTEXT.md) fixes the words.

## Prerequisites

| Dependency | Why | Install |
|---|---|---|
| Docker | Runs the Supabase CLI's database, and the optional closet camera | https://www.docker.com/products/docker-desktop/ |
| Go 1.25+ | Builds the server and the desktop app's Go host | https://go.dev/dl/ |
| Node.js 22+ with npm | Builds the frontend | https://nodejs.org/en/download |
| Supabase CLI | Runs the dev database, migrations and database tests | https://supabase.com/docs/guides/cli/getting-started |
| Wails CLI | Builds and runs the desktop app | Installed by `scripts/dev.sh` |
| rclone | Google Drive backups and the sign-in photo wall | https://rclone.org/downloads/ |

Develop on macOS or Linux. On Windows, work inside Ubuntu under WSL 2. `scripts\dev.ps1` exists for native Windows but has never run on it.

## Getting started

```bash
git clone https://github.com/Kathir-D/Stockroom.git
cd Stockroom
./scripts/dev.sh
```

This creates `.env` from `.env.example`, starts the Supabase stack with migrations and seed data, installs dependencies, and starts the API server and both frontends. Ctrl+C stops everything and keeps the data.

| Command | Description |
|---|---|
| `./scripts/dev.sh` | Start everything. Flags: `--no-desktop`, `--no-web`, `--no-open` |
| `./scripts/dev.sh deps` | Install npm and Go dependencies and the pre-commit hook |
| `./scripts/dev.sh test` | Run every test suite |
| `./scripts/dev.sh stop` | Stop Supabase and any running Stockroom processes |
| `./scripts/dev.sh status` | Show what is running |

| Service | URL |
|---|---|
| Web app | http://localhost:5173 |
| Desktop app (also opens as a native window) | http://localhost:34115 |
| API health check | http://127.0.0.1:8080/health |
| Supabase Studio (database table editor) | http://127.0.0.1:54323 |

### Test accounts

The development seed (`supabase/seed.sql`) creates these. Sign in by scanning the number, or type it and enter the password.

| Student number | Name | Role | Password |
|---|---|---|---|
| `123456` | Admin Admin | Admin | `password` |
| `234567` | Student Student | Student | `password` |

The seed also has a category tree, sample equipment (some checked out, one unavailable) and one kit. To try the first-scan password prompt, create a user in Admin → Users and scan or enter their number. New users have no password.

> [!WARNING]
> These credentials are public. The seed is for development only, and no install ever loads it.

### Manual setup

```bash
cp .env.example .env         # first time only
./scripts/dev.sh deps        # npm workspace, Go modules, git hooks
supabase start               # Postgres :54322 and Studio :54323, with migrations and seed
go run ./server              # API on :8080
npm run dev:web              # web app on :5173
cd desktop-app && wails dev  # desktop app
supabase stop                # stop the database; data is kept
```

`supabase db reset` reapplies every migration and reloads the seed. It deletes all local data.

Run npm commands from the repository root. It is an npm workspace, and installing inside an app folder creates a second copy of Svelte that breaks the UI. `./scripts/dev.sh deps` detects and fixes this.

## Architecture

The Go server is the only database client. `internal/stockroom` holds every query, transaction and permission check, and `server/` only decodes requests and encodes answers. Both frontends render the same Svelte package and call the server over HTTP on localhost. An install embeds the web UI and every migration in the server binary, which applies pending migrations at startup.

```
internal/stockroom/   business logic, permission checks and all database access
server/               the stockroom binary: HTTP API, embedded web UI, subcommands
internal/setup/       stockroom setup, service and doctor
internal/cli/         stockroom restore
cmd/restore/          the same restore, for go run from a checkout
packages/ui/          Svelte frontend shared by both apps
web-app/              browser app
desktop-app/          Wails desktop app
supabase/             migrations, seed data, pgTAP tests
packaging/            the .deb's systemd unit and maintainer scripts
deploy/               the older checkout installer's files, and camera/
scripts/              get.sh, get.ps1, dev.sh and the older install.sh
examples/             sample category trees, roster and asset list
docs/                 guides, design docs and ADRs
```

Adding a route means a handler in `server/`, a line in `server/router.go`, a function in `packages/ui/src/lib/api/index.ts`, and a row in [docs/api.md](docs/api.md).

## Pull requests

1. Branch from `main`.
2. Run `./scripts/dev.sh deps` once, so the pre-commit hook runs the suite for you.
3. Make sure `./scripts/dev.sh test` passes. [TESTING.md](TESTING.md) explains the suites and [CI.md](CI.md) what CI checks.
4. If you settled a design question, add a dated entry to [docs/decisions.md](docs/decisions.md).
5. Open the PR against `main`.

Report bugs through [GitHub Issues](https://github.com/Kathir-D/Stockroom/issues). For a problem on an install, attach the zip from `stockroom support-bundle`.

## Troubleshooting

| Problem | Fix |
|---|---|
| UI says it cannot reach the server | Run `./scripts/dev.sh status`. Check the API is running, Docker and Supabase are up, and the web app is on port 5173, the only origin the API allows |
| Components render but do not update | A nested `node_modules` is shadowing the workspace. Run `./scripts/dev.sh deps` |
| Port 5173 in use | Free it with `./scripts/dev.sh stop`. Vite won't pick another port, because the API would reject it |
| "Something holds :8080 but does not answer /health" | The server is running without a database. Run `./scripts/dev.sh stop`, then `./scripts/dev.sh` |
