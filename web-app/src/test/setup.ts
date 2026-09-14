import '@testing-library/jest-dom/vitest'
import {cleanup} from '@testing-library/svelte'
import {afterEach} from 'vitest'

// jsdom has no matchMedia, and svelte-sonner asks for prefers-color-scheme the
// moment the Toaster mounts. The app is dark-only for v1 (design-system.md
// §3.5), so a stub that always answers "no match" is the honest reply rather
// than a placeholder that could drift from the real behaviour.
if (!window.matchMedia) {
  window.matchMedia = (query: string) =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false
    }) as MediaQueryList
}

// Unmount anything a test rendered so components can't leak state between tests.
afterEach(() => cleanup())
