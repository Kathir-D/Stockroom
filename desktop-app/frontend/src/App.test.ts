import {beforeEach, describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, waitFor, within} from '@testing-library/svelte'
import App from './App.svelte'
import * as db from './lib/db'

// The screen's own logic is what is under test -- form wiring, the edit/cancel
// state machine, the delete confirmation, and error surfacing -- so the data
// layer is mocked wholesale.
vi.mock('./lib/db', () => ({
  listAssets: vi.fn(),
  listTags: vi.fn(),
  listAllAssetTags: vi.fn(),
  createAsset: vi.fn(),
  updateAsset: vi.fn(),
  deleteAsset: vi.fn(),
  createTag: vi.fn(),
  renameTag: vi.fn(),
  deleteTag: vi.fn(),
  addTagToAsset: vi.fn(),
  removeTagFromAsset: vi.fn()
}))

const mocked = vi.mocked(db)

const camera = {
  id: 'a1',
  asset_tag: 'CAM-001',
  name: 'Sony A7S III',
  description: 'Full-frame mirrorless',
  status: 'available' as const
}
const lens = {
  id: 'a2',
  asset_tag: 'LEN-001',
  name: 'Sigma 24-70mm',
  description: null,
  status: 'checked_out' as const
}
const audioTag = {id: 't1', name: 'audio'}
const videoTag = {id: 't2', name: 'video'}

function seed(overrides: Partial<{assets: unknown[]; tags: unknown[]; assetTags: unknown}> = {}) {
  mocked.listAssets.mockResolvedValue((overrides.assets ?? [camera, lens]) as never)
  mocked.listTags.mockResolvedValue((overrides.tags ?? [audioTag, videoTag]) as never)
  mocked.listAllAssetTags.mockResolvedValue((overrides.assetTags ?? {a1: [audioTag]}) as never)
}

async function renderLoaded() {
  const utils = render(App)
  await screen.findByRole('heading', {name: /Assets \(/})
  return utils
}

beforeEach(() => {
  vi.clearAllMocks()
  seed()
  vi.stubGlobal('confirm', vi.fn(() => true))
})

describe('initial load', () => {
  it('fetches assets, tags and the tag map in one pass', async () => {
    await renderLoaded()
    expect(mocked.listAssets).toHaveBeenCalledTimes(1)
    expect(mocked.listTags).toHaveBeenCalledTimes(1)
    expect(mocked.listAllAssetTags).toHaveBeenCalledTimes(1)
  })

  it('renders every asset with its tag, name and status', async () => {
    await renderLoaded()
    expect(screen.getByRole('heading', {name: 'Assets (2)'})).toBeInTheDocument()
    expect(screen.getByText('CAM-001')).toBeInTheDocument()
    expect(screen.getByText('Sony A7S III')).toBeInTheDocument()

    // Scoped to the row: "checked_out" is also an <option> in every status select.
    const lensRow = screen.getByText('LEN-001').closest('tr') as HTMLElement
    expect(within(lensRow).getByText('checked_out', {selector: 'td'})).toBeInTheDocument()
  })

  it('shows the assigned tags on the right asset only', async () => {
    await renderLoaded()
    const cameraRow = screen.getByText('CAM-001').closest('tr') as HTMLElement
    const lensRow = screen.getByText('LEN-001').closest('tr') as HTMLElement
    // .chip is the assigned-tag badge; the row's "add tag" select lists every
    // other tag by name, so the query has to be narrowed to the chips.
    expect(within(cameraRow).getByText('audio', {selector: '.chip'})).toBeInTheDocument()
    expect(within(lensRow).queryByText('audio', {selector: '.chip'})).not.toBeInTheDocument()
  })

  it('renders a null description as blank rather than "null"', async () => {
    await renderLoaded()
    expect(screen.queryByText('null')).not.toBeInTheDocument()
  })

  it('reports a failed load instead of hanging on "Loading…"', async () => {
    mocked.listAssets.mockRejectedValue(new Error('connection refused'))
    render(App)
    expect(await screen.findByText(/connection refused/)).toBeInTheDocument()
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument()
  })
})

describe('creating an asset', () => {
  it('sends the typed values and refreshes the list', async () => {
    const {container} = await renderLoaded()
    mocked.createAsset.mockResolvedValue({} as never)

    await fireEvent.input(screen.getByPlaceholderText('asset_tag'), {target: {value: 'AUD-003'}})
    await fireEvent.input(screen.getByPlaceholderText('name'), {target: {value: 'Zoom H6'}})
    await fireEvent.input(screen.getByPlaceholderText('description (optional)'), {
      target: {value: 'Recorder'}
    })
    await fireEvent.submit(container.querySelector('form') as HTMLFormElement)

    await waitFor(() =>
      expect(mocked.createAsset).toHaveBeenCalledWith({
        asset_tag: 'AUD-003',
        name: 'Zoom H6',
        description: 'Recorder',
        status: 'available'
      })
    )
    expect(mocked.listAssets).toHaveBeenCalledTimes(2)
  })

  // An empty description must not be written as "" -- the column is nullable.
  it('omits an empty description', async () => {
    const {container} = await renderLoaded()
    mocked.createAsset.mockResolvedValue({} as never)

    await fireEvent.input(screen.getByPlaceholderText('asset_tag'), {target: {value: 'AUD-004'}})
    await fireEvent.input(screen.getByPlaceholderText('name'), {target: {value: 'Mic'}})
    await fireEvent.submit(container.querySelector('form') as HTMLFormElement)

    await waitFor(() =>
      expect(mocked.createAsset).toHaveBeenCalledWith(
        expect.objectContaining({description: undefined})
      )
    )
  })

  it('clears the form after a successful create', async () => {
    const {container} = await renderLoaded()
    mocked.createAsset.mockResolvedValue({} as never)

    await fireEvent.input(screen.getByPlaceholderText('asset_tag'), {target: {value: 'AUD-005'}})
    await fireEvent.input(screen.getByPlaceholderText('name'), {target: {value: 'Mic'}})
    await fireEvent.submit(container.querySelector('form') as HTMLFormElement)

    // refreshAll() flips `loading`, which tears the form down and rebuilds it,
    // so the input has to be looked up again rather than held onto.
    await waitFor(() => {
      const input = screen.getByPlaceholderText('asset_tag') as HTMLInputElement
      expect(input.value).toBe('')
    })
  })

  it('shows the database error and does not refetch when the insert fails', async () => {
    const {container} = await renderLoaded()
    mocked.createAsset.mockRejectedValue(new Error('duplicate key value: assets_asset_tag_key'))

    await fireEvent.input(screen.getByPlaceholderText('asset_tag'), {target: {value: 'CAM-001'}})
    await fireEvent.input(screen.getByPlaceholderText('name'), {target: {value: 'Dup'}})
    await fireEvent.submit(container.querySelector('form') as HTMLFormElement)

    expect(await screen.findByText(/assets_asset_tag_key/)).toBeInTheDocument()
    expect(mocked.listAssets).toHaveBeenCalledTimes(1)
  })
})

describe('editing an asset', () => {
  it('prefills the row inputs and saves the edited values', async () => {
    await renderLoaded()
    mocked.updateAsset.mockResolvedValue({} as never)

    const row = screen.getByText('CAM-001').closest('tr') as HTMLElement
    await fireEvent.click(within(row).getByRole('button', {name: 'Edit'}))

    const editRow = screen.getByDisplayValue('CAM-001').closest('tr') as HTMLElement
    expect(within(editRow).getByDisplayValue('Sony A7S III')).toBeInTheDocument()
    expect(within(editRow).getByDisplayValue('Full-frame mirrorless')).toBeInTheDocument()

    await fireEvent.input(within(editRow).getByDisplayValue('Sony A7S III'), {
      target: {value: 'Sony A7S3'}
    })
    await fireEvent.click(within(editRow).getByRole('button', {name: 'Save'}))

    await waitFor(() =>
      expect(mocked.updateAsset).toHaveBeenCalledWith('a1', {
        asset_tag: 'CAM-001',
        name: 'Sony A7S3',
        description: 'Full-frame mirrorless',
        status: 'available'
      })
    )
  })

  it('only puts one row into edit mode', async () => {
    await renderLoaded()
    const row = screen.getByText('CAM-001').closest('tr') as HTMLElement
    await fireEvent.click(within(row).getByRole('button', {name: 'Edit'}))

    expect(screen.getAllByRole('button', {name: 'Save'})).toHaveLength(1)
    expect(screen.getByText('LEN-001')).toBeInTheDocument()
  })

  it('discards the edit on cancel without calling the database', async () => {
    await renderLoaded()
    const row = screen.getByText('CAM-001').closest('tr') as HTMLElement
    await fireEvent.click(within(row).getByRole('button', {name: 'Edit'}))

    const editRow = screen.getByDisplayValue('CAM-001').closest('tr') as HTMLElement
    await fireEvent.input(within(editRow).getByDisplayValue('Sony A7S III'), {
      target: {value: 'Discarded'}
    })
    await fireEvent.click(within(editRow).getByRole('button', {name: 'Cancel'}))

    await waitFor(() => expect(screen.getByText('CAM-001')).toBeInTheDocument())
    expect(mocked.updateAsset).not.toHaveBeenCalled()
    expect(screen.queryByText('Discarded')).not.toBeInTheDocument()
  })

  // A null description must edit as an empty box, not the string "null".
  it('edits a null description as an empty field', async () => {
    await renderLoaded()
    const row = screen.getByText('LEN-001').closest('tr') as HTMLElement
    await fireEvent.click(within(row).getByRole('button', {name: 'Edit'}))

    const editRow = screen.getByDisplayValue('LEN-001').closest('tr') as HTMLElement
    const inputs = within(editRow).getAllByRole('textbox') as HTMLInputElement[]
    expect(inputs[2].value).toBe('')
  })
})

describe('deleting an asset', () => {
  it('asks for confirmation and names the asset', async () => {
    await renderLoaded()
    mocked.deleteAsset.mockResolvedValue(undefined as never)

    const row = screen.getByText('CAM-001').closest('tr') as HTMLElement
    await fireEvent.click(within(row).getByRole('button', {name: 'Delete'}))

    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('Sony A7S III'))
    await waitFor(() => expect(mocked.deleteAsset).toHaveBeenCalledWith('a1'))
  })

  it('does nothing when the confirmation is declined', async () => {
    vi.stubGlobal('confirm', vi.fn(() => false))
    await renderLoaded()

    const row = screen.getByText('CAM-001').closest('tr') as HTMLElement
    await fireEvent.click(within(row).getByRole('button', {name: 'Delete'}))

    expect(mocked.deleteAsset).not.toHaveBeenCalled()
  })
})

describe('tags', () => {
  it('ignores a blank tag name', async () => {
    const {container} = await renderLoaded()
    const tagForm = container.querySelectorAll('form')[1] as HTMLFormElement

    await fireEvent.input(screen.getByPlaceholderText('new tag name'), {target: {value: '   '}})
    await fireEvent.submit(tagForm)

    expect(mocked.createTag).not.toHaveBeenCalled()
  })

  it('trims the tag name before creating it', async () => {
    const {container} = await renderLoaded()
    mocked.createTag.mockResolvedValue({} as never)
    const tagForm = container.querySelectorAll('form')[1] as HTMLFormElement

    await fireEvent.input(screen.getByPlaceholderText('new tag name'), {target: {value: '  lighting  '}})
    await fireEvent.submit(tagForm)

    await waitFor(() => expect(mocked.createTag).toHaveBeenCalledWith('lighting'))
  })

  it('warns that deleting a tag is global', async () => {
    await renderLoaded()
    mocked.deleteTag.mockResolvedValue(undefined as never)

    const item = screen.getByText('video', {selector: 'li'}).closest('li') as HTMLElement
    await fireEvent.click(within(item).getByRole('button', {name: 'Delete'}))

    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('every asset'))
    await waitFor(() => expect(mocked.deleteTag).toHaveBeenCalledWith('t2'))
  })

  it('renames a tag from the list', async () => {
    await renderLoaded()
    mocked.renameTag.mockResolvedValue({} as never)

    const item = screen.getByText('audio', {selector: 'li'}).closest('li') as HTMLElement
    await fireEvent.click(within(item).getByRole('button', {name: 'Rename'}))
    await fireEvent.input(screen.getByDisplayValue('audio'), {target: {value: 'sound'}})
    await fireEvent.click(screen.getByRole('button', {name: 'Save'}))

    await waitFor(() => expect(mocked.renameTag).toHaveBeenCalledWith('t1', 'sound'))
  })
})

describe('tagging an asset', () => {
  // availableTagsFor() must hide tags the asset already has, or the insert
  // would violate the asset_tags primary key.
  it('offers only the tags the asset does not already have', async () => {
    await renderLoaded()
    const row = screen.getByText('CAM-001').closest('tr') as HTMLElement
    const select = within(row).getByRole('combobox') as HTMLSelectElement
    const options = Array.from(select.options).map(o => o.textContent?.trim())

    expect(options).toContain('video')
    expect(options).not.toContain('audio')
  })

  it('offers every tag for an asset with none', async () => {
    await renderLoaded()
    const row = screen.getByText('LEN-001').closest('tr') as HTMLElement
    const select = within(row).getByRole('combobox') as HTMLSelectElement
    const options = Array.from(select.options).map(o => o.textContent?.trim())

    expect(options).toEqual(expect.arrayContaining(['audio', 'video']))
  })

  it('adds the selected tag and refreshes', async () => {
    await renderLoaded()
    mocked.addTagToAsset.mockResolvedValue(undefined as never)

    const row = screen.getByText('LEN-001').closest('tr') as HTMLElement
    await fireEvent.change(within(row).getByRole('combobox'), {target: {value: 't1'}})
    await fireEvent.click(within(row).getByRole('button', {name: 'Add'}))

    await waitFor(() => expect(mocked.addTagToAsset).toHaveBeenCalledWith('a2', 't1'))
    await waitFor(() => expect(mocked.listAllAssetTags).toHaveBeenCalledTimes(2))
  })

  it('does nothing when no tag is selected', async () => {
    await renderLoaded()
    const row = screen.getByText('LEN-001').closest('tr') as HTMLElement
    await fireEvent.click(within(row).getByRole('button', {name: 'Add'}))

    expect(mocked.addTagToAsset).not.toHaveBeenCalled()
  })

  it('removes a tag from just that asset', async () => {
    await renderLoaded()
    mocked.removeTagFromAsset.mockResolvedValue(undefined as never)

    const row = screen.getByText('CAM-001').closest('tr') as HTMLElement
    const chip = within(row).getByText('audio').closest('.chip') as HTMLElement
    await fireEvent.click(within(chip).getByRole('button', {name: 'x'}))

    await waitFor(() => expect(mocked.removeTagFromAsset).toHaveBeenCalledWith('a1', 't1'))
  })
})
