import {beforeEach, describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, waitFor, within} from '@testing-library/svelte'
import AssetBrowser from './AssetBrowser.svelte'
import * as db from './db'
import type {Asset, Category} from './db'

vi.mock('./db', () => ({listCategories: vi.fn()}))
const mocked = vi.mocked(db)

const categories: Category[] = [
  {id: 'c1', name: 'Cameras', parent_id: null},
  {id: 'c2', name: 'Lenses', parent_id: null},
  {id: 'c3', name: 'Zooms', parent_id: 'c2'},
  {id: 'c4', name: 'Primes', parent_id: 'c2'}
]

function asset(over: Partial<Asset> & Pick<Asset, 'id' | 'name'>): Asset {
  return {
    asset_tag: over.id.toUpperCase(),
    description: null,
    status: 'available',
    serial_number: null,
    category_id: null,
    category_name: null,
    subcategory_name: null,
    ...over
  }
}

const camera = asset({id: 'a1', name: 'Sony A7S III', category_id: 'c1', category_name: 'Cameras', serial_number: 'SN-CAM001', description: 'Full-frame mirrorless'})
const zoom = asset({id: 'a2', name: 'Canon 70-200mm', category_id: 'c3', category_name: 'Lenses', subcategory_name: 'Zooms'})
const prime = asset({id: 'a3', name: 'Canon 50mm', category_id: 'c4', category_name: 'Lenses', subcategory_name: 'Primes'})
const loose = asset({id: 'a4', name: 'Mystery cable'})
const all = [camera, zoom, prime, loose]

function rows(): HTMLElement[] {
  return screen.queryAllByRole('button').filter(el => el.tagName === 'DIV')
}
function names(): string[] {
  return rows().map(r => within(r).getByText(/./, {selector: 'span.font-medium'}).textContent!.trim())
}
function select(label: string): HTMLSelectElement {
  return screen.getByLabelText(label) as HTMLSelectElement
}
function options(sel: HTMLSelectElement): string[] {
  return Array.from(sel.options).map(o => o.textContent!.trim())
}

async function renderBrowser(props: Partial<{assets: Asset[]; loading: boolean; error: string | null}> = {}) {
  const utils = render(AssetBrowser, {assets: all, loading: false, error: null, ...props})
  // Categories arrive asynchronously; wait until the filter is populated.
  await waitFor(() => expect(options(select('Category')).length).toBeGreaterThan(1))
  return utils
}

beforeEach(() => {
  vi.clearAllMocks()
  mocked.listCategories.mockResolvedValue(categories as never)
})

describe('listing', () => {
  it('renders one row per asset with category, subcategory and serial', async () => {
    await renderBrowser()
    expect(names()).toEqual(['Sony A7S III', 'Canon 70-200mm', 'Canon 50mm', 'Mystery cable'])

    const zoomRow = rows()[1]
    expect(within(zoomRow).getByText('Lenses')).toBeInTheDocument()
    expect(within(zoomRow).getByText('Zooms')).toBeInTheDocument()
    expect(within(rows()[0]).getByText('SN-CAM001')).toBeInTheDocument()
  })

  it('shows an em dash for missing category, subcategory and serial', async () => {
    await renderBrowser({assets: [loose]})
    expect(within(rows()[0]).getAllByText('—')).toHaveLength(3)
  })

  it('shows the loading state and no rows while loading', async () => {
    render(AssetBrowser, {assets: all, loading: true, error: null})
    expect(screen.getByText('Loading assets…')).toBeInTheDocument()
    expect(rows()).toHaveLength(0)
  })

  // A failed admin action sets `error`; the list must stay usable underneath.
  it('shows an error banner without hiding the list', async () => {
    await renderBrowser({error: 'duplicate key value'})
    expect(screen.getByText(/duplicate key value/)).toBeInTheDocument()
    expect(rows()).toHaveLength(4)
  })
})

describe('filters', () => {
  it('offers only top-level categories in the category select', async () => {
    await renderBrowser()
    expect(options(select('Category'))).toEqual(['All categories', 'Cameras', 'Lenses'])
  })

  it('offers every subcategory until a category is chosen', async () => {
    await renderBrowser()
    expect(options(select('Subcategory'))).toEqual(['All subcategories', 'Zooms', 'Primes'])
  })

  it('narrows to a category and its children', async () => {
    await renderBrowser()
    await fireEvent.change(select('Category'), {target: {value: 'c2'}})

    await waitFor(() => expect(names()).toEqual(['Canon 70-200mm', 'Canon 50mm']))
    expect(options(select('Subcategory'))).toEqual(['All subcategories', 'Zooms', 'Primes'])
  })

  it('narrows to a single subcategory', async () => {
    await renderBrowser()
    await fireEvent.change(select('Category'), {target: {value: 'c2'}})
    await fireEvent.change(select('Subcategory'), {target: {value: 'c3'}})

    await waitFor(() => expect(names()).toEqual(['Canon 70-200mm']))
  })

  it('filters by subcategory alone when no category is chosen', async () => {
    await renderBrowser()
    await fireEvent.change(select('Subcategory'), {target: {value: 'c4'}})
    await waitFor(() => expect(names()).toEqual(['Canon 50mm']))
  })

  // Otherwise a stale subcategory would filter everything out silently.
  it('resets the subcategory when the category no longer contains it', async () => {
    await renderBrowser()
    await fireEvent.change(select('Category'), {target: {value: 'c2'}})
    await fireEvent.change(select('Subcategory'), {target: {value: 'c3'}})
    await fireEvent.change(select('Category'), {target: {value: 'c1'}})

    await waitFor(() => expect(select('Subcategory').value).toBe('all'))
    expect(names()).toEqual(['Sony A7S III'])
  })

  it('excludes uncategorised assets from any category filter', async () => {
    await renderBrowser()
    await fireEvent.change(select('Category'), {target: {value: 'c1'}})
    await waitFor(() => expect(names()).not.toContain('Mystery cable'))
  })

  it('shows an empty state when nothing matches', async () => {
    await renderBrowser({assets: [loose]})
    await fireEvent.change(select('Category'), {target: {value: 'c1'}})
    expect(await screen.findByText('No assets match the current filters.')).toBeInTheDocument()
  })

  it('clears both filters', async () => {
    await renderBrowser()
    await fireEvent.change(select('Category'), {target: {value: 'c2'}})
    await fireEvent.change(select('Subcategory'), {target: {value: 'c3'}})
    await fireEvent.click(screen.getByRole('button', {name: 'Clear filters'}))

    await waitFor(() => expect(names()).toHaveLength(4))
    expect(select('Category').value).toBe('all')
    expect(select('Subcategory').value).toBe('all')
  })

  it('still lists assets when the category fetch fails', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    mocked.listCategories.mockRejectedValue(new Error('offline'))
    render(AssetBrowser, {assets: all, loading: false, error: null})

    await waitFor(() => expect(consoleError).toHaveBeenCalled())
    expect(names()).toHaveLength(4)
    expect(options(select('Category'))).toEqual(['All categories'])
    consoleError.mockRestore()
  })
})

describe('detail dialog', () => {
  it('opens on click with the asset details', async () => {
    await renderBrowser()
    await fireEvent.click(rows()[0])

    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveAccessibleName('Sony A7S III')
    expect(within(dialog).getByText('A1')).toBeInTheDocument() // asset_tag
    expect(within(dialog).getByText('SN-CAM001')).toBeInTheDocument()
    expect(within(dialog).getByText('Cameras')).toBeInTheDocument()
    expect(within(dialog).getByText('Full-frame mirrorless')).toBeInTheDocument()
  })

  it('opens from the keyboard', async () => {
    await renderBrowser()
    await fireEvent.keyDown(rows()[1], {key: 'Enter'})
    expect(await screen.findByRole('dialog')).toHaveAccessibleName('Canon 70-200mm')
  })

  it('omits the description paragraph when there is none', async () => {
    await renderBrowser()
    await fireEvent.click(rows()[3])
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).queryByText(/null/)).not.toBeInTheDocument()
    expect(within(dialog).getAllByText('—')).toHaveLength(3)
  })

  it('closes with the Close button, the backdrop, and Escape', async () => {
    await renderBrowser()

    await fireEvent.click(rows()[0])
    await fireEvent.click(await screen.findByRole('button', {name: 'Close'}))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

    await fireEvent.click(rows()[0])
    await screen.findByRole('dialog')
    await fireEvent.click(screen.getByRole('presentation'))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

    await fireEvent.click(rows()[0])
    await screen.findByRole('dialog')
    await fireEvent.keyDown(window, {key: 'Escape'})
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('does not close when clicking inside the dialog', async () => {
    await renderBrowser()
    await fireEvent.click(rows()[0])
    const dialog = await screen.findByRole('dialog')
    await fireEvent.click(within(dialog).getByText('SN-CAM001'))
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})
