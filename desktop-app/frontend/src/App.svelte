<script lang="ts">
  import { onMount } from 'svelte'
  import * as db from './lib/db'
  import type { Asset, AssetStatus, Tag } from './lib/db'
  import AssetBrowser from './lib/AssetBrowser.svelte'

  const STATUSES: AssetStatus[] = [
    'available',
    'checked_out',
    'reserved',
    'maintenance',
    'retired',
    'lost'
  ]

  let assets: Asset[] = []
  let tags: Tag[] = []
  let assetTags: Record<string, Tag[]> = {}
  let loading = true
  let error: string | null = null

  let newAssetTag = ''
  let newAssetName = ''
  let newAssetDescription = ''
  let newAssetStatus: AssetStatus = 'available'

  let newTagName = ''

  let editingAssetId: string | null = null
  let editAssetTag = ''
  let editName = ''
  let editDescription = ''
  let editStatus: AssetStatus = 'available'

  let editingTagId: string | null = null
  let editTagName = ''

  let addTagSelection: Record<string, string> = {}

  async function refreshAll() {
    loading = true
    error = null
    try {
      ;[assets, tags, assetTags] = await Promise.all([
        db.listAssets(),
        db.listTags(),
        db.listAllAssetTags()
      ])
    } catch (e) {
      error = (e as Error).message
    }
    loading = false
  }

  onMount(refreshAll)

  async function handleCreateAsset() {
    try {
      await db.createAsset({
        asset_tag: newAssetTag,
        name: newAssetName,
        description: newAssetDescription || undefined,
        status: newAssetStatus
      })
      newAssetTag = ''
      newAssetName = ''
      newAssetDescription = ''
      newAssetStatus = 'available'
      await refreshAll()
    } catch (e) {
      error = (e as Error).message
    }
  }

  function startEditAsset(asset: Asset) {
    editingAssetId = asset.id
    editAssetTag = asset.asset_tag
    editName = asset.name
    editDescription = asset.description ?? ''
    editStatus = asset.status
  }

  function cancelEditAsset() {
    editingAssetId = null
  }

  async function saveEditAsset(id: string) {
    try {
      await db.updateAsset(id, {
        asset_tag: editAssetTag,
        name: editName,
        description: editDescription,
        status: editStatus
      })
      editingAssetId = null
      await refreshAll()
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function handleDeleteAsset(id: string, label: string) {
    if (!confirm(`Delete asset "${label}"? This cannot be undone.`)) return
    try {
      await db.deleteAsset(id)
      await refreshAll()
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function handleCreateTag() {
    if (!newTagName.trim()) return
    try {
      await db.createTag(newTagName.trim())
      newTagName = ''
      await refreshAll()
    } catch (e) {
      error = (e as Error).message
    }
  }

  function startEditTag(tag: Tag) {
    editingTagId = tag.id
    editTagName = tag.name
  }

  async function saveEditTag(id: string) {
    try {
      await db.renameTag(id, editTagName)
      editingTagId = null
      await refreshAll()
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function handleDeleteTag(id: string, name: string) {
    if (!confirm(`Delete tag "${name}" globally? Removes it from every asset.`)) return
    try {
      await db.deleteTag(id)
      await refreshAll()
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function handleAddTagToAsset(assetId: string) {
    const tagId = addTagSelection[assetId]
    if (!tagId) return
    try {
      await db.addTagToAsset(assetId, tagId)
      // Replace the map rather than mutating it: Svelte 5's legacy compiler
      // turns `addTagSelection[assetId] = ''` into an invalidation that
      // references the `asset` each-block variable from outside its scope,
      // which throws "asset is not defined" and swallows the refresh.
      addTagSelection = {...addTagSelection, [assetId]: ''}
      await refreshAll()
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function handleRemoveTagFromAsset(assetId: string, tagId: string) {
    try {
      await db.removeTagFromAsset(assetId, tagId)
      await refreshAll()
    } catch (e) {
      error = (e as Error).message
    }
  }

  // Utility strings shared by the admin controls below. Tailwind's preflight
  // strips native input/button chrome, so every control needs these.
  const input =
    'rounded-md border border-divider bg-surface px-2 py-1 text-sm text-fg caret-accent hover:border-fg/45 focus-visible:border-accent focus-visible:outline-offset-0'
  const button =
    'cursor-pointer rounded-md border border-divider px-2 py-1 text-sm hover:bg-fg/7 active:bg-fg/14'
  const buttonPrimary =
    'cursor-pointer rounded-md border border-accent px-2 py-1 text-sm text-accent hover:bg-accent/10 active:bg-accent/22'
  const cell = 'border-b border-divider px-2.5 py-1.5 text-left align-top'
  const chip = 'mr-1 mb-1 inline-flex items-center gap-1 rounded bg-neutral-800 px-1.5 py-0.5 text-[0.85rem] text-neutral-100'

  function availableTagsFor(assetId: string): Tag[] {
    const assigned = new Set((assetTags[assetId] ?? []).map((t) => t.id))
    return tags.filter((t) => !assigned.has(t.id))
  }
</script>

<!-- Primary browse/checkout screen: the Claude Design import. -->
<AssetBrowser {assets} {loading} {error} />

<!-- Admin tools below: add/edit/delete assets and manage the global tag list.
     These predate the design import and aren't part of it yet — fast-follow
     is to either restyle them to match or move them behind the account menu
     in the nav once real auth/roles land (Week 5, CLAUDE.md Section 7). -->
<main class="mx-auto my-8 max-w-[1000px] px-4">
  {#if !loading}
    <section class="mb-8">
      <h2 class="mb-2 text-2xl font-medium">Add asset</h2>
      <form class="flex flex-wrap items-center gap-2" on:submit|preventDefault={handleCreateAsset}>
        <input class={input} placeholder="asset_tag" bind:value={newAssetTag} required />
        <input class={input} placeholder="name" bind:value={newAssetName} required />
        <input class={input} placeholder="description (optional)" bind:value={newAssetDescription} />
        <select class={input} bind:value={newAssetStatus}>
          {#each STATUSES as s}
            <option value={s}>{s}</option>
          {/each}
        </select>
        <button type="submit" class={buttonPrimary}>Add asset</button>
      </form>
    </section>

    <section class="mb-8">
      <h2 class="mb-2 text-2xl font-medium">Edit / delete assets</h2>
      <table class="w-full border-collapse text-sm">
        <thead>
          <tr>
            <th class={cell}>Tag</th>
            <th class={cell}>Name</th>
            <th class={cell}>Description</th>
            <th class={cell}>Status</th>
            <th class={cell}>Tags</th>
            <th class={cell}>Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each assets as asset (asset.id)}
            <tr>
              {#if editingAssetId === asset.id}
                <td class={cell}><input class={input} bind:value={editAssetTag} /></td>
                <td class={cell}><input class={input} bind:value={editName} /></td>
                <td class={cell}><input class={input} bind:value={editDescription} /></td>
                <td class={cell}>
                  <select class={input} bind:value={editStatus}>
                    {#each STATUSES as s}
                      <option value={s}>{s}</option>
                    {/each}
                  </select>
                </td>
                <td class={cell}>—</td>
                <td class="{cell} space-x-1 whitespace-nowrap">
                  <button class={buttonPrimary} on:click={() => saveEditAsset(asset.id)}>Save</button>
                  <button class={button} on:click={cancelEditAsset}>Cancel</button>
                </td>
              {:else}
                <td class={cell}>{asset.asset_tag}</td>
                <td class={cell}>{asset.name}</td>
                <td class={cell}>{asset.description ?? ''}</td>
                <td class={cell}>{asset.status}</td>
                <td class={cell}>
                  {#each assetTags[asset.id] ?? [] as tag (tag.id)}
                    <span class={chip}>
                      {tag.name}
                      <button class="cursor-pointer text-fg/60 hover:text-fg" on:click={() => handleRemoveTagFromAsset(asset.id, tag.id)}>x</button>
                    </span>
                  {/each}
                  <select class={input} bind:value={addTagSelection[asset.id]}>
                    <option value="">add tag…</option>
                    {#each availableTagsFor(asset.id) as tag (tag.id)}
                      <option value={tag.id}>{tag.name}</option>
                    {/each}
                  </select>
                  <button class={button} on:click={() => handleAddTagToAsset(asset.id)}>Add</button>
                </td>
                <td class="{cell} space-x-1 whitespace-nowrap">
                  <button class={button} on:click={() => startEditAsset(asset)}>Edit</button>
                  <button class={button} on:click={() => handleDeleteAsset(asset.id, asset.name)}>Delete</button>
                </td>
              {/if}
            </tr>
          {/each}
        </tbody>
      </table>
    </section>

    <section class="mb-8">
      <h2 class="mb-2 text-2xl font-medium">Tags (global)</h2>
      <form class="mb-3 flex items-center gap-2" on:submit|preventDefault={handleCreateTag}>
        <input class={input} placeholder="new tag name" bind:value={newTagName} required />
        <button type="submit" class={buttonPrimary}>Add tag</button>
      </form>
      <ul class="space-y-1">
        {#each tags as tag (tag.id)}
          <li class="flex items-center gap-2">
            {#if editingTagId === tag.id}
              <input class={input} bind:value={editTagName} />
              <button class={buttonPrimary} on:click={() => saveEditTag(tag.id)}>Save</button>
              <button class={button} on:click={() => (editingTagId = null)}>Cancel</button>
            {:else}
              {tag.name}
              <button class={button} on:click={() => startEditTag(tag)}>Rename</button>
              <button class={button} on:click={() => handleDeleteTag(tag.id, tag.name)}>Delete</button>
            {/if}
          </li>
        {/each}
      </ul>
    </section>
  {/if}
</main>
