<script lang="ts">
  /**
   * Every serial number and student number in the app renders through this
   * (design-system.md §5.2).
   *
   * Mono, because an identifier is read character by character and a
   * proportional `1`/`l`/`I` costs someone a trip to the shelf. Click copies and
   * toasts; `user-select: text` is forced on because the app chrome disables
   * selection in the Wails build (§12) and a serial is exactly the thing
   * somebody wants to select.
   *
   * Two display rules, both of which keep the *stored* value untouched — the
   * serial is the scan key, so what the tooltip shows and what a click copies is
   * always the whole thing:
   *
   * 1. **A model-prefixed serial shows only its number.** `T7IBAT-001` renders
   *    `1`. Batteries, bags and tripods have no manufacturer serial, so their
   *    stickers carry a serial we generate — the prefix is the same for every
   *    unit of the model and the model name is already in the row beside it, so
   *    printing it again is noise. The number is the only part that identifies
   *    the unit.
   * 2. **Anything longer than `max` truncates in the middle**, `3QZB…8842`,
   *    never at the end. Manufacturer serials tend to share a long prefix across
   *    a production run, so a trailing ellipsis would render every unit of a
   *    model identical — the one thing this component exists to prevent.
   */
  import { toast } from "svelte-sonner"
  import * as Tooltip from "@stockroom/ui/components/ui/tooltip"
  import { cn } from "@stockroom/ui/utils"

  let {
    value,
    class: className,
    label = "serial number",
    shortenToUnitNumber = true,
    /**
     * Longest serial shown whole. Past this it becomes `head…tail`, which is
     * `HEAD + TAIL + 1` characters wide — so the cap has to exceed that or
     * truncating would make the string longer.
     */
    max = 14,
  }: {
    value: string | null | undefined
    class?: string
    /** What the toast and the aria-label call it: serial, student number, ... */
    label?: string
    max?: number
    /**
     * Whether rule 1 applies. False where the whole identifier *is* the point
     * rather than which unit it picks out — the asset tag in the detail dialog,
     * which sits right beside the serial and would otherwise render as the same
     * bare number.
     */
    shortenToUnitNumber?: boolean
  } = $props()

  const HEAD = 4
  const TAIL = 4

  /**
   * A serial we generated rather than one a manufacturer stamped: a prefix, a
   * hyphen, and a unit number. Real serials in this inventory are unbroken runs
   * of letters and digits, so requiring the hyphen is what keeps the two apart
   * without a column in the database saying which is which.
   */
  const MODEL_PREFIXED = /^[A-Za-z][\w.]*-0*(\d+)$/

  const shown = $derived.by(() => {
    if (!value) return ""
    const unitNumber = shortenToUnitNumber ? MODEL_PREFIXED.exec(value) : null
    if (unitNumber) return unitNumber[1]
    if (value.length > max) return `${value.slice(0, HEAD)}…${value.slice(-TAIL)}`
    return value
  })
  /** True whenever what's on screen isn't the whole serial, either way. */
  const truncated = $derived(value !== null && value !== undefined && shown !== value)

  async function copy() {
    if (!value) return
    try {
      // Always the whole serial, never the truncated form: what lands on the
      // clipboard has to be what the barcode encodes.
      await navigator.clipboard.writeText(value)
      toast.success(`Copied ${label} ${value}`)
    } catch {
      // A webview with no clipboard permission still selects the text, which is
      // the fallback that has always worked. Nothing to apologise for.
      toast.error("Could not copy — select the text instead")
    }
  }
</script>

{#snippet button(props: Record<string, unknown> = {})}
  <button
    {...props}
    type="button"
    onclick={(event: MouseEvent) => {
      // Serials sit inside accordion triggers and table rows that are themselves
      // clickable; copying must not also toggle the row.
      event.stopPropagation()
      copy()
    }}
    aria-label={`${label} ${value}, click to copy`}
    class={cn(
      "cursor-pointer rounded-sm font-mono text-[0.9em] tracking-tight text-fg select-all",
      "hover:bg-raised focus-visible:bg-raised",
      className
    )}
  >
    {shown}
  </button>
{/snippet}

{#if value}
  {#if truncated}
    <!-- The tooltip carries the full value; the native title is the fallback for
         a touch or a screen reader that never fires a hover. -->
    <Tooltip.Provider>
      <Tooltip.Root>
        <Tooltip.Trigger>
          {#snippet child({ props })}
            {@render button({ ...props, title: value })}
          {/snippet}
        </Tooltip.Trigger>
        <Tooltip.Content>
          <span class="font-mono">{value}</span>
        </Tooltip.Content>
      </Tooltip.Root>
    </Tooltip.Provider>
  {:else}
    {@render button({ title: `Copy ${label}` })}
  {/if}
{:else}
  <span class={cn("font-mono text-[0.9em] text-fg-faint", className)} aria-label="no {label}">—</span>
{/if}
