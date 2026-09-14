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

  it('does not reach the network before anyone signs in', async () => {
    render(App)
    await screen.findByText('Scan your student ID.')
    expect(fetch).not.toHaveBeenCalled()
  })
})
