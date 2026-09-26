<script lang="ts">
  /**
   * A closet visit's snapshot on the activity timeline.
   *
   * Fetched as a Blob only when it scrolls near the viewport: the timeline can
   * hold hundreds of visits, every snapshot is behind the admin's token (an
   * `<img src>` cannot carry it), and a page that downloads all of them up
   * front is a page that takes a minute to open.
   */
  import CameraOffIcon from "@lucide/svelte/icons/camera-off"
  import * as api from "../../api/index"

  let { visitId, available }: { visitId: string; available: boolean } = $props()

  let el = $state<HTMLElement | null>(null)
  let url = $state<string | null>(null)
  let failed = $state(false)

  $effect(() => {
    if (!el || !available) return
    let cancelled = false
    let objectUrl: string | null = null
    const io = new IntersectionObserver(
      (entries) => {
        if (!entries.some((e) => e.isIntersecting)) return
        io.disconnect()
        api.visitSnapshot(visitId).then(
          (blob) => {
            if (cancelled) return
            objectUrl = URL.createObjectURL(blob)
            url = objectUrl
          },
          () => {
            if (!cancelled) failed = true
          },
        )
      },
      { rootMargin: "300px" },
    )
    io.observe(el)
    return () => {
      cancelled = true
      io.disconnect()
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  })
</script>

<div
  bind:this={el}
  class="flex aspect-video w-32 shrink-0 items-center justify-center overflow-hidden rounded-md border border-line bg-ground"
>
  {#if url}
    <img src={url} alt="Snapshot of the visit" class="size-full object-cover" />
  {:else if !available || failed}
    <CameraOffIcon class="size-4 text-fg-faint" aria-label="No snapshot" />
  {/if}
</div>
