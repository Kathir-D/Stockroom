<script lang="ts">
  import { onMount } from 'svelte'
  import * as db from './db'
  import type { Asset, Category } from './db'

  export let assets: Asset[] = []
  export let loading = true
  export let error: string | null = null

  let categories: Category[] = []

  // "all" is the sentinel used by the <select> elements in the design.
  let selectedCategoryId = 'all'
  let selectedSubcategoryId = 'all'

  let selectedAsset: Asset | null = null

  onMount(async () => {
    try {
      categories = await db.listCategories()
    } catch (e) {
      // Non-fatal: filters just show as empty if this fails.
      console.error('Failed to load categories', e)
    }
  })

  $: topCategories = categories.filter((c) => c.parent_id === null)
  $: subcategoriesForSelection =
    selectedCategoryId === 'all'
      ? categories.filter((c) => c.parent_id !== null)
      : categories.filter((c) => c.parent_id === selectedCategoryId)

  // Reset the subcategory choice whenever the category changes to something
  // that no longer contains it.
  $: if (
    selectedSubcategoryId !== 'all' &&
    !subcategoriesForSelection.some((c) => c.id === selectedSubcategoryId)
  ) {
    selectedSubcategoryId = 'all'
  }

  function categoryIdByName(name: string | null): string | undefined {
    return categories.find((c) => c.name === name)?.id
  }

  $: filteredAssets = assets.filter((asset) => {
    if (selectedCategoryId !== 'all') {
      const cat = topCategories.find((c) => c.id === selectedCategoryId)
      if (!cat || asset.category_name !== cat.name) return false
    }
    if (selectedSubcategoryId !== 'all') {
      const sub = categories.find((c) => c.id === selectedSubcategoryId)
      if (!sub || asset.subcategory_name !== sub.name) return false
    }
    return true
  })

  function clearFilters() {
    selectedCategoryId = 'all'
    selectedSubcategoryId = 'all'
  }

  function openAsset(asset: Asset) {
    selectedAsset = asset
  }

  function closeAsset() {
    selectedAsset = null
  }

  function handleBackdropKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') closeAsset()
  }
</script>

<svelte:window on:keydown={handleBackdropKeydown} />

<div class="sr-shell">
  <nav class="nav">
    <span class="nav-brand">Stockroom</span>
    <div class="sr-account">
      <!-- Accounts/roles land in Week 5 (see CLAUDE.md Section 7) — this is a
           placeholder until real auth exists. -->
      <div class="sr-avatar">
        <svg width="16" height="16" viewBox="0 0 256 256" fill="currentColor"
          ><path
            d="M230.92,212c-15.23-26.33-38.7-45.21-66.09-54.16a72,72,0,1,0-73.66,0C63.78,166.78,40.31,185.66,25.08,212a8,8,0,1,0,13.85,8c18.84-32.56,52.14-52,89.07-52s70.23,19.44,89.07,52a8,8,0,1,0,13.85-8ZM72,96a56,56,0,1,1,56,56A56.06,56.06,0,0,1,72,96Z"
          ></path></svg
        >
      </div>
      <div class="sr-account-meta">
        <span class="sr-account-name">Local session</span>
        <span class="sr-account-role">Media department</span>
      </div>
    </div>
  </nav>

  <div class="sr-body">
    <aside class="sr-aside">
      <p class="sr-filter-title" style="width: 85%; margin-left: auto; margin-right: auto;">
        Filters
      </p>
      <div class="field">
        <label for="sr-category">Category</label>
        <select class="input" id="sr-category" bind:value={selectedCategoryId}>
          <option value="all">All categories</option>
          {#each topCategories as cat (cat.id)}
            <option value={cat.id}>{cat.name}</option>
          {/each}
        </select>
      </div>
      <div class="field" style="margin-top: var(--space-4);">
        <label for="sr-subcategory">Subcategory</label>
        <select class="input" id="sr-subcategory" bind:value={selectedSubcategoryId}>
          <option value="all">All subcategories</option>
          {#each subcategoriesForSelection as sub (sub.id)}
            <option value={sub.id}>{sub.name}</option>
          {/each}
        </select>
      </div>
      <button
        type="button"
        class="btn btn-ghost btn-block"
        style="margin-top: var(--space-5);"
        on:click={clearFilters}
      >
        Clear filters
      </button>
    </aside>

    <main class="sr-main">
      {#if loading}
        <p class="sr-empty">Loading assets…</p>
      {:else if error}
        <p class="sr-empty">Error: {error}</p>
      {:else}
        <div class="sr-row-head">
          <span></span><span>Item</span><span>Category</span><span>Subcategory</span>
          <span style="text-align: right;">Serial</span>
        </div>

        {#each filteredAssets as asset (asset.id)}
          <div
            class="sr-row"
            tabindex="0"
            role="button"
            on:click={() => openAsset(asset)}
            on:keydown={(e) => (e.key === 'Enter' || e.key === ' ') && openAsset(asset)}
          >
            <div
              style="width: 44px; height: 44px; flex-shrink: 0; border-radius: 8px; background: var(--color-neutral-800); display: flex; align-items: center; justify-content: center; color: color-mix(in srgb, var(--color-text) 40%, transparent);"
            >
              <svg width="18" height="18" viewBox="0 0 256 256" fill="currentColor"
                ><path
                  d="M216,40H40A16,16,0,0,0,24,56V200a16,16,0,0,0,16,16H216a16,16,0,0,0,16-16V56A16,16,0,0,0,216,40Zm0,16V158.75l-26.07-26.06a16,16,0,0,0-22.63,0l-20,20-44-44a16,16,0,0,0-22.63,0L40,168.69V56ZM40,192.29l52-52,64,64H40Z"
                ></path></svg
              >
            </div>
            <span class="sr-name">{asset.name}</span>
            <span class="tag tag-neutral">{asset.category_name ?? '—'}</span>
            <span class="tag tag-outline">{asset.subcategory_name ?? '—'}</span>
            <span class="sr-serial">{asset.serial_number ?? '—'}</span>
          </div>
        {:else}
          <p class="sr-empty">No assets match the current filters.</p>
        {/each}
      {/if}
    </main>
  </div>
</div>

{#if selectedAsset}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div class="dialog-backdrop" on:click={closeAsset} role="presentation">
    <div class="dialog" role="dialog" aria-modal="true" on:click|stopPropagation>
      <p class="dialog-title">{selectedAsset.name}</p>

      <div class="sr-modal-photo" style="display: flex; align-items: center; justify-content: center; background: var(--color-neutral-800);">
        <svg width="40" height="40" viewBox="0 0 256 256" fill="currentColor" style="opacity: 0.4;"
          ><path
            d="M216,40H40A16,16,0,0,0,24,56V200a16,16,0,0,0,16,16H216a16,16,0,0,0,16-16V56A16,16,0,0,0,216,40Zm0,16V158.75l-26.07-26.06a16,16,0,0,0-22.63,0l-20,20-44-44a16,16,0,0,0-22.63,0L40,168.69V56ZM40,192.29l52-52,64,64H40Z"
          ></path></svg
        >
      </div>

      <div class="sr-detail-grid">
        <div>
          <p class="sr-detail-label">Asset tag</p>
          <p class="sr-detail-value">{selectedAsset.asset_tag}</p>
        </div>
        <div>
          <p class="sr-detail-label">Status</p>
          <p class="sr-detail-value">{selectedAsset.status}</p>
        </div>
        <div>
          <p class="sr-detail-label">Category</p>
          <p class="sr-detail-value">{selectedAsset.category_name ?? '—'}</p>
        </div>
        <div>
          <p class="sr-detail-label">Subcategory</p>
          <p class="sr-detail-value">{selectedAsset.subcategory_name ?? '—'}</p>
        </div>
        <div>
          <p class="sr-detail-label">Serial</p>
          <p class="sr-detail-value">{selectedAsset.serial_number ?? '—'}</p>
        </div>
      </div>

      {#if selectedAsset.description}
        <p class="sr-modal-note">{selectedAsset.description}</p>
      {/if}

      <div class="dialog-actions">
        <button type="button" class="btn btn-secondary" on:click={closeAsset}>Close</button>
      </div>
    </div>
  </div>
{/if}
