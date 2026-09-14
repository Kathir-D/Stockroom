<script lang="ts">
  /**
   * The 48px bottom bar (design-system.md §5.2, §8.4).
   *
   * It exists because it is the part that stops someone walking off with an
   * uncommitted cart. It carries no commit button — that lives on the cart page,
   * which the whole bar links to — and it is absent at zero items, so it costs
   * nothing until it matters.
   *
   * Adding an item **pulses** the dock and increments the count. It never
   * navigates on its own, which would interrupt someone mid-scan.
   *
   * When the viewer has an overdue item the dock turns overdue-coloured, names
   * what to bring back, and disables the link — so the block reads *before* the
   * page rather than at the commit. That is in addition to, not instead of,
   * `CheckOutAssets` refusing server-side (§15 Q6).
   */
  import ArrowRightIcon from "@lucide/svelte/icons/arrow-right"
  import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert"
  import { Button } from "@stockroom/ui/components/ui/button"
  import { cn } from "@stockroom/ui/utils"
  import type { AssetListItem, CustodyRecord } from "../../api/types"
  import { shortDate } from "../../status"
  import { cart } from "../../stores/cart.svelte"
  import PhotoFrame from "./photo-frame.svelte"

  let {
    /** Rows for the thumbnails; whatever the current screen already loaded. */
    knownAssets = [],
    overdueItems = [],
    isAdmin = false,
    onReview,
    onOverride,
  }: {
    knownAssets?: AssetListItem[]
    overdueItems?: CustodyRecord[]
    isAdmin?: boolean
    onReview: () => void
    onOverride?: () => void
  } = $props()

  /** Up to five overlapping thumbnails, per §8.4. */
  const THUMB_LIMIT = 5

  const blocked = $derived(overdueItems.length > 0)
  const thumbs = $derived(
    cart.ids
      .map((id) => knownAssets.find((a) => a.id === id))
      .filter((a): a is AssetListItem => a !== undefined)
      .slice(0, THUMB_LIMIT)
  )

  /** `BM6K-002`, or the count when several things are late. */
  const blockedLabel = $derived(() => {
    if (overdueItems.length === 0) return ""
    if (overdueItems.length === 1) {
      const row = overdueItems[0]
      return `Return ${row.serial_number ?? row.asset_tag} to check out`
    }
    return `Return ${overdueItems.length} overdue items to check out`
  })

  // The pulse: a background flash for --dur-fast on every add (§11). Driven off
  // cart.pulse rather than a CSS animation, so a second add during the first
  // flash restarts it instead of being swallowed.
  let flashing = $state(false)
  let seenPulse = $state(cart.pulse)
  $effect(() => {
    if (cart.pulse === seenPulse) return
    seenPulse = cart.pulse
    flashing = true
    const timer = setTimeout(() => (flashing = false), 120)
    return () => clearTimeout(timer)
  })
</script>

{#if !cart.isEmpty}
  <!-- The whole bar is the link, not just the button (§8.4). A <div> wrapping a
       <button> that fills it would make the thumbnails dead space. -->
  <div
    class={cn(
      "flex h-12 shrink-0 items-center gap-3 border-t px-(--gutter)",
      "transition-colors duration-(--dur-fast) ease-(--ease-brand)",
      blocked
        ? "border-status-overdue/40 bg-status-overdue-bg"
        : "border-line-strong bg-surface",
      flashing && !blocked && "bg-raised"
    )}
  >
    {#if blocked}
      <AlertTriangleIcon class="size-4 shrink-0 text-status-overdue" aria-hidden="true" />
      <p class="min-w-0 flex-1 truncate font-medium text-status-overdue">{blockedLabel()}</p>
      {#if isAdmin && onOverride}
        <Button variant="secondary" size="sm" onclick={onOverride}>Override</Button>
      {/if}
      <Button size="sm" disabled>Review cart · {cart.count}</Button>
    {:else}
      <button
        type="button"
        onclick={onReview}
        class="flex min-w-0 flex-1 items-center gap-3 rounded-sm py-1 text-left"
        aria-label={`Review cart, ${cart.count} ${cart.count === 1 ? "item" : "items"}`}
      >
        {#if thumbs.length > 0}
          <span class="flex shrink-0 items-center">
            {#each thumbs as asset, index (asset.id)}
              <PhotoFrame
                src={asset.photo_url}
                alt={asset.name}
                class={cn(
                  "size-7 rounded-sm ring-1 ring-surface",
                  index > 0 && "-ml-2"
                )}
                iconClass="size-3"
              />
            {/each}
          </span>
        {/if}
        <span class="font-medium text-fg">
          {cart.count} {cart.count === 1 ? "item" : "items"}
        </span>
        {#if cart.dueAt}
          <span class="text-fg-muted">due {shortDate(cart.dueAt)}</span>
        {/if}
      </button>
      <Button size="sm" onclick={onReview}>
        Review cart · {cart.count}
        <ArrowRightIcon aria-hidden="true" />
      </Button>
    {/if}
  </div>
{/if}
