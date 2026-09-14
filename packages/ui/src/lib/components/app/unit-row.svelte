<script lang="ts">
  /**
   * One physical asset (design-system.md §5.2, §8.2).
   *
   * Used twice: inside an expanded `<ModelRow>` on browse, and standalone by the
   * admin asset table, which stays flat `B1` because an operator editing assets
   * works unit by unit and the grouping only gets in the way (§8.2). One
   * component for both is the point — it is why the admin table can't drift from
   * the browse list.
   *
   * The row is a real `<button>`, and **Add** is its *sibling*, not a child: a
   * button inside a button is invalid, and a press that both adds an item and
   * opens a dialog is a press nobody meant.
   */
  import PlusIcon from "@lucide/svelte/icons/plus"
  import CheckIcon from "@lucide/svelte/icons/check"
  import type { Snippet } from "svelte"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Tooltip from "@stockroom/ui/components/ui/tooltip"
  import { cn } from "@stockroom/ui/utils"
  import type { AssetListItem } from "../../api/types"
  import { custodianLine, resolveStatus } from "../../status"
  import PhotoFrame from "./photo-frame.svelte"
  import Serial from "./serial.svelte"
  import StatusDot from "./status-dot.svelte"

  let {
    unit,
    viewerId = null,
    inCart = false,
    /** False when the viewer is overdue: the whole cart path disables at once. */
    canAdd = true,
    /** Set for the unit a scan just landed on; highlights for --dur-slow. */
    highlighted = false,
    /** Admin table rows show the model name and a trailing action cluster. */
    showName = false,
    onOpen,
    onAdd,
    actions,
    class: className,
  }: {
    unit: AssetListItem
    viewerId?: string | null
    inCart?: boolean
    canAdd?: boolean
    highlighted?: boolean
    showName?: boolean
    onOpen?: (unit: AssetListItem) => void
    onAdd?: (unit: AssetListItem) => void
    actions?: Snippet<[AssetListItem]>
    class?: string
  } = $props()

  const status = $derived(resolveStatus(unit))
  const isUnavailable = $derived(unit.status === "unavailable")
  const isOut = $derived(unit.custody !== null)
  const addable = $derived(canAdd && !isUnavailable && !isOut && onAdd !== undefined)

  /** Why **Add** is disabled, shown in a tooltip rather than left to guesswork. */
  const blockedReason = $derived(
    inCart
      ? "Already in your cart"
      : isUnavailable
        ? "Marked unavailable — ask an admin"
        : isOut
          ? "Someone has this one out"
          : !canAdd
            ? "Return your overdue item first"
            : null
  )
</script>

<div
  class={cn(
    "flex items-center gap-2 border-b border-line pr-2 last:border-b-0",
    "transition-colors duration-(--dur-fast) ease-(--ease-brand)",
    highlighted && "bg-status-available-bg",
    className
  )}
>
  <button
    type="button"
    onclick={() => onOpen?.(unit)}
    disabled={!onOpen}
    class={cn(
      "flex min-h-(--row-h) flex-1 items-center gap-3 px-3 py-1.5 text-left",
      "rounded-none hover:bg-raised disabled:pointer-events-none",
      // Unavailable units dim their fill and thumbnail but never their serial or
      // status label: the row still has to be readable to say *why* it's out of
      // play (§8.2).
      isUnavailable && "opacity-100"
    )}
  >
    <PhotoFrame
      src={unit.photo_url}
      alt={unit.name}
      class={cn("size-8 rounded-sm", isUnavailable && "opacity-55")}
      iconClass="size-3.5"
    />

    <span class="flex min-w-0 flex-1 items-center gap-3">
      <Serial value={unit.serial_number ?? unit.asset_tag} class="shrink-0" />
      {#if showName}
        <span class="truncate text-fg">{unit.name}</span>
      {/if}
      {#if unit.condition}
        <span class="hidden truncate text-fg-faint xl:inline">{unit.condition}</span>
      {/if}
    </span>

    <StatusDot {status} class="shrink-0" />

    <!--
      Who holds it, visible to any signed-in viewer (§8.3, resolved 2026-09-12):
      a student being able to find who has the lens they want outweighs
      withholding it. The name and due date only — the student number is the
      scan-login key and is already null in the payload for a non-admin.
      Shed below 1280px, per the §7.2 breakpoint table.
    -->
    <span class="hidden w-40 shrink-0 truncate text-right text-fg-muted xl:inline">
      {#if unit.custody}
        {custodianLine(unit.custody, viewerId)}
      {/if}
    </span>
  </button>

  {#if actions}
    <div class="flex shrink-0 items-center gap-1">{@render actions(unit)}</div>
  {:else if onAdd}
    {#if addable && !inCart}
      <Button size="sm" variant="secondary" onclick={() => onAdd?.(unit)}>
        <PlusIcon aria-hidden="true" />
        Add
      </Button>
    {:else}
      <Tooltip.Provider>
        <Tooltip.Root>
          <Tooltip.Trigger>
            {#snippet child({ props })}
              <span {...props}>
                <Button size="sm" variant="secondary" disabled aria-label={blockedReason ?? "Cannot add"}>
                  {#if inCart}
                    <CheckIcon aria-hidden="true" />
                    In cart
                  {:else}
                    <PlusIcon aria-hidden="true" />
                    Add
                  {/if}
                </Button>
              </span>
            {/snippet}
          </Tooltip.Trigger>
          <Tooltip.Content>{blockedReason}</Tooltip.Content>
        </Tooltip.Root>
      </Tooltip.Provider>
    {/if}
  {/if}
</div>
