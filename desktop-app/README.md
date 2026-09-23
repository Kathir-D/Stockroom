# Stockroom desktop app

The Wails host for Stockroom. It opens a native window that renders `<StockroomApp>` from [`packages/ui`](../packages/ui). The Go side is only a window host. All data goes through the Stockroom API server over HTTP.

## Development

Start the API server first (`go run ./server` from the repository root, or `./scripts/dev.sh`), then:

```bash
cd desktop-app
wails dev
```

The frontend dev server is also reachable in a browser at http://localhost:34115.

Set `VITE_API_BASE_URL` to point the app at a server other than `http://127.0.0.1:8080`.

## Build

```bash
cd desktop-app
wails build
```

The output is written to `build/bin/`. Platform-specific build files are in [`build/`](build/README.md).
