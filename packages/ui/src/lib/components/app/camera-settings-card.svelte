<script lang="ts">
  /**
   * Admin → Settings → Closet camera (ROADMAP §2.1–2.3).
   *
   * The camera itself runs outside Stockroom: an open-source person detector
   * (Frigate) watching a USB webcam, installed beside the database by
   * `deploy/camera/camera.sh`. This card only points Stockroom at it, chooses
   * where visit recordings are kept on this machine, and says how long.
   *
   * Its own card with its own Save, like every card on this screen, and its
   * own endpoint (`/admin/camera`): the camera is a separate subsystem that a
   * school may never turn on, and nothing here is a backup setting. Recordings
   * stay on this machine -- never Drive, never GitHub, never in a backup.
   */
  import CameraIcon from "@lucide/svelte/icons/camera"
  import FolderOpenIcon from "@lucide/svelte/icons/folder-open"
  import { toast } from "svelte-sonner"
  import { Button } from "@stockroom/ui/components/ui/button"
  import { Checkbox } from "@stockroom/ui/components/ui/checkbox"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import FolderPicker, { type FolderChoice } from "./folder-picker.svelte"
  import * as api from "../../api/index"
  import type { CameraOverview } from "../../api/types"
  import { formatBytes } from "../../status"
  import { router } from "../../stores/router.svelte"

  let overview = $state<CameraOverview | null>(null)
  let loadError = $state<string | null>(null)
  let error = $state<string | null>(null)
  let saving = $state(false)
  let testing = $state(false)
  let testResult = $state<string | null>(null)
  let pickerOpen = $state(false)

  // Text, not numbers: see settings.svelte on why a number field cannot be
  // trusted to hand back what is in the box.
  let draft = $state({
    enabled: false,
    detector_url: "",
    camera_name: "",
    recordings_dir: "",
    retention_days: "",
    min_free_gb: "",
    min_visit_seconds: "",
  })

  function adopt(o: CameraOverview) {
    overview = o
    const s = o.settings
    draft = {
      enabled: s.enabled,
      detector_url: s.detector_url,
      camera_name: s.camera_name,
      recordings_dir: s.recordings_dir,
      retention_days: String(s.retention_days),
      min_free_gb: String(s.min_free_gb),
      min_visit_seconds: String(s.min_visit_seconds),
    }
  }

  async function load() {
    try {
      adopt(await api.camera())
      loadError = null
    } catch (err) {
      loadError = err instanceof Error ? err.message : String(err)
    }
  }

  $effect(() => {
    load()
    // The status line is live: poll while the screen is open.
    const t = setInterval(() => {
      api.camera().then((o) => (overview = o), () => {})
    }, 10_000)
    return () => clearInterval(t)
  })

  function whole(value: string, label: string, min: number, max: number): number {
    const n = Number(value.trim())
    if (!value.trim() || !Number.isInteger(n) || n < min || n > max) {
      throw new Error(`${label} must be a whole number from ${min} to ${max}.`)
    }
    return n
  }

  async function save() {
    error = null
    saving = true
    try {
      adopt(
        await api.saveCamera({
          enabled: draft.enabled,
          detector_url: draft.detector_url.trim(),
          camera_name: draft.camera_name.trim(),
          recordings_dir: draft.recordings_dir.trim(),
          retention_days: whole(draft.retention_days, "Keep recordings for", 1, 3650),
          min_free_gb: whole(draft.min_free_gb, "Warn below", 0, 100000),
          min_visit_seconds: whole(draft.min_visit_seconds, "Ignore visits shorter than", 0, 600),
        }),
      )
      toast.success("Closet camera settings saved")
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      saving = false
    }
  }

  async function test() {
    testing = true
    testResult = null
    error = null
    try {
      const h = await api.testCamera({ detector_url: draft.detector_url.trim(), camera_name: draft.camera_name.trim() })
      testResult = h.camera_online
        ? `${h.message}. Detection takes about ${h.inference_ms} ms per frame.`
        : h.message
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      testing = false
    }
  }

  async function pick(choice: FolderChoice) {
    draft.recordings_dir = choice.path
  }

  const status = $derived(overview?.status)
  const STATE_WORD: Record<string, string> = {
    off: "Off",
    starting: "Starting",
    online: "Online",
    camera_offline: "Camera offline",
    detector_down: "Detector not running",
    error: "Error",
  }
</script>

<section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
  <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
    <CameraIcon class="size-4 text-fg-muted" aria-hidden="true" />
    Closet camera
  </h2>
  <p class="text-xs text-fg-muted">
    A webcam over the closet door records each visit, from when someone walks in to when they walk out,
    and puts it on the <button
      type="button"
      class="underline underline-offset-2 hover:text-fg"
      onclick={() => router.go({ name: "admin", tab: "activity" })}>Activity</button
    > timeline beside the sign-ins and scans. Recordings stay on this machine. They never go to Google Drive,
    GitHub or a backup, and they are deleted after the number of days below unless you mark one keep. Recording
    students needs your school's approval and a sign on the door.
  </p>

  {#if loadError}
    <p class="text-sm text-status-overdue" role="alert">{loadError}</p>
  {:else if overview}
    {#if status && draft.enabled}
      <div class="flex flex-col gap-0.5 rounded-lg border border-line bg-ground p-2 text-xs">
        <span class="font-medium {status.state === 'online' ? 'text-status-available' : 'text-status-due-soon'}">
          {STATE_WORD[status.state] ?? status.state}
        </span>
        <span class="text-fg-muted">{status.message}</span>
        {#if status.state === "online"}
          <span class="text-fg-muted">
            {status.in_closet} in the closet now · {status.visits_today} visits today · {status.recordings} recordings kept
            {#if status.free_bytes != null}· {formatBytes(status.free_bytes)} free{/if}
          </span>
        {/if}
        {#each status.warnings as w (w)}
          <span class="text-status-due-soon">{w}</span>
        {/each}
      </div>
    {/if}

    <div class="flex items-start gap-2">
      <Checkbox id="cam-enabled" bind:checked={draft.enabled} />
      <Label for="cam-enabled">Record closet visits</Label>
    </div>

    <div class="flex flex-col gap-1">
      <Label for="cam-dir">Recordings folder</Label>
      <div class="flex gap-2">
        <Input id="cam-dir" bind:value={draft.recordings_dir} placeholder="Press Choose to pick one" spellcheck={false} />
        <Button variant="secondary" onclick={() => (pickerOpen = true)}>
          <FolderOpenIcon aria-hidden="true" /> Choose…
        </Button>
      </div>
      <p class="text-xs text-fg-faint">About half a gigabyte per hour of visits. An hour a day for 30 days is roughly 15 GB.</p>
    </div>

    <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
      <div class="flex flex-col gap-1">
        <Label for="cam-days">Keep recordings for (days)</Label>
        <Input id="cam-days" inputmode="numeric" bind:value={draft.retention_days} />
      </div>
      <div class="flex flex-col gap-1">
        <Label for="cam-free">Warn below (GB free)</Label>
        <Input id="cam-free" inputmode="numeric" bind:value={draft.min_free_gb} />
      </div>
      <div class="flex flex-col gap-1">
        <Label for="cam-min">Ignore visits shorter than (s)</Label>
        <Input id="cam-min" inputmode="numeric" bind:value={draft.min_visit_seconds} />
      </div>
    </div>

    <details class="text-xs text-fg-muted">
      <summary class="cursor-pointer">Detector</summary>
      <div class="mt-2 grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div class="flex flex-col gap-1">
          <Label for="cam-url">Detector address</Label>
          <Input id="cam-url" bind:value={draft.detector_url} spellcheck={false} />
        </div>
        <div class="flex flex-col gap-1">
          <Label for="cam-name">Camera name</Label>
          <Input id="cam-name" bind:value={draft.camera_name} spellcheck={false} />
        </div>
      </div>
      <p class="mt-1">
        Frigate, started by <code>deploy/camera/camera.sh up</code>. The defaults match it.
      </p>
    </details>

    {#if testResult}
      <p class="text-sm text-fg">{testResult}</p>
    {/if}
    {#if error}
      <p class="text-sm text-status-overdue" role="alert">{error}</p>
    {/if}
    <div class="flex gap-2">
      <Button disabled={saving} onclick={save}>{saving ? "Saving…" : "Save camera"}</Button>
      <Button variant="secondary" disabled={testing} onclick={test}>
        {testing ? "Testing…" : "Test connection"}
      </Button>
    </div>
  {/if}
</section>

<FolderPicker
  bind:open={pickerOpen}
  source="local"
  title="Choose where closet recordings are kept"
  description="A folder on this machine with room to grow. Recordings never leave it."
  pickLabel="Use this folder"
  onpick={pick}
/>
