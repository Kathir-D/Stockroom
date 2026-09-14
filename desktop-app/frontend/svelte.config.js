import {vitePreprocess} from '@sveltejs/vite-plugin-svelte'

// vitePreprocess, not svelte-preprocess (the Wails scaffold's default).
//
// svelte-preprocess rewrites every .svelte file it sees, including ones inside
// node_modules, and its rewrite of a `lang="ts"` block breaks Svelte 5's rune
// detection: bits-ui's components fail to compile with "$bindable() can only be
// used inside a $props() declaration". vitePreprocess ships with the plugin,
// handles `lang="ts"` through esbuild, and leaves the runes alone.
//
// This also matches web-app and packages/ui, which is the point: a component
// that compiles in one host and not the other is design-system.md §1.4's bug.
export default {preprocess: vitePreprocess()}
