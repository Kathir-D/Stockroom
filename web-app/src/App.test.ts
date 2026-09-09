import {render, screen} from '@testing-library/svelte'
import {describe, expect, it} from 'vitest'

import App from './App.svelte'

// web-app is still the placeholder shell (TODO Phase 6 builds the real
// screens on lib/api.ts). This suite exists so the harness is wired and
// proven: a fresh clone runs `npm test` here and gets a real pass, and the
// first genuine screen has somewhere to land. Keep it shallow on purpose —
// everything it touches is expected to be replaced.
describe('App shell', () => {
  it('renders the product name', () => {
    render(App)
    expect(screen.getByRole('heading', {name: 'Stockroom'})).toBeInTheDocument()
  })

  it('links to the API health endpoint and Studio', () => {
    render(App)
    expect(screen.getByRole('link', {name: 'API health'})).toHaveAttribute(
      'href',
      'http://127.0.0.1:8080/health'
    )
    expect(screen.getByRole('link', {name: 'Supabase Studio'})).toHaveAttribute(
      'href',
      'http://127.0.0.1:54323'
    )
  })
})
