# Desktop app frontend

The Vite frontend for the [Wails desktop app](../README.md). `src/App.svelte` renders `<StockroomApp>` from [`@stockroom/ui`](../../packages/ui) and contains nothing else. All components, screens and the API client live in that package.

Run npm commands from the repository root, not from this directory. The root is an npm workspace, and a separate install here creates a second copy of Svelte.

```bash
npm run dev:desktop                       # Vite dev server (usually started by `wails dev`)
npm run check --workspace=frontend        # svelte-check
npm run test --workspace=frontend         # Vitest smoke tests
```
