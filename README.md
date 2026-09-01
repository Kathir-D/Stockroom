# Stockroom

Stockroom is a fully local asset checkout/check-in system for a school media department — scan a barcode to check gear in or out, track custody, and prevent double-booking, all without needing an internet connection.

## Dependencies

Install these before running Stockroom:

| Dependency | Purpose | Download |
|---|---|---|
| Go | builds/runs the Wails desktop app | https://go.dev/dl/ |
| Node.js (includes npm) | frontend tooling for both apps | https://nodejs.org/en/download |
| Docker Desktop | runs the local Supabase stack | https://www.docker.com/products/docker-desktop/ |
| Supabase CLI | manages the local Postgres/Auth/REST stack | https://supabase.com/docs/guides/cli/getting-started |
| Wails CLI | builds/runs the desktop app | https://wails.io/docs/gettingstarted/installation |

You don't need to install the Wails CLI by hand — the start script below installs it automatically (via `go install`) if it's missing.

## How to run

### Easiest: one-step script
- **macOS (double-click):** in Finder, double-click `scripts/Start Stockroom.command`. It opens a Terminal window and runs the same script as below. (First time only: macOS Gatekeeper may show an "unidentified developer" warning — right-click the file → **Open** → **Open** to approve it once.)
- **macOS (terminal):**
```
chmod +x ./scripts/start-mac.sh
./scripts/start-mac.sh
```
- **Windows:** right-click `scripts/start-windows.ps1` → **Run with PowerShell** (or run `powershell -ExecutionPolicy Bypass -File scripts\start-windows.ps1` from a terminal)

The script:
1. Checks that every dependency above is installed (and installs the Wails CLI if it's missing)
2. Starts Docker Desktop if it isn't already running
3. Starts the local Supabase stack (Postgres + Auth + REST API + Studio)
4. Installs frontend packages if needed
5. Launches the desktop app and the web app

Press **Ctrl+C** in the script's terminal window to shut everything down — it stops both frontend processes and the local Supabase stack cleanly. Your database data is preserved between runs.

### URLs once it's running

| What | URL |
|---|---|
| Supabase Studio (DB admin UI) | http://127.0.0.1:54323/project/default |
| Web app | http://localhost:5173/ (Vite bumps to 5174, 5175, etc. if that port's busy — check the terminal output for the actual port) |
| Stockroom admin (desktop app, in-browser) | http://localhost:34115/ |

The desktop app also opens as its own native window automatically — the `localhost:34115` URL is Wails' dev server, useful if you want to view/inspect it in a regular browser instead.

### Manual steps

1. `supabase start` (from the repo root) — starts Postgres, Auth, REST API, and Studio (`http://127.0.0.1:54323`)
2. `cd desktop-app && wails dev` — primary UI, opens a native window
3. `cd web-app && npm run dev` — secondary UI, served on `http://localhost:5173`
4. `supabase stop` — stop the local stack when done (data is preserved)

## Adding/editing/removing assets and tags

Two ways to mutate the database:

1. **Stockroom admin panel** (`http://localhost:34115/` or the desktop app's native window) — a plain, unstyled screen for day-to-day use: add/edit/delete assets, add/rename/delete tags globally, and attach/detach tags on individual assets. It's intentionally bare-bones for now (functional first, styled later).
2. **Supabase Studio** (`http://127.0.0.1:54323/project/default` → Table Editor) — the full Postgres table editor, useful for bulk edits or anything the admin panel doesn't cover yet (categories, locations, bookings, etc.).

The admin panel's logic lives in two files:
- `desktop-app/frontend/src/lib/db.ts` — the actual mutation functions (`createAsset`, `updateAsset`, `deleteAsset`, `createTag`, `renameTag`, `deleteTag`, `addTagToAsset`, `removeTagFromAsset`, `listAllAssetTags`, etc.), each a thin wrapper around the Supabase client in `lib/supabase.ts`. Import from here if you're adding new UI or scripting mutations directly.
- `desktop-app/frontend/src/App.svelte` — the screen that calls those functions from forms/buttons.

See `CLAUDE.md` for full architecture, database schema, and project roadmap.
