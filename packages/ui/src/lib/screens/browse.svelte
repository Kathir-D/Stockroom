<script lang="ts">
  /**
   * The browse screen (design-system.md §8.2): the primary screen.
   *
   * List shape is `B3 + B1` — one row per model, and the row is an accordion onto
   * its units. Sort order is the server's: categories in `Catagories.md`'s
   * document order, and available units before checked-out ones within any list
   * (CLAUDE.md §13). Nothing here re-sorts, because the server's first sort key is
   * the asset's position in the category tree and this screen doesn't know it.
   *
   * The two empty states are deliberately different. "This category has no models
   * yet" is true of `Primes`, which is seeded empty because `Catagories.md`
   * records no primes in inventory; "your filters matched nothing" wants the
   * filters cleared. They need different actions (§8.2).
   */
  import { Button } from "@stockroom/ui/components/ui/button"
  import { Skeleton } from "@stockroom/ui/components/ui/skeleton"
  import EmptyState from "@stockroom/ui/components/app/empty-state.svelte"
  import ModelRow from "@stockroom/ui/components/app/model-row.svelte"
  import AssetDetailDialog from "@stockroom/ui/components/app/asset-detail-dialog.svelte"
  import type { AssetDetail, AssetListItem } from "../api/types"
  import { addShouldOpenDetail } from "../add-flow"
  import { cart } from "../stores/cart.svelte"
  import { catalog } from "../stores/catalog.svelte"
  import { session } from "../stores/session.svelte"

  let {
    onAdd,
    onRemove,
    onCheckIn,
  }: {
    onAdd: (unit: AssetListItem) => void
    /** Same button as Add, pressed again. See <UnitRow>. */
    onRemove: (unit: AssetListItem) => void
    onCheckIn: (asset: AssetDetail, note: string) => Promise<void>
  } = $props()

  let detail = $state<AssetDetail | null>(null)
  let detailOpen = $state(false)

  /**
   * Rows are collapsed by default. Two things open one: a search result opens the
   * group that matched, and a scanned serial opens its group (§8.2). Everything
   * the server returned for a search *did* match, so a search opens them all.
   */
  const searching = $derived(catalog.search.trim().length > 0)
  const highlightedGroupKey = $derived(
    catalog.highlightedUnitId
      ? (catalog.groups.find((g) =>
          g.units.some((u) => u.id === catalog.highlightedUnitId)
        )?.key ?? null)
      : null
  )

  function openDetail(unit: AssetListItem) {
    detail = unit
    detailOpen = true
  }

  /**
   * **Add** on a unit row, which is not always an add.
   *
   * `CLAUDE.md` §1 step 3 routes the cart through the detail popup: the press
   * opens the dialog, and the dialog's own **Add to cart** is what commits. The
   * dialog is the only place the photo, the condition note and the custody
   * history are visible, and on a shelf of three identical bodies it is where
   * someone confirms they are taking the one they meant.
   *
   * Units with nothing extra to show — batteries and the like — skip it and go
   * straight in, because a popup that repeats the row is a press for nothing.
   * `lib/add-flow.ts` owns both the rule and the flag that turns it off.
   */
  function requestAdd(unit: AssetListItem) {
    if (addShouldOpenDetail(unit)) openDetail(unit)
    else onAdd(unit)
  }

  async function handleCheckIn(asset: AssetDetail, note: string) {
    await onCheckIn(asset, note)
    detailOpen = false
  }

  function clearFilters() {
    catalog.search = ""
    catalog.status = undefined
    catalog.reload()
  }
</script>

<section class="flex flex-col">
  {#if catalog.error}
    <EmptyState
      title="Couldn't load the inventory"
      description={catalog.error}
      class="border-status-overdue/40"
    >
      {#snippet action()}
        <Button variant="secondary" onclick={() => catalog.reload()}>Try again</Button>
      {/snippet}
    </EmptyState>
  {:else if catalog.loading && !catalog.loaded}
    <div class="flex flex-col gap-px" aria-busy="true" aria-label="Loading inventory">
      {#each Array(6) as _, index (index)}
        <Skeleton class="h-(--row-h) rounded-none" />
      {/each}
    </div>
  {:else if catalog.groups.length === 0}
    {#if catalog.selectedIsEmptyBranch}
      <EmptyState
        title={`${catalog.selectedNode?.name ?? "This category"} has nothing in it yet`}
        description="No units are filed under here. An admin can add them from the Assets tab of the admin panel."
      />
    {:else}
      <EmptyState
        title="Nothing matched"
        description="No units match the current category, status and search together."
      >
        {#snippet action()}
          <Button variant="secondary" onclick={clearFilters}>Clear filters</Button>
        {/snippet}
      </EmptyState>
    {/if}
  {:else}
    <div class="overflow-hidden rounded-xl border border-line-strong">
      {#each catalog.groups as group (group.key)}
        <ModelRow
          {group}
          viewerId={session.profile?.id ?? null}
          cartIds={cart.ids}
          canAdd={!session.checkoutBlocked}
          forceOpen={searching || highlightedGroupKey === group.key}
          highlightedUnitId={catalog.highlightedUnitId}
          onOpenUnit={openDetail}
          onAddUnit={requestAdd}
          onRemoveUnit={onRemove}
        />
      {/each}
    </div>
  {/if}
</section>

<AssetDetailDialog
  asset={detail}
  bind:open={detailOpen}
  viewerId={session.profile?.id ?? null}
  isAdmin={session.isAdmin}
  canAdd={!session.checkoutBlocked}
  inCart={detail ? cart.has(detail.id) : false}
  onAdd={(asset) => {
    onAdd(asset)
    detailOpen = false
  }}
  onRemove={(asset) => {
    onRemove(asset)
    detailOpen = false
  }}
  onCheckIn={handleCheckIn}
/>
