<h1 align="center">Stockroom</h1>

<p align="center">
  Barcode-driven equipment checkout for a school media department.<br>
  Scan an ID card to sign in, scan a sticker to check gear in or out. Runs locally on one machine.
</p>

<p align="center">
  <a href="LICENSE"><img alt="license: AGPL-3.0" src="https://img.shields.io/badge/license-AGPL--3.0-blue.svg"></a>
  <img alt="Go 1.25" src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white">
  <img alt="Svelte 5" src="https://img.shields.io/badge/Svelte-5-FF3E00?logo=svelte&logoColor=white">
  <img alt="PostgreSQL 17" src="https://img.shields.io/badge/PostgreSQL-17-336791?logo=postgresql&logoColor=white">
</p>

## Features

- Sign in by scanning a student ID, or type the number with a password
- Browse equipment by category or search, add items to a cart, check out with a due date (up to 7 days, adjustable in code)
- Scan any checked-out item to return it, with an optional note
- Overdue users are blocked from new checkouts, with an admin override
- Full custody history per item and per user
- Kits: bundles of items that go out and come back together
- Admin panel for items, categories, users and kits, with CSV imports and printable barcode labels and ID cards
- Nightly backups to a local folder, Google Drive or GitHub, with restore from the admin panel
- No internet connection needed for daily use

## Requirements

### Hardware

| Item | Notes |
|---|---|
| A PC that stays on | Runs the server and database. Disable sleep, since nightly backups run inside the server |
| USB barcode scanner | Any HID keyboard-wedge scanner (types the code, then Enter). No drivers needed |
| Label sheets | Stockroom prints Code 128 barcode labels as PDFs for standard sheet sizes |
| Student ID cards with barcodes | Optional. Stockroom can print ID cards, or students can type their number and a password |

### Software

| Dependency | Why | Install |
|---|---|---|
| **Docker** | Runs PostgreSQL | https://www.docker.com/products/docker-desktop/ |
| **Go** 1.25+ | Builds the API server and the desktop app's Go host | https://go.dev/dl/ |
| **Node.js** 22+ (with npm) | Builds the frontend | https://nodejs.org/en/download |
| **Supabase CLI** | Development only. Runs the dev database, migrations and database tests | https://supabase.com/docs/guides/cli/getting-started |
| **Wails CLI** | Development only. Builds and runs the desktop app | Installed automatically by `scripts/dev.sh` |
| `rclone` | Optional. Google Drive backups and the sign-in photo wall | https://rclone.org/downloads/ (see [docs/BACKUP-SETUP.md](docs/BACKUP-SETUP.md)) |

## Installation

Supported on macOS and Linux. Windows does not have an installer yet.

```bash
git clone https://github.com/Kathir-D/Stockroom.git
cd Stockroom
./scripts/install.sh
```

This builds a single binary (API, web UI and migrations), starts a local PostgreSQL container and registers a service that keeps the server running. Open http://127.0.0.1:8080 and the setup wizard walks you through the rest.

Run the same command again to upgrade. It backs up the database first. See [docs/INSTALL.md](docs/INSTALL.md) for options and troubleshooting.

> [!IMPORTANT]
> On macOS the service starts at login, so turn on **System Settings → Users & Groups → Automatic login**. Otherwise Stockroom does not run after a restart.

### First-time setup

The setup wizard covers these steps, and each one is also available in the admin panel:

1. **Failsafe admin.** Set during install, or in the wizard. This account is recreated on every start and is the way back in if every other admin is locked out.
2. **Student-number format.** Digits (default), alphanumeric, or a custom pattern.
3. **Categories.** A three-level tree: Type → Category → Model (e.g. Lenses → Zooms → Canon 70-200mm). Import a whole tree from Admin → Categories → Import.
4. **Equipment.** One row per physical item, each with a unique serial number. Use Admin → Assets → Import CSV, or **Add several** for numbered batches like `T7B-001` to `T7B-040`.
5. **Labels.** Admin → Assets → Print labels. Print at 100% scale on matte labels.
6. **Students.** Admin → Users → Import roster, with columns `first_name,last_name,student_number`. Imported students set a password the first time they scan their card.
7. **Admins.** Edit a user and tick **Administrator**.
8. **Backups.** The installer sets a local backup folder. Add Google Drive or GitHub in Admin → Settings.

Sample files for every import are in [`examples/`](examples/).

## Usage

1. Scan your student ID to sign in.
2. Find an item and add it to your cart.
3. Pick a due date and check out.
4. To return something, scan its sticker from any screen.

Any USB barcode scanner that works as a keyboard (HID keyboard-wedge) is supported. See the [admin guide](docs/ADMIN-GUIDE.md) for setting up equipment, students and labels, and the [student guide](docs/STUDENT-GUIDE.md) for a printable one-pager.

## Configuration

Most settings live in the admin panel. The server reads a few values from `.env`:

| Variable | Description | Default |
|---|---|---|
| `DATABASE_URL` | PostgreSQL connection string | `postgresql://postgres:postgres@127.0.0.1:54322/postgres` |
| `SERVER_ADDR` | Listen address | `127.0.0.1:8080` |
| `ADMIN_STUDENT_NUMBER` | Failsafe admin student number, recreated as an admin on every start | none |
| `ADMIN_PASSWORD` | Failsafe admin password, 8 to 72 characters | none |
| `UPLOADS_DIR` | Photo storage | `./uploads` |
| `SESSION_IDLE_MINUTES` | Idle sign-out timeout, from the last interaction | `10` |
| `BACKUP_DIR`, `PHOTO_BACKUP_DIR`, `RCLONE_REMOTE` | Initial backup settings, read on first start only | none |
| `SIGNIN_PHOTOS_REMOTE` | Name of the sign-in photo wall's rclone remote. The wall itself is turned on by **Sign in with Google** in Admin → Photo wall | `gdrive-photos` |

Values read on first start only are managed in **Admin → Settings** afterwards. See [`.env.example`](.env.example) for the full list.

## Backups

Backups run nightly inside the server and write every table to CSV in a zip, with checksums and optional encryption. Copies can be pushed to Google Drive (via [rclone](https://rclone.org/)) and GitHub. Restore from **Admin → Backup**, or with `go run ./cmd/restore` if no admin can sign in.

See [docs/BACKUP-SETUP.md](docs/BACKUP-SETUP.md).

## Development

```bash
git clone https://github.com/Kathir-D/Stockroom.git
cd Stockroom
./scripts/dev.sh
```

This creates `.env` from `.env.example`, starts the Supabase stack (applying migrations and seed data), installs dependencies, starts the API server and then both frontends. Press Ctrl+C to stop; data is kept between runs. On Windows use `scripts\dev.ps1`.

| Command | Description |
|---|---|
| `./scripts/dev.sh` | Start everything. Flags: `--no-desktop`, `--no-web`, `--no-open` |
| `./scripts/dev.sh deps` | Install npm and Go dependencies and the pre-commit hook |
| `./scripts/dev.sh test` | Run all test suites |
| `./scripts/dev.sh stop` | Stop Supabase and any running Stockroom processes |
| `./scripts/dev.sh status` | Show what is running |

| Service | URL |
|---|---|
| Web app | http://localhost:5173 |
| Desktop app (also opens as a native window) | http://localhost:34115 |
| API health check | http://127.0.0.1:8080/health |
| Supabase Studio (database table editor) | http://127.0.0.1:54323 |

### Test accounts

The development seed (`supabase/seed.sql`) creates these accounts. Sign in by scanning the number, or type it and enter the password.

| Student number | Name | Role | Password |
|---|---|---|---|
| `123456` | Admin Admin | Admin | `password` |
| `234567` | Student Student | Student | `password` |

The seed also includes a category tree, sample equipment (some checked out, one unavailable) and one kit. To try the first-login password prompt, create a user in Admin → Users (new users have no password) and scan or enter their number.

> [!WARNING]
> These credentials are public. The seed is for development only and is never loaded by `install.sh`.

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

`supabase db reset` reapplies all migrations and reloads the seed. It deletes all local data.

Run npm commands from the repository root. It is an npm workspace, and installing inside an app folder creates a second copy of Svelte that breaks the UI. `./scripts/dev.sh deps` detects and fixes this.

### Architecture

The Go server is the only database client. Both frontends render the same Svelte package and talk to the server over HTTP on localhost. In production, the web UI and migrations are embedded in the server binary, which applies pending migrations at startup.

```
internal/stockroom/   business logic, permission checks and all database access
server/               HTTP API; serves the embedded web UI
cmd/restore/          command-line restore
packages/ui/          Svelte frontend shared by both apps
web-app/              browser app
desktop-app/          Wails desktop app
supabase/             migrations, seed data, pgTAP tests
deploy/               production docker-compose and service templates
scripts/              install and dev scripts
examples/             sample category trees, roster and asset list
docs/                 guides, design docs and ADRs
```

More detail: [TESTING.md](TESTING.md), [CI.md](CI.md), [CONTEXT.md](CONTEXT.md) (glossary), [docs/adr/](docs/adr/), [ROADMAP.md](ROADMAP.md) (open work) and [CLAUDE.md](CLAUDE.md) (full technical reference).

## Troubleshooting

| Problem | Fix |
|---|---|
| UI says it cannot reach the server | Run `./scripts/dev.sh status`. Check the API is running, Docker/Supabase is up, and the web app is on port 5173 (the only origin the API allows) |
| Components render but do not update | A nested `node_modules` is shadowing the workspace. Run `./scripts/dev.sh deps` |
| Port 5173 in use | Free it with `./scripts/dev.sh stop`. Vite will not pick another port, because the API would reject it |
| "Something holds :8080 but does not answer /health" | The server is running without a database. Run `./scripts/dev.sh stop`, then `./scripts/dev.sh` |
| A scan asks for a password | The scan was read as typing. Press Ctrl+Shift+D to see key timings and adjust `SCAN_KEY_THRESHOLD_MS` in `packages/ui/src/lib/scanner.ts` |
| A scan does nothing | Scan into a text editor. If nothing appears, the scanner is not in keyboard mode. If no new line appears, configure it to send Enter after each code |
| Locked out of the admin panel | Set `ADMIN_STUDENT_NUMBER` and `ADMIN_PASSWORD` in `.env` and restart the server |
| Cannot delete a user | Users holding items cannot be deleted. Check the items in first |
| Backups reported as stale | Admin → Backup shows the last error for each target and the backup log |

## Contributing

Issues and pull requests are welcome. Run `./scripts/dev.sh deps` to install dependencies and the pre-commit hook, and make sure `./scripts/dev.sh test` passes before opening a PR.

## License

[AGPL-3.0](LICENSE)
