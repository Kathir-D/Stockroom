import { supabase } from './supabase'

export type AssetStatus =
  | 'available'
  | 'checked_out'
  | 'reserved'
  | 'maintenance'
  | 'retired'
  | 'lost'

export type Asset = {
  id: string
  asset_tag: string
  name: string
  description: string | null
  status: AssetStatus
}

export type Tag = {
  id: string
  name: string
}

function unwrap<T>(result: { data: T | null; error: { message: string } | null }): T {
  if (result.error) throw new Error(result.error.message)
  return result.data as T
}

// --- Assets ---

export async function listAssets(): Promise<Asset[]> {
  const res = await supabase
    .from('assets')
    .select('id, asset_tag, name, description, status')
    .order('asset_tag')
  return unwrap(res) ?? []
}

export async function createAsset(input: {
  asset_tag: string
  name: string
  description?: string
  status?: AssetStatus
}): Promise<Asset> {
  const res = await supabase.from('assets').insert(input).select().single()
  return unwrap(res)
}

export async function updateAsset(
  id: string,
  patch: Partial<Pick<Asset, 'asset_tag' | 'name' | 'description' | 'status'>>
): Promise<Asset> {
  const res = await supabase.from('assets').update(patch).eq('id', id).select().single()
  return unwrap(res)
}

export async function deleteAsset(id: string): Promise<void> {
  const res = await supabase.from('assets').delete().eq('id', id)
  unwrap(res)
}

// --- Tags (global) ---

export async function listTags(): Promise<Tag[]> {
  const res = await supabase.from('tags').select('id, name').order('name')
  return unwrap(res) ?? []
}

export async function createTag(name: string): Promise<Tag> {
  const res = await supabase.from('tags').insert({ name }).select().single()
  return unwrap(res)
}

export async function renameTag(id: string, name: string): Promise<Tag> {
  const res = await supabase.from('tags').update({ name }).eq('id', id).select().single()
  return unwrap(res)
}

// Deleting a tag removes it everywhere: asset_tags has ON DELETE CASCADE on tag_id.
export async function deleteTag(id: string): Promise<void> {
  const res = await supabase.from('tags').delete().eq('id', id)
  unwrap(res)
}

// --- Tags on a specific asset ---

export async function listAllAssetTags(): Promise<Record<string, Tag[]>> {
  const res = await supabase.from('asset_tags').select('asset_id, tags(id, name)')
  const rows = unwrap(res) as unknown as { asset_id: string; tags: Tag }[]
  const map: Record<string, Tag[]> = {}
  for (const row of rows ?? []) {
    if (!row.tags) continue
    ;(map[row.asset_id] ??= []).push(row.tags)
  }
  return map
}

export async function addTagToAsset(assetId: string, tagId: string): Promise<void> {
  const res = await supabase.from('asset_tags').insert({ asset_id: assetId, tag_id: tagId })
  unwrap(res)
}

export async function removeTagFromAsset(assetId: string, tagId: string): Promise<void> {
  const res = await supabase
    .from('asset_tags')
    .delete()
    .eq('asset_id', assetId)
    .eq('tag_id', tagId)
  unwrap(res)
}
