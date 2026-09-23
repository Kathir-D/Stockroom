import {render, screen} from '@testing-library/svelte'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'

import App from './App.svelte'

/**
 * The host's whole job is to mount `@stockroom/ui`'s `<StockroomApp>` against
 * the right API base URL, so that is what this proves: the package resolves,
 * compiles under this app's Vite, and reaches its first screen.
 *
 * Deeper behaviour is tested in the package, once, rather than twice here and
 * in the desktop app — duplicating it is exactly the drift `design-system.md`
 * §1.4 is about. What is worth asserting per host is that the wiring holds.
 */
describe('web host', () => {
  beforeEach(() => {
    sessionStorage.clear()
    // No token stored, so the app never calls /me; any stray call fails loudly
    // rather than hanging the render.
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

  // A fresh install has no accounts, and the server says so on the one
  // request sign-in already makes. The screen becomes "create the first admin"
  // (the setup wizard's step 2) instead of asking for an ID nobody has.
  it('offers to create the first admin on an install with no accounts', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string) =>
      String(url).endsWith('/signin/config')
        ? Promise.resolve(new Response(JSON.stringify({
            needs_setup: true, student_number_format: 'digits', student_number_filter: '[^0-9]',
          })))
        : Promise.reject(new Error('no server in tests'))))
    render(App)
    expect(await screen.findByRole('button', {name: 'Create my account'})).toBeInTheDocument()
    expect(screen.queryByText('Scan your student ID.')).not.toBeInTheDocument()
  })
})
