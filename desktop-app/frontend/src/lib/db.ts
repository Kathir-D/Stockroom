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
  serial_number: string | null
  category_id: string | null
  // Flattened from the categories self-join: if the asset's category has a
  // parent category, category_name is the parent and subcategory_name is the
  // leaf; otherwise category_name is the leaf and subcategory_name is null.
  category_name: string | null
  subcategory_name: string | null
}

export type Tag = {
  id: string
  name: string
}

export type Category = {
  id: string
  name: string
  parent_id: string | null
}

function unwrap<T>(result: { data: T | null; error: { message: string } | null }): T {
  if (result.error) throw new Error(result.error.message)
  return result.data as T
}

// --- Assets ---

type RawCategoryEmbed = {
  name: string
  parent: { name: string } | null
} | null

type RawAssetRow = {
  id: string
  asset_tag: string
  name: string
  description: string | null
  status: AssetStatus
  serial_number: string | null
  category_id: string | null
  category: RawCategoryEmbed
}

function flattenAsset(row: RawAssetRow): Asset {
  const cat = row.category
  const category_name = cat ? (cat.parent ? cat.parent.name : cat.name) : null
  const subcategory_name = cat && cat.parent ? cat.name : null
  return {
    id: row.id,
    asset_tag: row.asset_tag,
    name: row.name,
    description: row.description,
    status: row.status,
    serial_number: row.serial_number,
    category_id: row.category_id,
    category_name,
    subcategory_name
  }
}

export async function listAssets(): Promise<Asset[]> {
  const res = await supabase
    .from('assets')
    // The category self-join has to use the column-as-embed form
    // (`parent:parent_id(...)`): a `!categories_parent_id_fkey` hint is
    // rejected by PostgREST and `!parent_id` resolves to the *children*
    // direction, coming back as an array instead of the parent row.
    .select(
      `id, asset_tag, name, description, status, serial_number, category_id,
       category:categories!assets_category_id_fkey ( name, parent:parent_id ( name ) )`
    )
    .order('asset_tag')
  const rows = unwrap(res) as unknown as RawAssetRow[]
  return (rows ?? []).map(flattenAsset)
}

export async function createAsset(input: {
  asset_tag: string
  name: string
  description?: string
  status?: AssetStatus
  serial_number?: string
  category_id?: string
}): Promise<Asset> {
  const res = await supabase.from('assets').insert(input).select().single()
  const row = unwrap(res) as unknown as Omit<RawAssetRow, 'category'>
  return flattenAsset({ ...row, category: null })
}

export async function updateAsset(
  id: string,
  patch: Partial<
    Pick<Asset, 'asset_tag' | 'name' | 'description' | 'status' | 'serial_number' | 'category_id'>
  >
): Promise<Asset> {
  const res = await supabase.from('assets').update(patch).eq('id', id).select().single()
  const row = unwrap(res) as unknown as Omit<RawAssetRow, 'category'>
  return flattenAsset({ ...row, category: null })
}

export async function deleteAsset(id: string): Promise<void> {
  const res = await supabase.from('assets').delete().eq('id', id)
  unwrap(res)
}

// --- Categories / subcategories ---
// Categories are a self-referencing table: rows with parent_id === null are
// top-level categories, rows with a parent_id are subcategories of that row.

export async function listCategories(): Promise<Category[]> {
  const res = await supabase.from('categories').select('id, name, parent_id').order('name')
  return unwrap(res) ?? []
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
