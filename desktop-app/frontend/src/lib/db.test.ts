import {beforeEach, describe, expect, it, vi} from 'vitest'

// The Supabase client is replaced with a recorder: these tests are about the
// query each helper builds and how it handles the {data, error} envelope, not
// about PostgREST itself (the database behaviour is covered by supabase/tests).
const state: {result: {data: unknown; error: {message: string} | null}; chain: unknown[][]} = {
  result: {data: null, error: null},
  chain: []
}

function builder() {
  const b: Record<string, unknown> = {
    then: (onOk: (v: unknown) => unknown, onErr?: (e: unknown) => unknown) =>
      Promise.resolve(state.result).then(onOk, onErr)
  }
  for (const method of ['select', 'insert', 'update', 'delete', 'eq', 'order', 'single']) {
    b[method] = (...args: unknown[]) => {
      state.chain.push([method, ...args])
      return b
    }
  }
  return b
}

vi.mock('./supabase', () => ({
  supabase: {
    from(table: string) {
      state.chain.push(['from', table])
      return builder()
    }
  }
}))

import * as db from './db'

function resolves(data: unknown) {
  state.result = {data, error: null}
}
function fails(message: string) {
  state.result = {data: null, error: {message}}
}

beforeEach(() => {
  state.chain = []
  state.result = {data: null, error: null}
})

describe('error handling', () => {
  // Every helper funnels through unwrap(); a PostgREST error must surface as a
  // thrown Error carrying the message, because App.svelte renders e.message.
  const calls: [string, () => Promise<unknown>][] = [
    ['listAssets', () => db.listAssets()],
    ['createAsset', () => db.createAsset({asset_tag: 'A', name: 'B'})],
    ['updateAsset', () => db.updateAsset('id', {name: 'B'})],
    ['deleteAsset', () => db.deleteAsset('id')],
    ['listTags', () => db.listTags()],
    ['createTag', () => db.createTag('t')],
    ['renameTag', () => db.renameTag('id', 't')],
    ['deleteTag', () => db.deleteTag('id')],
    ['listAllAssetTags', () => db.listAllAssetTags()],
    ['addTagToAsset', () => db.addTagToAsset('a', 't')],
    ['removeTagFromAsset', () => db.removeTagFromAsset('a', 't')]
  ]

  it.each(calls)('%s rejects with the database message', async (_name, call) => {
    fails('duplicate key value violates unique constraint "assets_asset_tag_key"')
    await expect(call()).rejects.toThrow(/assets_asset_tag_key/)
  })

  it.each(calls)('%s throws an Error instance, not the raw envelope', async (_name, call) => {
    fails('boom')
    await expect(call()).rejects.toBeInstanceOf(Error)
  })
})

describe('listAssets', () => {
  it('selects only the columns the screen renders, ordered by asset tag', async () => {
    resolves([{id: '1', asset_tag: 'CAM-001', name: 'A7S III', description: null, status: 'available'}])
    const assets = await db.listAssets()

    expect(state.chain).toEqual([
      ['from', 'assets'],
      ['select', 'id, asset_tag, name, description, status'],
      ['order', 'asset_tag']
    ])
    expect(assets).toHaveLength(1)
    expect(assets[0].asset_tag).toBe('CAM-001')
  })

  it('returns an empty array when the table is empty', async () => {
    resolves(null)
    await expect(db.listAssets()).resolves.toEqual([])
  })
})

describe('createAsset', () => {
  it('inserts the payload and returns the created row', async () => {
    const created = {id: '1', asset_tag: 'CAM-003', name: 'FX3', description: null, status: 'available'}
    resolves(created)
    const input = {asset_tag: 'CAM-003', name: 'FX3', status: 'available' as const}

    await expect(db.createAsset(input)).resolves.toEqual(created)
    expect(state.chain).toEqual([['from', 'assets'], ['insert', input], ['select'], ['single']])
  })

  it('passes an omitted description through untouched rather than sending null', async () => {
    resolves({})
    await db.createAsset({asset_tag: 'X', name: 'Y'})
    expect(state.chain[1]).toEqual(['insert', {asset_tag: 'X', name: 'Y'}])
  })
})

describe('updateAsset', () => {
  it('patches a single row by id and returns it', async () => {
    resolves({id: '1', asset_tag: 'CAM-001', name: 'Renamed', description: null, status: 'checked_out'})
    const patch = {name: 'Renamed', status: 'checked_out' as const}

    const updated = await db.updateAsset('1', patch)

    expect(state.chain).toEqual([
      ['from', 'assets'],
      ['update', patch],
      ['eq', 'id', '1'],
      ['select'],
      ['single']
    ])
    expect(updated.name).toBe('Renamed')
  })

  it('supports a partial patch of one field', async () => {
    resolves({})
    await db.updateAsset('1', {status: 'lost'})
    expect(state.chain[1]).toEqual(['update', {status: 'lost'}])
  })
})

describe('deleteAsset', () => {
  it('deletes exactly the row with the given id', async () => {
    resolves(null)
    await db.deleteAsset('1')
    expect(state.chain).toEqual([['from', 'assets'], ['delete'], ['eq', 'id', '1']])
  })
})

describe('tags', () => {
  it('lists tags by name', async () => {
    resolves([{id: 't1', name: 'audio'}])
    await expect(db.listTags()).resolves.toEqual([{id: 't1', name: 'audio'}])
    expect(state.chain).toEqual([['from', 'tags'], ['select', 'id, name'], ['order', 'name']])
  })

  it('returns an empty array when there are no tags', async () => {
    resolves(null)
    await expect(db.listTags()).resolves.toEqual([])
  })

  it('creates a tag from a bare name', async () => {
    resolves({id: 't1', name: 'audio'})
    await db.createTag('audio')
    expect(state.chain).toEqual([['from', 'tags'], ['insert', {name: 'audio'}], ['select'], ['single']])
  })

  it('renames a tag by id', async () => {
    resolves({id: 't1', name: 'sound'})
    await db.renameTag('t1', 'sound')
    expect(state.chain).toEqual([
      ['from', 'tags'],
      ['update', {name: 'sound'}],
      ['eq', 'id', 't1'],
      ['select'],
      ['single']
    ])
  })

  it('deletes a tag globally', async () => {
    resolves(null)
    await db.deleteTag('t1')
    expect(state.chain).toEqual([['from', 'tags'], ['delete'], ['eq', 'id', 't1']])
  })
})

describe('asset_tags join table', () => {
  it('adds a tag to an asset', async () => {
    resolves(null)
    await db.addTagToAsset('a1', 't1')
    expect(state.chain).toEqual([['from', 'asset_tags'], ['insert', {asset_id: 'a1', tag_id: 't1'}]])
  })

  // Both keys must be in the delete, otherwise the tag comes off every asset.
  it('removes a tag from one asset only', async () => {
    resolves(null)
    await db.removeTagFromAsset('a1', 't1')
    expect(state.chain).toEqual([
      ['from', 'asset_tags'],
      ['delete'],
      ['eq', 'asset_id', 'a1'],
      ['eq', 'tag_id', 't1']
    ])
  })
})

describe('listAllAssetTags', () => {
  it('groups the flat join rows by asset id', async () => {
    resolves([
      {asset_id: 'a1', tags: {id: 't1', name: 'audio'}},
      {asset_id: 'a1', tags: {id: 't2', name: 'wireless'}},
      {asset_id: 'a2', tags: {id: 't1', name: 'audio'}}
    ])

    await expect(db.listAllAssetTags()).resolves.toEqual({
      a1: [
        {id: 't1', name: 'audio'},
        {id: 't2', name: 'wireless'}
      ],
      a2: [{id: 't1', name: 'audio'}]
    })
    expect(state.chain).toEqual([['from', 'asset_tags'], ['select', 'asset_id, tags(id, name)']])
  })

  it('preserves the order tags come back in', async () => {
    resolves([
      {asset_id: 'a1', tags: {id: 't2', name: 'b'}},
      {asset_id: 'a1', tags: {id: 't1', name: 'a'}}
    ])
    const map = await db.listAllAssetTags()
    expect(map.a1.map(t => t.id)).toEqual(['t2', 't1'])
  })

  // An embedded row can come back null if the tag was deleted between queries;
  // it must be skipped rather than producing an entry containing null.
  it('skips rows whose embedded tag is null', async () => {
    resolves([
      {asset_id: 'a1', tags: null},
      {asset_id: 'a1', tags: {id: 't1', name: 'audio'}},
      {asset_id: 'a2', tags: null}
    ])
    await expect(db.listAllAssetTags()).resolves.toEqual({a1: [{id: 't1', name: 'audio'}]})
  })

  it('returns an empty map for no rows and for a null payload', async () => {
    resolves([])
    await expect(db.listAllAssetTags()).resolves.toEqual({})
    state.chain = []
    resolves(null)
    await expect(db.listAllAssetTags()).resolves.toEqual({})
  })

  it('never returns an asset key with an empty tag list', async () => {
    resolves([{asset_id: 'a1', tags: null}])
    const map = await db.listAllAssetTags()
    expect(Object.keys(map)).toEqual([])
  })
})
