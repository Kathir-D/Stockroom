import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'
import { mkdirSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { defineConfig, type Plugin } from 'vitest/config'

// web-app/embed.go embeds `all:dist`, so the directory has to exist on a fresh
// clone for `go build ./...` to work. A tracked .gitkeep provides that, but
// Vite empties dist on every build; put it back so the placeholder never shows
// up as a deletion in git.
//
// Copied deliberately from desktop-app/frontend/vite.config.ts, which has
// needed exactly this since the Wails app started embedding its own dist. Two
// hosts, one hazard: the failure without it is a `go build ./...` that breaks
// for the next person to clone, long after whoever ran the build has moved on.
function keepDistPlaceholder(): Plugin {
  return {
    name: 'keep-dist-placeholder',
    apply: 'build',
    closeBundle() {
      const dir = resolve(__dirname, 'dist')
      mkdirSync(dir, { recursive: true })
      writeFileSync(resolve(dir, '.gitkeep'), '')
    },
  }
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [tailwindcss(), svelte(), keepDistPlaceholder()],
  resolve: {
    // MANDATORY (design-system.md §2.2). @stockroom/ui ships raw .svelte, so
    // without this the package can resolve its own copy of Svelte and the
    // failure is silent: components render but their state never updates.
    dedupe: ['svelte'],
    // Under Vitest, resolve packages with the browser condition so Svelte 5
    // components mount against the client runtime inside jsdom.
    ...(process.env.VITEST ? { conditions: ['browser'] } : {}),
  },
  // It's source in this workspace, not a prebuilt dependency; prebundling it
  // would break hot reload across the package boundary.
  optimizeDeps: { exclude: ['@stockroom/ui'] },
  build: {
    // NOT Vite's default `assets`. The built bundles are served by the Go
    // server from the same origin as the API (web-app/embed.go), and the API
    // already owns `GET /assets` and `GET /assets/{id}` -- so a default build
    // would put `/assets/index-a1b2c3.js` squarely inside the equipment
    // catalogue's route, and the router would answer a JavaScript request with
    // "asset not found". Renaming the directory is the fix; renaming the API
    // route would change a documented endpoint (CLAUDE.md §8.1) to suit a
    // bundler.
    assetsDir: 'static',
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.ts'],
  },
})
