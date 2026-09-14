import {defineConfig, type Plugin} from 'vitest/config'
import {svelte} from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'
import {mkdirSync, writeFileSync} from 'node:fs'
import {resolve} from 'node:path'

// desktop-app/main.go embeds `all:frontend/dist`, so the directory has to
// exist on a fresh clone for `go build ./...` to work. A tracked .gitkeep
// provides that, but Vite empties dist on every build; put it back so the
// placeholder never shows up as a deletion in git.
function keepDistPlaceholder(): Plugin {
  return {
    name: 'keep-dist-placeholder',
    apply: 'build',
    closeBundle() {
      const dir = resolve(__dirname, 'dist')
      mkdirSync(dir, {recursive: true})
      writeFileSync(resolve(dir, '.gitkeep'), '')
    }
  }
}

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [tailwindcss(), svelte(), keepDistPlaceholder()],
  resolve: {
    // MANDATORY (design-system.md §2.2). @stockroom/ui ships raw .svelte, so
    // without this the package can resolve its own copy of Svelte and the
    // failure is silent: components render but their state never updates.
    dedupe: ['svelte'],
    // Under Vitest, resolve packages with the browser condition so Svelte 5
    // components mount against the client runtime inside jsdom.
    ...(process.env.VITEST ? {conditions: ['browser']} : {})
  },
  // It's source in this workspace, not a prebuilt dependency; prebundling it
  // would break hot reload across the package boundary.
  optimizeDeps: {exclude: ['@stockroom/ui']},
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.ts'],
    coverage: {
      provider: 'v8',
      include: ['src/lib/**/*.ts'],
      reporter: ['text', 'html']
    }
  }
})
