<script lang="ts">
  /**
   * The one way status is expressed in this app (design-system.md §6).
   *
   * Default is `C1`, a 7px filled dot followed by the word in the same colour.
   * The word is never omitted and the dot is never used alone: a colour-blind
   * user and a greyscale printout both have to work (§1.3).
   *
   * `variant="chip"` is `C2`, the filled pill, and is correct in exactly two
   * places: the asset detail dialog, where there is one status on screen and it
   * should be unmissable, and the scan confirmation, where it is the entire
   * message.
   */
  import { cn } from "@stockroom/ui/utils"
  import type { ResolvedStatus } from "../../status"

  let {
    status,
    variant = "dot",
    class: className,
  }: {
    status: ResolvedStatus
    variant?: "dot" | "chip"
    class?: string
  } = $props()
</script>

{#if variant === "chip"}
  <span
    data-status={status.state}
    class={cn(
      "inline-flex items-center gap-2 rounded-sm px-2.5 py-1 text-xs font-semibold",
      status.bg,
      status.fg,
      className
    )}
  >
    <span class="size-[7px] shrink-0 rounded-full bg-current" aria-hidden="true"></span>
    {status.label}
  </span>
{:else}
  <span
    data-status={status.state}
    class={cn("inline-flex min-w-0 items-center gap-1.5 whitespace-nowrap", status.fg, className)}
  >
    <span class="size-[7px] shrink-0 rounded-full bg-current" aria-hidden="true"></span>
    <!-- truncate, not clip: the unit row's status column carries the holder's
         name now, so the one string in it whose length nobody controls is a
         person. It only ever engages when a parent constrains the width. -->
    <span class="truncate text-[11.5px] font-[650] tracking-[0.01em]">{status.label}</span>
  </span>
{/if}
