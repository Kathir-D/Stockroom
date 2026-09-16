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
  import XIcon from "@lucide/svelte/icons/x"
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
    /**
     * Kept for the admin table's call site. The name is now always shown —
     * every row carries `[photo] name · serial · status · add` — so this no
     * longer changes anything, and the next edit here should delete it.
     */
    showName = false,
    onOpen,
    onAdd,
    onRemove,
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
    /** Takes it back out. Same button as Add, which is the point (see below). */
    onRemove?: (unit: AssetListItem) => void
    actions?: Snippet<[AssetListItem]>
    class?: string
  } = $props()

  const status = $derived(resolveStatus(unit))
  const isUnavailable = $derived(unit.status === "unavailable")
  const isOut = $derived(unit.custody !== null)
  /**
   * Addable only when `resolveStatus` calls the unit available.
   *
   * Spelling the same test out here (`!isUnavailable && !isOut`) let the two
   * disagree: `checked_out` with no open custody row is status drift, which
   * `resolveStatus` reports as unavailable and the dot renders as such, while
   * the hand-rolled test saw an item on the shelf and offered **Add**. One
   * source for what state a unit is in (status.ts), as the file says.
   */
  const addable = $derived(status.state === "available" && canAdd && onAdd !== undefined)

  /** Why **Add** is disabled, shown in a tooltip rather than left to guesswork. */
  const blockedReason = $derived(
    isUnavailable
      ? "Marked unavailable — ask an admin"
      : isOut
        ? "Someone has this one out"
        : !canAdd
          ? "Return your overdue item first"
          : status.state !== "available"
            ? `Not available — ${status.label.toLowerCase()}`
            : null
  )
</script>

<div
  class={cn(
    // Its own rounded edge rather than a rule between rows. overflow-hidden is
    // what keeps the inner button's hover fill inside the corners. The border is
    // --line-control, not --line: the inner button is rounded-none, so this
    // wrapper *is* the clickable control's boundary, and §10 needs 3:1 for that
    // (--line is decorative at 1.50).
    "flex items-center gap-2 overflow-hidden rounded-(--radius) border border-line-control pr-2",
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

    <!--
      Name, then the serial right beside it, both left-aligned: the two together
      are what identifies the unit, and reading them takes one fixation instead
      of a jump across the row. The name truncates under pressure, the serial
      does not -- it is the scan key, and half of it is no use against the
      sticker in someone's hand (it has its own middle-truncation past 14
      characters; see <Serial>).
    -->
    <span class="flex min-w-0 flex-1 items-center gap-3">
      <span class="truncate text-fg">{unit.name}</span>
      <Serial value={unit.serial_number ?? unit.asset_tag} class="shrink-0" />
    </span>

    {#if unit.condition}
      <span class="hidden w-28 shrink-0 truncate text-fg-faint xl:inline">{unit.condition}</span>
    {/if}

    <!--
      Status, and who holds it *on hover only*.

      The custodian is still visible to any signed-in viewer (§8.3, resolved
      2026-09-12) — a student being able to find who has the lens they want
      outweighs withholding it — but it is no longer a column. It was the widest
      thing in the row and the least often needed, and burying it behind the
      hover keeps the row scannable while leaving the answer one gesture away.
      The name and due date only: the student number is the scan-login key and is
      already null in the payload for a non-admin.
    -->
    <span class="w-44 shrink-0 text-left">
      {#if unit.custody}
        <!-- Bound out here: the `{#if}` narrowing doesn't reach inside a
             snippet's closure, so `unit.custody` reads as nullable in there. -->
        {@const holder = custodianLine(unit.custody, viewerId)}
        <Tooltip.Provider>
          <Tooltip.Root>
            <Tooltip.Trigger>
              {#snippet child({ props })}
                <span {...props} title={holder}>
                  <StatusDot {status} />
                </span>
              {/snippet}
            </Tooltip.Trigger>
            <Tooltip.Content>{holder}</Tooltip.Content>
          </Tooltip.Root>
        </Tooltip.Provider>
      {:else}
        <StatusDot {status} />
      {/if}
    </span>
  </button>

  {#if actions}
    <div class="flex shrink-0 items-center gap-1">{@render actions(unit)}</div>
  {:else if onAdd}
    {#if inCart}
      <!--
        **The same button takes it back out.** A row that has been added shows
        `In cart`, and pressing it removes the item — undo lives exactly where
        the action was, which is the one place someone looks for it. It used to
        be a disabled `In cart` label, which said what happened and gave no way
        to change your mind without finding the cart page.

        The icon swaps to an X on hover so the press is predictable before it
        happens, rather than a check that silently means "remove".
      -->
      <Tooltip.Provider>
        <Tooltip.Root>
          <Tooltip.Trigger>
            {#snippet child({ props })}
              <Button
                {...props}
                size="sm"
                variant="secondary"
                class="group"
                aria-label={`Remove ${unit.name} from your cart`}
                onclick={() => onRemove?.(unit)}
              >
                <CheckIcon class="group-hover:hidden" aria-hidden="true" />
                <XIcon class="hidden group-hover:block" aria-hidden="true" />
                In cart
              </Button>
            {/snippet}
          </Tooltip.Trigger>
          <Tooltip.Content>Remove from cart</Tooltip.Content>
        </Tooltip.Root>
      </Tooltip.Provider>
    {:else if addable}
      <Button
        size="sm"
        variant="secondary"
        aria-label={`Add ${unit.name} to your cart`}
        onclick={() => onAdd?.(unit)}
      >
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
                  <PlusIcon aria-hidden="true" />
                  Add
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
