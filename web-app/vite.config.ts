import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'vitest/config'

// https://vite.dev/config/
export default defineConfig({
  plugins: [tailwindcss(), svelte()],
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
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.ts'],
  },
})
