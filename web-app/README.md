# Stockroom web app

The browser host for Stockroom. `src/App.svelte` renders `<StockroomApp>` from [`@stockroom/ui`](../packages/ui) and contains nothing else.

## Development

Start the API server first (`go run ./server` from the repository root, or `./scripts/dev.sh`), then from the repository root:

```bash
npm run dev:web
```

The app runs at http://localhost:5173. The port is fixed with `--strictPort` because the API's CORS allow-list only includes 5173.

Run npm commands from the repository root. It is an npm workspace, and a separate install in this directory creates a second copy of Svelte.

## Production build

```bash
npm run build --workspace=web-app
```

The build is written to `dist/` and embedded into the server binary by `embed.go`. The server serves it at `/`, from the same origin as the API. Bundled assets go under `static/` rather than Vite's default `assets/`, because `/assets` is an API route.

## API address

In development the app calls `http://127.0.0.1:8080`. In a production build it calls the origin it was loaded from. Set `VITE_API_BASE_URL` to override either.
