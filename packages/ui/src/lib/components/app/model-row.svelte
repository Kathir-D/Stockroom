<script lang="ts">
  /**
   * A model, and the accordion onto its units (design-system.md §8.2, list shape
   * `B3 + B1`).
   *
   * `B3` is the base because the closet holds 200-odd units across roughly thirty
   * models, and a flat unit list is thirty screens of near-identical rows. `B1`
   * survives inside the expansion, so nothing the flat list could tell you is
   * lost — and the same `<UnitRow>` serves the admin table, which stays flat.
   *
   * **The model row has no Add.** It is a summary and a way in: it says how many
   * of this model are free, and opens to let you pick one. Adding is a unit-row
   * action, always, because what goes in the cart is a specific serial and a
   * model-level press had to guess which one — reversing the 2026-09-10 rule
   * that let it take "any free unit" (design-system.md §8.2, §16). The guess was
   * invisible in both directions: you pressed Add on `Canon T7i`, one of three
   * bodies landed in your cart, and the row never said which.
   *
   * With no button beside it, the whole row is the trigger and nothing has to
   * stop propagation.
   *
   * The expansion does not slide open. Units appear at once; only the caret
   * rotates. A twenty-row expansion animating its height is exactly the layout
   * thrash §11 exists to prevent, and the row is a filter step, not a reveal
   * worth decorating.
   */
  import ChevronRightIcon from "@lucide/svelte/icons/chevron-right"
  import * as Collapsible from "@stockroom/ui/components/ui/collapsible"
  import { cn } from "@stockroom/ui/utils"
  import type { AssetListItem } from "../../api/types"
  import type { ModelGroup } from "../../stores/catalog.svelte"
  import { groupStatus } from "../../status"
  import PhotoFrame from "./photo-frame.svelte"
  import StatusDot from "./status-dot.svelte"
  import UnitRow from "./unit-row.svelte"

  /**
   * How many units render inline before the footer row takes over. `SD Card
   * 128GB` with twenty units is fine; a future model with three hundred would
   * not be. Not pagination — a cap with a way out (§8.2).
   */
  const INLINE_UNIT_CAP = 50

  let {
    group,
    viewerId = null,
    cartIds = [],
    canAdd = true,
    /**
     * Forces the row open. The only two things that set it are a search hit and
     * a scan (§8.2); everything else is collapsed by default. A press still
     * wins over it, so a forced-open row can be collapsed by hand.
     */
    forceOpen = false,
    highlightedUnitId = null,
    onOpenUnit,
    onAddUnit,
    onRemoveUnit,
  }: {
    group: ModelGroup
    viewerId?: string | null
    cartIds?: string[]
    canAdd?: boolean
    forceOpen?: boolean
    highlightedUnitId?: string | null
    onOpenUnit?: (unit: AssetListItem) => void
    onAddUnit?: (unit: AssetListItem) => void
    onRemoveUnit?: (unit: AssetListItem) => void
  } = $props()

  let showAll = $state(false)

  // Controlled rather than `bind:open`, so the parent can pass a derived value:
  // null means "nobody has pressed this row, follow forceOpen".
  let userToggled = $state<boolean | null>(null)
  const open = $derived(userToggled ?? forceOpen)
  $effect(() => {
    // A change in why the row would be open (a new search, a new scan) drops the
    // manual override, so the next search doesn't inherit the last collapse.
    void forceOpen
    userToggled = null
  })

  const status = $derived(groupStatus(group.availableCount, group.units.length))
  const visibleUnits = $derived(
    showAll ? group.units : group.units.slice(0, INLINE_UNIT_CAP)
  )
  const hiddenCount = $derived(group.units.length - visibleUnits.length)

  /** The path minus the model itself, which is already the row's headline. */
  const parentPath = $derived(group.categoryPath.slice(0, -1).map((c) => c.name).join(" › "))
</script>

<Collapsible.Root {open} onOpenChange={(next) => (userToggled = next)}>
  <div
    class={cn(
      "flex items-center border-b border-line bg-surface",
      open && "bg-raised"
    )}
  >
    <Collapsible.Trigger
      aria-label={`${group.name}, ${status.label}`}
      class={cn(
        "flex min-h-(--row-h) w-full items-center gap-3 px-(--gutter) py-2 text-left outline-none",
        "hover:bg-raised focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-(--ring)"
      )}
    >
      <ChevronRightIcon
        class={cn(
          "size-4 shrink-0 text-fg-muted transition-transform duration-(--dur-fast) ease-(--ease-brand)",
          open && "rotate-90"
        )}
        aria-hidden="true"
      />

      <PhotoFrame src={group.thumbnail} alt={group.name} class="size-10" />

      <span class="flex min-w-0 flex-1 flex-col">
        <span class="truncate font-medium text-fg">{group.name}</span>
        {#if parentPath}
          <!-- Shed below 1280px, per the §7.2 breakpoint table. -->
          <span class="hidden truncate text-xs text-fg-muted xl:inline">{parentPath}</span>
        {/if}
      </span>

      <StatusDot {status} class="shrink-0" />

      <span class="w-28 shrink-0 text-right text-xs text-fg-muted">
        {#if group.outCount > 0}
          {group.outCount} out
        {/if}
        {#if group.unavailableCount > 0}
          {group.outCount > 0 ? " · " : ""}{group.unavailableCount} unavailable
        {/if}
      </span>
    </Collapsible.Trigger>
  </div>

  <Collapsible.Content>
    <div class="border-b border-line bg-ground pl-8">
      {#each visibleUnits as unit (unit.id)}
        <UnitRow
          {unit}
          {viewerId}
          {canAdd}
          inCart={cartIds.includes(unit.id)}
          highlighted={highlightedUnitId === unit.id}
          onOpen={onOpenUnit}
          onAdd={onAddUnit}
          onRemove={onRemoveUnit}
        />
      {/each}
      {#if hiddenCount > 0}
        <button
          type="button"
          class="min-h-(--row-h) w-full px-3 text-left text-fg-muted hover:bg-raised hover:text-fg"
          onclick={() => (showAll = true)}
        >
          {hiddenCount} more {hiddenCount === 1 ? "unit" : "units"}
        </button>
      {/if}
    </div>
  </Collapsible.Content>
</Collapsible.Root>
