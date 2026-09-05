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
  // Under Vitest, resolve packages with the browser condition so Svelte 5
  // components mount against the client runtime inside jsdom.
  resolve: process.env.VITEST ? {conditions: ['browser']} : undefined,
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
