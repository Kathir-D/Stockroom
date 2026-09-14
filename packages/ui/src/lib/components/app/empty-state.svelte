<script lang="ts">
  /**
   * Left-aligned, not a centred hero: content in this app is scannable, and
   * centred hero layouts inside the app are on the §13 list of things that make
   * a screen look like any other SaaS product.
   *
   * The browse screen uses it twice with different words, because "this category
   * has no models yet" (true for `Primes`) and "your filters matched nothing"
   * need different actions (§8.2).
   */
  import type { Snippet } from "svelte"
  import { cn } from "@stockroom/ui/utils"

  let {
    title,
    description,
    action,
    class: className,
  }: {
    title: string
    description?: string
    action?: Snippet
    class?: string
  } = $props()
</script>

<div class={cn("rounded-xl border border-line-strong bg-surface p-(--gutter)", className)}>
  <p class="font-medium text-fg">{title}</p>
  {#if description}
    <p class="mt-1 max-w-prose text-fg-muted">{description}</p>
  {/if}
  {#if action}
    <div class="mt-3 flex gap-2">{@render action()}</div>
  {/if}
</div>
