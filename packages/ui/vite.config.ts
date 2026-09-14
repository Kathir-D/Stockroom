import {defineConfig} from 'vitest/config'
import {svelte} from '@sveltejs/vite-plugin-svelte'

// The package ships raw source and has no build of its own (design-system.md
// §2.2); this config exists only so its unit tests have a runner. The apps
// compile packages/ui with their own Vite.
export default defineConfig({
  plugins: [svelte()],
  // Resolve with the browser condition so Svelte 5 components mount against the
  // client runtime inside jsdom, matching both hosts' configs.
  resolve: process.env.VITEST ? {conditions: ['browser']} : undefined,
  test: {
    environment: 'jsdom',
    globals: true,
    include: ['src/**/*.test.ts']
  }
})
