<script lang="ts">
  /**
   * An asset or profile photo, with the fallback for the common case: real
   * inventory entry starts in Week 7 and most units will have no picture for a
   * while.
   *
   * `alt` is the asset's name, never "photo" (design-system.md §10). The URL
   * comes from the payload's `photo_url` through `fileUrl`, because the photo is
   * served by the Go server off `UPLOADS_DIR` at `/files/` and the frontend runs
   * on a different origin.
   */
  import ImageOffIcon from "@lucide/svelte/icons/image-off"
  import { cn } from "@stockroom/ui/utils"
  import { fileUrl } from "../../api/index"

  let {
    src,
    alt,
    class: className,
    iconClass = "size-5",
  }: {
    src: string | null | undefined
    alt: string
    class?: string
    iconClass?: string
  } = $props()

  let failed = $state(false)
  const resolved = $derived(fileUrl(src))
</script>

<div
  class={cn(
    "flex shrink-0 items-center justify-center overflow-hidden rounded-sm bg-raised",
    className
  )}
>
  {#if resolved && !failed}
    <img
      src={resolved}
      {alt}
      loading="lazy"
      class="size-full object-cover"
      onerror={() => (failed = true)}
    />
  {:else}
    <!-- Decorative: the name is already beside it in every place this renders. -->
    <ImageOffIcon class={cn("text-fg-faint", iconClass)} aria-hidden="true" />
  {/if}
</div>
