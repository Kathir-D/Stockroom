<script lang="ts">
  import { onMount } from 'svelte'
  import * as db from './lib/db'
  import type { Asset, AssetStatus, Tag } from './lib/db'

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
      addTagSelection[assetId] = ''
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

  function availableTagsFor(assetId: string): Tag[] {
    const assigned = new Set((assetTags[assetId] ?? []).map((t) => t.id))
    return tags.filter((t) => !assigned.has(t.id))
  }
</script>

<main>
  <h1>Stockroom — Admin</h1>

  {#if error}
    <p class="error">Error: {error}</p>
  {/if}

  {#if loading}
    <p>Loading…</p>
  {:else}
    <section>
      <h2>Add asset</h2>
      <form on:submit|preventDefault={handleCreateAsset}>
        <input placeholder="asset_tag" bind:value={newAssetTag} required />
        <input placeholder="name" bind:value={newAssetName} required />
        <input placeholder="description (optional)" bind:value={newAssetDescription} />
        <select bind:value={newAssetStatus}>
          {#each STATUSES as s}
            <option value={s}>{s}</option>
          {/each}
        </select>
        <button type="submit">Add asset</button>
      </form>
    </section>

    <section>
      <h2>Assets ({assets.length})</h2>
      <table>
        <thead>
          <tr>
            <th>Tag</th>
            <th>Name</th>
            <th>Description</th>
            <th>Status</th>
            <th>Tags</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each assets as asset (asset.id)}
            <tr>
              {#if editingAssetId === asset.id}
                <td><input bind:value={editAssetTag} /></td>
                <td><input bind:value={editName} /></td>
                <td><input bind:value={editDescription} /></td>
                <td>
                  <select bind:value={editStatus}>
                    {#each STATUSES as s}
                      <option value={s}>{s}</option>
                    {/each}
                  </select>
                </td>
                <td>—</td>
                <td>
                  <button on:click={() => saveEditAsset(asset.id)}>Save</button>
                  <button on:click={cancelEditAsset}>Cancel</button>
                </td>
              {:else}
                <td>{asset.asset_tag}</td>
                <td>{asset.name}</td>
                <td>{asset.description ?? ''}</td>
                <td>{asset.status}</td>
                <td>
                  {#each assetTags[asset.id] ?? [] as tag (tag.id)}
                    <span class="chip">
                      {tag.name}
                      <button on:click={() => handleRemoveTagFromAsset(asset.id, tag.id)}>x</button>
                    </span>
                  {/each}
                  <select bind:value={addTagSelection[asset.id]}>
                    <option value="">add tag…</option>
                    {#each availableTagsFor(asset.id) as tag (tag.id)}
                      <option value={tag.id}>{tag.name}</option>
                    {/each}
                  </select>
                  <button on:click={() => handleAddTagToAsset(asset.id)}>Add</button>
                </td>
                <td>
                  <button on:click={() => startEditAsset(asset)}>Edit</button>
                  <button on:click={() => handleDeleteAsset(asset.id, asset.name)}>Delete</button>
                </td>
              {/if}
            </tr>
          {/each}
        </tbody>
      </table>
    </section>

    <section>
      <h2>Tags (global)</h2>
      <form on:submit|preventDefault={handleCreateTag}>
        <input placeholder="new tag name" bind:value={newTagName} required />
        <button type="submit">Add tag</button>
      </form>
      <ul>
        {#each tags as tag (tag.id)}
          <li>
            {#if editingTagId === tag.id}
              <input bind:value={editTagName} />
              <button on:click={() => saveEditTag(tag.id)}>Save</button>
              <button on:click={() => (editingTagId = null)}>Cancel</button>
            {:else}
              {tag.name}
              <button on:click={() => startEditTag(tag)}>Rename</button>
              <button on:click={() => handleDeleteTag(tag.id, tag.name)}>Delete</button>
            {/if}
          </li>
        {/each}
      </ul>
    </section>
  {/if}
</main>

<style>
  main {
    max-width: 1000px;
    margin: 2rem auto;
    padding: 0 1rem;
    font-family: sans-serif;
  }

  section {
    margin-bottom: 2rem;
  }

  table {
    width: 100%;
    border-collapse: collapse;
  }

  th, td {
    text-align: left;
    padding: 0.4rem 0.6rem;
    border-bottom: 1px solid #ddd;
    vertical-align: top;
  }

  .chip {
    display: inline-block;
    background: #eee;
    border-radius: 4px;
    padding: 0.1rem 0.4rem;
    margin: 0 0.2rem 0.2rem 0;
    font-size: 0.85rem;
  }

  .error {
    color: #c00;
  }
</style>
