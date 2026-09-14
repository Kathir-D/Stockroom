import {render, screen} from '@testing-library/svelte'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'

import App from './App.svelte'

/**
 * Same smoke test as `web-app/src/App.test.ts`, and deliberately so: the two
 * hosts render the same component, and the thing worth proving per host is that
 * the package resolves and compiles under *this* app's Vite config. Anything
 * beyond that is tested once, in the package.
 *
 * The old suite here exercised the supabase-js admin screen, which TODO Phase 6
 * deleted along with `lib/db.ts`.
 */
describe('desktop host', () => {
  beforeEach(() => {
    sessionStorage.clear()
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('no server in tests'))))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('mounts the shared app and lands on sign-in', async () => {
    render(App)
    expect(await screen.findByText('Scan your student ID.')).toBeInTheDocument()
  })

  it('does not reach the network before anyone signs in', async () => {
    render(App)
    await screen.findByText('Scan your student ID.')
    expect(fetch).not.toHaveBeenCalled()
  })
})
