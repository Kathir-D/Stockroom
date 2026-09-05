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

  // An asset's category_id points at the leaf node. It matches a top-level
  // choice when it *is* that node or a direct child of it.
  $: filteredAssets = assets.filter((asset) => {
    if (selectedCategoryId !== 'all') {
      const leaf = categories.find((c) => c.id === asset.category_id)
      if (!leaf) return false
      if (leaf.id !== selectedCategoryId && leaf.parent_id !== selectedCategoryId) return false
    }
    if (selectedSubcategoryId !== 'all' && asset.category_id !== selectedSubcategoryId) {
      return false
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

  function handleWindowKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') closeAsset()
  }

  // Shared utility strings for the two button styles from the design system.
  const btnBase =
    'inline-flex cursor-pointer items-center justify-center gap-1.5 rounded-md border text-sm font-medium leading-tight disabled:cursor-not-allowed disabled:opacity-45'
  const btnGhost = `${btnBase} border-transparent px-0.5 py-1.5 text-accent hover:bg-accent/10 active:bg-accent/18`
  const btnSecondary = `${btnBase} border-divider px-2.5 py-1.5 text-fg hover:bg-fg/7 active:bg-fg/14`

  const inputClass =
    'min-h-9 rounded-md border border-divider bg-neutral-700 px-2.5 py-1.5 text-sm text-fg caret-accent hover:border-fg/45 focus-visible:border-accent focus-visible:outline-offset-0'
  const tagBase = 'inline-flex items-center rounded-[6px] px-2.5 py-[3px] text-[11px] tracking-[0.02em]'
  const rowGrid =
    'grid grid-cols-[56px_minmax(0,2.4fr)_minmax(0,1.2fr)_minmax(0,1.2fr)_minmax(0,1fr)] items-center gap-3 p-2'
</script>

<svelte:window on:keydown={handleWindowKeydown} />

<div class="flex h-screen flex-col">
  <nav class="flex items-center gap-3 px-3 py-2">
    <span class="mr-auto text-lg font-medium">Stockroom</span>
    <div class="ml-auto flex items-center gap-2">
      <!-- Accounts/roles land in Week 5 (see CLAUDE.md Section 7) — this is a
           placeholder until real auth exists. -->
      <div
        class="flex size-[30px] shrink-0 items-center justify-center rounded-full bg-accent-800 text-[13px] font-medium text-accent-100"
      >
        <svg width="16" height="16" viewBox="0 0 256 256" fill="currentColor"
          ><path
            d="M230.92,212c-15.23-26.33-38.7-45.21-66.09-54.16a72,72,0,1,0-73.66,0C63.78,166.78,40.31,185.66,25.08,212a8,8,0,1,0,13.85,8c18.84-32.56,52.14-52,89.07-52s70.23,19.44,89.07,52a8,8,0,1,0,13.85-8ZM72,96a56,56,0,1,1,56,56A56.06,56.06,0,0,1,72,96Z"
          ></path></svg
        >
      </div>
      <div class="flex flex-col leading-tight">
        <span class="text-[13.5px] font-medium">Local session</span>
        <span class="text-[11.5px] text-fg/55">Media department</span>
      </div>
    </div>
  </nav>

  <div class="flex min-h-0 flex-1">
    <aside class="w-1/6 min-w-[220px] overflow-y-auto border-r border-divider px-[15px] py-5">
      <p class="mx-auto mb-3 w-[85%] text-[11px] uppercase tracking-[0.08em] opacity-55">Filters</p>
      <div class="flex flex-col items-center">
        <label for="sr-category" class="mb-[5px] block w-[85%] text-xs text-fg/70">Category</label>
        <select class="w-[85%] {inputClass}" id="sr-category" bind:value={selectedCategoryId}>
          <option value="all">All categories</option>
          {#each topCategories as cat (cat.id)}
            <option value={cat.id}>{cat.name}</option>
          {/each}
        </select>
      </div>
      <div class="mt-3 flex flex-col items-center">
        <label for="sr-subcategory" class="mb-[5px] block w-[85%] text-xs text-fg/70">Subcategory</label>
        <select class="w-[85%] {inputClass}" id="sr-subcategory" bind:value={selectedSubcategoryId}>
          <option value="all">All subcategories</option>
          {#each subcategoriesForSelection as sub (sub.id)}
            <option value={sub.id}>{sub.name}</option>
          {/each}
        </select>
      </div>
      <button type="button" class="mt-4 w-full {btnGhost}" on:click={clearFilters}>
        Clear filters
      </button>
    </aside>

    <main class="min-w-0 flex-1 overflow-y-auto px-5 pt-4 pb-6">
      {#if error}
        <p class="mb-3 rounded-md border border-red-400/40 bg-red-400/10 px-3 py-2 text-sm text-red-200">
          Error: {error}
        </p>
      {/if}

      {#if loading}
        <p class="px-3 py-6 text-center opacity-60">Loading assets…</p>
      {:else}
        <div
          class="{rowGrid} sticky top-0 z-[1] border-b border-divider bg-bg text-[11px] uppercase tracking-[0.08em] opacity-55"
        >
          <span></span><span>Item</span><span>Category</span><span>Subcategory</span>
          <span class="text-right">Serial</span>
        </div>

        {#each filteredAssets as asset (asset.id)}
          <div
            class="{rowGrid} cursor-pointer rounded-md border-b border-divider/60 hover:bg-neutral-800"
            tabindex="0"
            role="button"
            on:click={() => openAsset(asset)}
            on:keydown={(e) => (e.key === 'Enter' || e.key === ' ') && openAsset(asset)}
          >
            <div class="flex size-11 shrink-0 items-center justify-center rounded-md bg-neutral-800 text-fg/40">
              <svg width="18" height="18" viewBox="0 0 256 256" fill="currentColor"
                ><path
                  d="M216,40H40A16,16,0,0,0,24,56V200a16,16,0,0,0,16,16H216a16,16,0,0,0,16-16V56A16,16,0,0,0,216,40Zm0,16V158.75l-26.07-26.06a16,16,0,0,0-22.63,0l-20,20-44-44a16,16,0,0,0-22.63,0L40,168.69V56ZM40,192.29l52-52,64,64H40Z"
                ></path></svg
              >
            </div>
            <span class="font-medium">{asset.name}</span>
            <span class="{tagBase} bg-neutral-800 text-neutral-100">{asset.category_name ?? '—'}</span>
            <span class="{tagBase} border border-accent text-accent">{asset.subcategory_name ?? '—'}</span>
            <span class="text-right font-mono text-[13px] text-fg/60">{asset.serial_number ?? '—'}</span>
          </div>
        {:else}
          <p class="px-3 py-6 text-center opacity-60">No assets match the current filters.</p>
        {/each}
      {/if}
    </main>
  </div>
</div>

{#if selectedAsset}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div class="fixed inset-0 grid place-items-center bg-neutral-900/50 p-3" on:click={closeAsset} role="presentation">
    <div
      class="flex w-full max-w-[440px] flex-col gap-2 rounded-lg bg-surface p-3 shadow-lg"
      role="dialog"
      aria-modal="true"
      aria-labelledby="sr-dialog-title"
      tabindex="-1"
      on:click|stopPropagation
    >
      <p id="sr-dialog-title" class="text-xl font-medium">{selectedAsset.name}</p>

      <div class="mb-3 flex aspect-[4/3] w-full items-center justify-center overflow-hidden rounded-md bg-neutral-800">
        <svg width="40" height="40" viewBox="0 0 256 256" fill="currentColor" class="opacity-40"
          ><path
            d="M216,40H40A16,16,0,0,0,24,56V200a16,16,0,0,0,16,16H216a16,16,0,0,0,16-16V56A16,16,0,0,0,216,40Zm0,16V158.75l-26.07-26.06a16,16,0,0,0-22.63,0l-20,20-44-44a16,16,0,0,0-22.63,0L40,168.69V56ZM40,192.29l52-52,64,64H40Z"
          ></path></svg
        >
      </div>

      <div class="mb-3 grid grid-cols-2 gap-x-3 gap-y-2">
        {#each [['Asset tag', selectedAsset.asset_tag], ['Status', selectedAsset.status], ['Category', selectedAsset.category_name ?? '—'], ['Subcategory', selectedAsset.subcategory_name ?? '—'], ['Serial', selectedAsset.serial_number ?? '—']] as [label, value] (label)}
          <div>
            <p class="mb-0.5 text-[11px] uppercase tracking-[0.06em] opacity-55">{label}</p>
            <p class="text-sm">{value}</p>
          </div>
        {/each}
      </div>

      {#if selectedAsset.description}
        <p class="text-[12.5px] opacity-55">{selectedAsset.description}</p>
      {/if}

      <div class="mt-2 flex justify-end gap-1.5">
        <button type="button" class={btnSecondary} on:click={closeAsset}>Close</button>
      </div>
    </div>
  </div>
{/if}
