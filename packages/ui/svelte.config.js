import { vitePreprocess } from '@sveltejs/vite-plugin-svelte'

// The package ships raw .svelte source; each app's Vite compiles it
// (design-system.md §2.2, "No build step"). This config exists only so
// svelte-check and editor tooling know how to read `lang="ts"` blocks.
export default { preprocess: vitePreprocess() }
