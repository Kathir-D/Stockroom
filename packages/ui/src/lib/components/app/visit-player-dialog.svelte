<script lang="ts">
  /**
   * Plays one closet visit's recording, admin-only.
   *
   * The clip is fetched as a Blob (a `<video src>` cannot carry the token) and
   * the server logs the fetch as "watched the recording" -- which is why it is
   * fetched when the dialog opens and not before: opening the timeline must not
   * write a "watched" row for every visit on screen.
   */
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import * as api from "../../api/index"
  import type { ClosetVisit } from "../../api/types"
  import { dateTime } from "../../status"
  import { saveBlob } from "../../download"

  let {
    open = $bindable(false),
    visit,
    onKeepChanged,
  }: { open?: boolean; visit: ClosetVisit | null; onKeepChanged?: (v: ClosetVisit) => void } = $props()

  let url = $state<string | null>(null)
  let blob = $state<Blob | null>(null)
  let error = $state<string | null>(null)
  let saving = $state(false)

  // Keyed on the id, not the object: marking a visit keep hands back a new
  // object for the same visit, and refetching would log another viewing.
  const visitId = $derived(visit?.id)

  $effect(() => {
    const id = visitId
    if (!open || !id) return
    let cancelled = false
    let objectUrl: string | null = null
    error = null
    url = null
    // Or Download would save the last visit's clip under this one's name.
    blob = null
    api.visitClip(id).then(
      (b) => {
        if (cancelled) return
        blob = b
        objectUrl = URL.createObjectURL(b)
        url = objectUrl
      },
      (err) => {
        if (!cancelled) error = err instanceof Error ? err.message : String(err)
      },
    )
    return () => {
      cancelled = true
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  })

  async function toggleKeep() {
    if (!visit) return
    saving = true
    try {
      const next = await api.keepVisit(visit.id, !visit.keep)
      onKeepChanged?.(next)
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      saving = false
    }
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="sm:max-w-3xl">
    <Dialog.Header>
      <Dialog.Title>Closet visit</Dialog.Title>
      <Dialog.Description>
        {#if visit}
          {dateTime(visit.started_at)}{visit.ended_at ? ` to ${dateTime(visit.ended_at)}` : ", still in the closet"}.
          Watching this is recorded in the activity log.
        {/if}
      </Dialog.Description>
    </Dialog.Header>

    {#if error}
      <p class="text-sm text-status-overdue">{error}</p>
    {:else if url}
      <!-- svelte-ignore a11y_media_has_caption -->
      <video src={url} controls autoplay class="w-full rounded-md bg-black"></video>
    {:else}
      <div class="flex aspect-video w-full items-center justify-center rounded-md bg-ground text-sm text-fg-muted">
        Loading the recording…
      </div>
    {/if}

    <Dialog.Footer class="gap-2">
      {#if visit}
        <Button variant="secondary" disabled={saving} onclick={toggleKeep}>
          {visit.keep ? "Release to retention" : "Keep this recording"}
        </Button>
      {/if}
      {#if blob && visit}
        <Button variant="ghost" onclick={() => blob && visit && saveBlob(blob, `closet-${visit.started_at.slice(0, 16).replace(":", "")}.mp4`)}>
          Download
        </Button>
      {/if}
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
