<script lang="ts">
  /**
   * An asset's photo, from wherever it is: a file on disk, a file dragged in,
   * an image pasted from the clipboard, or the webcam on the closet PC.
   *
   * Every way in ends at the same preview, and nothing uploads until Save.
   * The checks in photo-file.ts match the server's, so a file the server
   * would refuse is refused here before the wait.
   *
   * The camera is released whenever the dialog closes, however it closes.
   * A webcam left open keeps its light on and blocks the closet camera and
   * any video call from using it.
   */
  import { untrack } from "svelte"
  import CameraIcon from "@lucide/svelte/icons/camera"
  import UploadIcon from "@lucide/svelte/icons/upload"
  import { toast } from "svelte-sonner"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import * as api from "../../api/index"
  import type { AssetDetail } from "../../api/types"
  import { cameraErrorMessage, cameraSupported, checkPhoto, PHOTO_RULES } from "../../photo-file"

  let {
    asset = $bindable(null),
    onSaved,
  }: {
    /** The asset to photograph. null means the dialog is closed. */
    asset?: { id: string; name: string } | null
    onSaved?: (detail: AssetDetail) => void
  } = $props()

  let file = $state<File | null>(null)
  let previewUrl = $state<string | null>(null)
  let error = $state<string | null>(null)
  let saving = $state(false)
  let dragging = $state(false)

  let stream = $state<MediaStream | null>(null)
  let starting = $state(false)
  let video = $state<HTMLVideoElement | null>(null)
  let picker = $state<HTMLInputElement | null>(null)

  const open = $derived(asset !== null)

  // Open and close. untrack keeps the reset from re-running when the state
  // it writes changes: only `open` should trigger this.
  $effect(() => {
    const isOpen = open
    untrack(() => {
      stopCamera()
      setFile(null)
      error = null
      saving = false
      dragging = false
      if (!isOpen) starting = false
    })
  })

  // A paste anywhere while the dialog is open. The dialog has no text field,
  // so a paste can only mean a picture.
  $effect(() => {
    if (!open) return
    const onPaste = (e: ClipboardEvent) => {
      const item = Array.from(e.clipboardData?.items ?? []).find(
        (i) => i.kind === "file" && i.type.startsWith("image/"),
      )
      const pasted = item?.getAsFile()
      if (!pasted) {
        error = "The clipboard has no picture in it. Copy an image, then paste again."
        return
      }
      e.preventDefault()
      take(pasted)
    }
    window.addEventListener("paste", onPaste)
    return () => window.removeEventListener("paste", onPaste)
  })

  // Navigating away with the dialog open unmounts it without closing it.
  $effect(() => () =>
    untrack(() => {
      stopCamera()
      setFile(null)
    }),
  )

  // The live picture needs the stream attached once the <video> exists.
  $effect(() => {
    if (video && stream) video.srcObject = stream
  })

  function setFile(next: File | null) {
    if (previewUrl) URL.revokeObjectURL(previewUrl)
    file = next
    previewUrl = next ? URL.createObjectURL(next) : null
  }

  function take(candidate: File) {
    const r = checkPhoto(candidate)
    if (!r.ok) {
      error = r.message
      return
    }
    stopCamera()
    error = null
    setFile(r.file)
  }

  function onPick(e: Event) {
    const input = e.currentTarget as HTMLInputElement
    const chosen = input.files?.[0]
    // Clear it so choosing the same file again still fires a change.
    input.value = ""
    if (chosen) take(chosen)
  }

  function onDrop(e: DragEvent) {
    e.preventDefault()
    dragging = false
    const dropped = e.dataTransfer?.files?.[0]
    if (dropped) take(dropped)
    else error = "That wasn't a file. Drag a photo from a folder, or paste it."
  }

  async function startCamera() {
    error = null
    if (!cameraSupported()) {
      error = "This browser can't use a camera here. Open Stockroom at http://localhost:8080 in Chrome, Edge or Firefox, or choose a file."
      return
    }
    starting = true
    try {
      const s = await navigator.mediaDevices.getUserMedia({
        video: { width: { ideal: 1280 }, height: { ideal: 960 } },
        audio: false,
      })
      // The dialog may have closed while the permission prompt was up.
      if (!open) {
        s.getTracks().forEach((t) => t.stop())
        return
      }
      setFile(null)
      stream = s
    } catch (err) {
      error = cameraErrorMessage(err)
    } finally {
      starting = false
    }
  }

  function stopCamera() {
    stream?.getTracks().forEach((t) => t.stop())
    stream = null
    if (video) video.srcObject = null
  }

  function snap() {
    if (!video || !video.videoWidth) {
      error = "The camera hasn't shown a picture yet. Wait a moment and try again."
      return
    }
    const canvas = document.createElement("canvas")
    canvas.width = video.videoWidth
    canvas.height = video.videoHeight
    const ctx = canvas.getContext("2d")
    if (!ctx) {
      error = "The browser couldn't take the picture. Choose a file instead."
      return
    }
    ctx.drawImage(video, 0, 0)
    canvas.toBlob(
      (blob) => {
        if (!blob) {
          error = "The browser couldn't take the picture. Choose a file instead."
          return
        }
        take(new File([blob], "webcam.jpg", { type: "image/jpeg" }))
      },
      "image/jpeg",
      0.9,
    )
  }

  async function save() {
    if (!asset || !file) return
    saving = true
    error = null
    try {
      const detail = await api.setAssetPhoto(asset.id, file)
      toast.success(`Saved the photo for ${asset.name}`)
      onSaved?.(detail)
      asset = null
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      saving = false
    }
  }
</script>

<Dialog.Root {open} onOpenChange={(o) => !o && (asset = null)}>
  <Dialog.Content class="max-w-lg">
    <Dialog.Header>
      <Dialog.Title>Photo for {asset?.name}</Dialog.Title>
      <Dialog.Description>
        {PHOTO_RULES}. Drop a file here, paste one, or use the camera. It replaces any existing photo.
      </Dialog.Description>
    </Dialog.Header>

    {#if stream}
      <div class="flex flex-col gap-2">
        <!-- svelte-ignore a11y_media_has_caption -->
        <video
          bind:this={video}
          autoplay
          playsinline
          muted
          class="aspect-4/3 w-full rounded-(--radius) bg-ground object-cover"
        ></video>
        <div class="flex justify-end gap-2">
          <Button variant="ghost" onclick={stopCamera}>Stop the camera</Button>
          <Button onclick={snap}><CameraIcon aria-hidden="true" />Take the photo</Button>
        </div>
      </div>
    {:else if previewUrl}
      <div class="flex flex-col gap-2">
        <img
          src={previewUrl}
          alt="New photo for {asset?.name}"
          class="aspect-4/3 w-full rounded-(--radius) bg-ground object-contain"
        />
        <div class="flex justify-end gap-2">
          <Button variant="ghost" onclick={() => setFile(null)}>Choose another</Button>
        </div>
      </div>
    {:else}
      <div
        role="region"
        aria-label="Drop a photo here"
        class={[
          "flex flex-col items-center justify-center gap-3 rounded-(--radius) border-2 border-dashed p-8 text-center",
          dragging ? "border-ring bg-raised" : "border-line",
        ]}
        ondragenter={(e) => {
          e.preventDefault()
          dragging = true
        }}
        ondragover={(e) => {
          e.preventDefault()
          if (e.dataTransfer) e.dataTransfer.dropEffect = "copy"
        }}
        ondragleave={(e) => {
          if (!(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node | null)) dragging = false
        }}
        ondrop={onDrop}
      >
        <p class="text-sm text-fg-muted">Drop a photo here, or paste one with Ctrl+V.</p>
        <div class="flex flex-wrap justify-center gap-2">
          <Button variant="outline" onclick={() => picker?.click()}>
            <UploadIcon aria-hidden="true" />Choose a file
          </Button>
          <Button variant="outline" disabled={starting} onclick={startCamera}>
            <CameraIcon aria-hidden="true" />{starting ? "Starting the camera…" : "Use the camera"}
          </Button>
        </div>
        <input
          bind:this={picker}
          type="file"
          accept=".jpg,.jpeg,.png,.gif,.webp,image/jpeg,image/png,image/gif,image/webp"
          class="hidden"
          onchange={onPick}
        />
      </div>
    {/if}

    {#if error}
      <p class="text-sm text-status-overdue" role="alert">{error}</p>
    {/if}

    <Dialog.Footer>
      <Button variant="ghost" onclick={() => (asset = null)}>Cancel</Button>
      <Button disabled={!file || saving} onclick={save}>{saving ? "Saving…" : "Save"}</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
