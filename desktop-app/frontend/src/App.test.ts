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

  it('reaches the network only for the photo wall and the ID format, and survives both failing', async () => {
    render(App)
    await screen.findByText('Scan your student ID.')

    // Signing in must not depend on a request. The only call the sign-in
    // screen is allowed to make is the photo wall's batch, which is decoration
    // (docs/design/signin-photo-wall.html §6) -- and here it rejects, exactly
    // as it does on a machine with no server, while the field still renders.
    const calls = vi.mocked(fetch).mock.calls.map(([url]) => String(url))
    // Both halves matter: that the wall did ask, and that nothing else did.
    // Without the first, `every` passes vacuously on an empty list and the
    // assertion stops meaning anything the day the wall stops fetching.
    expect(calls).toContainEqual(expect.stringContaining('/signin/photos'))
    // The one other allowed call is the student-number format
    // (lib/student-number.ts), which is just as optional: it rejects here too,
    // and the field falls back to digits and still renders.
    const allowed = ['/signin/photos', '/signin/config']
    expect(calls.every((url) => allowed.some((path) => url.endsWith(path)))).toBe(true)
    expect(await screen.findByPlaceholderText(/student number/i)).toBeInTheDocument()
  })
})
