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
   */
  import { toast } from "svelte-sonner"
  import { cn } from "@stockroom/ui/utils"

  let {
    value,
    class: className,
    label = "serial number",
  }: {
    value: string | null | undefined
    class?: string
    /** What the toast and the aria-label call it: serial, student number, ... */
    label?: string
  } = $props()

  async function copy() {
    if (!value) return
    try {
      await navigator.clipboard.writeText(value)
      toast.success(`Copied ${label} ${value}`)
    } catch {
      // A webview with no clipboard permission still selects the text, which is
      // the fallback that has always worked. Nothing to apologise for.
      toast.error("Could not copy — select the text instead")
    }
  }
</script>

{#if value}
  <button
    type="button"
    onclick={(event) => {
      // Serials sit inside accordion triggers and table rows that are themselves
      // clickable; copying must not also toggle the row.
      event.stopPropagation()
      copy()
    }}
    title={`Copy ${label}`}
    aria-label={`${label} ${value}, click to copy`}
    class={cn(
      "cursor-pointer rounded-sm font-mono text-[0.9em] tracking-tight text-fg select-all",
      "hover:bg-raised focus-visible:bg-raised",
      className
    )}
  >
    {value}
  </button>
{:else}
  <span class={cn("font-mono text-[0.9em] text-fg-faint", className)} aria-label="no {label}">—</span>
{/if}
