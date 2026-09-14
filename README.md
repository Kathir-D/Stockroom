# Stockroom

Stockroom is a fully local asset checkout/check-in system for a school media department. Scan a barcode to check gear in or out, track custody, and prevent double-booking, all without needing an internet connection.

## Dependencies

Install these before running Stockroom:

| Dependency | Purpose | Download |
|---|---|---|
| Go | builds/runs the API server and the Wails desktop app | https://go.dev/dl/ |
| Node.js (includes npm) | frontend tooling for both apps | https://nodejs.org/en/download |
| Docker Desktop | runs the local Supabase stack | https://www.docker.com/products/docker-desktop/ |
| Supabase CLI | manages the local Postgres/Auth/REST stack | https://supabase.com/docs/guides/cli/getting-started |
| Wails CLI | builds/runs the desktop app | https://wails.io/docs/gettingstarted/installation |

You don't need to install the Wails CLI by hand. The start script below installs it automatically (via `go install`) if it's missing.

## How to run

### Easiest: one-step script
- **macOS (double-click).** In Finder, double-click `scripts/Start Stockroom.command`. It opens a Terminal window and runs the same script as below. (First time only: macOS Gatekeeper may show an "unidentified developer" warning; right-click the file → **Open** → **Open** to approve it once.)
- **macOS (terminal).**
```
chmod +x ./scripts/start-mac.sh
./scripts/start-mac.sh
```
- **Windows.** Right-click `scripts/start-windows.ps1` → **Run with PowerShell** (or run `powershell -ExecutionPolicy Bypass -File scripts\start-windows.ps1` from a terminal)

The script:
1. Checks that every dependency above is installed (and installs the Wails CLI if it's missing)
2. Starts Docker Desktop if it isn't already running
3. Starts the local Supabase stack (Postgres + Studio; the REST/Auth services are unused)
4. Installs frontend packages if needed
5. Creates `.env` from `.env.example` if it doesn't exist yet
6. Launches the Go API server, the desktop app, and the web app

Press **Ctrl+C** in the script's terminal window to shut everything down. It stops the API server, both frontend processes, and the local Supabase stack cleanly. Your database data is preserved between runs.

### URLs once it's running

| What | URL |
|---|---|
| Go API server (health check) | http://127.0.0.1:8080/health |
| Supabase Studio (DB admin UI) | http://127.0.0.1:54323/project/default |
| Web app | http://localhost:5173/ (Vite bumps to 5174, 5175, etc. if that port's busy; check the terminal output for the actual port) |
| Stockroom admin (desktop app, in-browser) | http://localhost:34115/ |

The desktop app also opens as its own native window automatically. The `localhost:34115` URL is Wails' dev server, useful if you want to view/inspect it in a regular browser instead.

### Manual steps

1. `cp .env.example .env` (first time only) and fill in the values. See `CLAUDE.md` §9
2. `./scripts/ensure-deps.sh` — installs the npm workspace and the Go modules (first time, and after any dependency change). Plain `npm install` from the repo root does the npm half
3. `supabase start` (from the repo root). Starts Postgres and Studio (`http://127.0.0.1:54323`)
4. `go run ./server` (from the repo root). The API; check `curl http://127.0.0.1:8080/health`
5. `cd desktop-app && wails dev`. Primary UI, opens a native window
6. `npm run dev:web`. Secondary UI, served on `http://localhost:5173`
7. `supabase stop`. Stop the local stack when done (data is preserved)

> **Run every npm command from the repo root.** This is an npm workspace — `packages/ui` (all the
> frontend code), `web-app` and `desktop-app/frontend` share one `package-lock.json` and one installed
> tree. `npm install` inside one of the apps gives that app its own copy of Svelte and Vite, and the
> failure is nasty because it is silent: components render, but their state stops updating.
>
> `./scripts/ensure-deps.sh` checks for that and repairs it, and the start scripts and `test-all.sh`
> all call it, so you rarely have to think about it. (A `node_modules/.vite` under an app is *not* the
> problem — that is just Vite's dependency cache.)

The Go module lives at the repo root (`go.mod`), so `go build ./...` from the root builds the server, the desktop app's Go side, and `internal/stockroom` together.

## Tests

Run every suite (Go, pgTAP against the local Postgres, and the frontend's Vitest tests) with:

```bash
./scripts/test-all.sh
```

The database suites need `supabase start` to have been run first. The suite is deliberately small (one happy path and one permission gate per module); [TESTING.md](TESTING.md) lists what runs and what was cut, and [CI.md](CI.md) covers the GitHub Actions job and branch protection.

## Adding/editing/removing assets and tags

Two ways to mutate the database:

1. **Stockroom admin panel** (`http://localhost:34115/` or the desktop app's native window). A plain screen for day-to-day use: add/edit/delete assets, add/rename/delete tags globally, and attach/detach tags on individual assets. It's intentionally bare-bones for now (functional first, styled later).
2. **Supabase Studio** (`http://127.0.0.1:54323/project/default` → Table Editor). The full Postgres table editor, useful for bulk edits or anything the admin panel doesn't cover yet (categories, locations, bookings, etc.).

The admin panel's logic lives in two files:
- `desktop-app/frontend/src/lib/db.ts`. The actual mutation functions (`createAsset`, `updateAsset`, `deleteAsset`, `createTag`, `renameTag`, `deleteTag`, `addTagToAsset`, `removeTagFromAsset`, `listAllAssetTags`, etc.), each a thin wrapper around the Supabase client in `lib/supabase.ts`. Import from here if you're adding new UI or scripting mutations directly.
- `desktop-app/frontend/src/App.svelte`. The screen that calls those functions from forms/buttons.

See `CLAUDE.md` for full architecture, database schema, and project roadmap.
