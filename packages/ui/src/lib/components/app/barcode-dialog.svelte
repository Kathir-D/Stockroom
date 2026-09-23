<script lang="ts">
  /**
   * One unit's barcode, on screen (CLAUDE.md §13, Phase B).
   *
   * The recovery path for the failure a school actually has: a sticker peels
   * off, somebody finds the item by name, and needs its barcode back. Shown
   * here big enough to scan straight off the monitor, with a download for
   * printing one replacement.
   */
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import * as api from "../../api/index"
  import { saveBlob } from "../../download"

  let {
    open = $bindable(false),
    assetId,
    name,
    serial,
  }: { open?: boolean; assetId: string | null; name: string; serial: string } = $props()

  let blob = $state<Blob | null>(null)
  let url = $state<string | null>(null)
  let error = $state<string | null>(null)

  $effect(() => {
    if (!open || !assetId) return
    let cancelled = false
    error = null
    api.assetBarcode(assetId).then(
      (b) => {
        if (cancelled) return
        blob = b
        url = URL.createObjectURL(b)
      },
      (err) => {
        if (!cancelled) error = err instanceof Error ? err.message : String(err)
      },
    )
    return () => {
      cancelled = true
      if (url) URL.revokeObjectURL(url)
      url = null
      blob = null
    }
  })
</script>

<Dialog.Root bind:open>
  <Dialog.Content>
    <Dialog.Header>
      <Dialog.Title>{name}</Dialog.Title>
      <Dialog.Description>
        <span class="font-mono">{serial}</span> — scan it straight off the screen to test a
        scanner, or download it to print one replacement sticker.
      </Dialog.Description>
    </Dialog.Header>
    <!-- White whatever the theme: a scanner reads dark bars on a light ground,
         and on the dark theme's surface this would be unreadable. -->
    <div class="flex min-h-24 items-center justify-center rounded-(--radius) bg-white p-4">
      {#if url}
        <img src={url} alt="Barcode for {serial}" class="max-w-full" />
      {:else if error}
        <p class="text-sm text-status-overdue" role="alert">{error}</p>
      {:else}
        <p class="text-sm text-fg-faint">Loading…</p>
      {/if}
    </div>
    <Dialog.Footer>
      <Button variant="ghost" onclick={() => (open = false)}>Close</Button>
      <Button disabled={!blob} onclick={() => blob && saveBlob(blob, `${serial}.png`)}>Download</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
