<script lang="ts">
  /**
   * Admin → Photo wall (docs/design/signin-photo-wall.html §7).
   *
   * One job: choose which Google Drive folder the sign-in screen's two
   * scrolling columns read from, and make it possible to tell whether the
   * right one is live.
   *
   * **The field is write-only.** A Drive folder link is a capability, not a
   * label — a folder shared "anyone with the link" is readable by whoever
   * holds the URL, so the link currently in use is never sent to a client,
   * never rendered and never pre-filled. The project already holds this rule
   * for `app_settings.github_token` (CLAUDE.md §11) and for every password
   * field; this is that rule reaching one more value, and the server has a
   * test asserting neither response body contains the id or a
   * `drive.google.com` string.
   *
   * **What is shown instead** is the three things that answer an admin's real
   * question without handing back the capability: the name a person typed for
   * the folder, the counts, and a live preview strip. The strip is the actual
   * confirmation — "are the right photographs showing?" — and it reveals
   * nothing the sign-in screen does not already show to everyone who walks up
   * to the machine.
   *
   * **The probe is the point.** Replacing the folder validates the paste
   * against Drive *before* anything is written, so a typo or an unshared
   * folder is a message here rather than a wall that silently empties ten
   * minutes later with nothing on any screen connecting the two events.
   *
   * **Signing in to Google is the switch.** The wall reads Drive through the
   * one Google connection the backup uses too (google.go), so it is off until
   * an admin signs in — here, or with Connect under Settings → Google Drive —
   * and then starts on the running server, no `.env` line, no restart. Until then the button is marked Required and the folder form
   * waits on it. The token goes into rclone's own config file; this screen
   * only ever learns `google_connected`.
   */
  import ImagesIcon from "@lucide/svelte/icons/images"
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw"
  import FolderIcon from "@lucide/svelte/icons/folder"
  import LinkIcon from "@lucide/svelte/icons/link"
  import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert"
  import CheckIcon from "@lucide/svelte/icons/check"
  import KeyIcon from "@lucide/svelte/icons/key-round"
  import { toast } from "svelte-sonner"
  import { Button } from "@stockroom/ui/components/ui/button"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import { Skeleton } from "@stockroom/ui/components/ui/skeleton"
  import EmptyState from "@stockroom/ui/components/app/empty-state.svelte"
  import GoogleConnectDialog from "@stockroom/ui/components/app/google-connect-dialog.svelte"
  import * as api from "../../api/index"
  import { fileUrl } from "../../api/index"
  import type { PhotoWallStatus } from "../../api/types"
  import { dateTime } from "../../status"

  let status = $state<PhotoWallStatus | null>(null)
  let preview = $state<string[]>([])
  let loading = $state(true)
  let loadError = $state<string | null>(null)

  /**
   * The paste and its name. Both are cleared after a successful switch — the
   * link box in particular, because leaving a Drive link sitting in a text
   * field on a shared closet machine is the thing the write-only rule is
   * about.
   */
  let link = $state("")
  let label = $state("")
  let saving = $state(false)
  let saveError = $state<string | null>(null)
  let rebuilding = $state(false)

  async function load(silent = false) {
    if (!silent) loading = true
    loadError = null
    try {
      status = await api.photoWallStatus()
      // The preview is a second request rather than a field on the status,
      // because it must not consume the reel and the status is polled while a
      // listing runs. A failure here is not worth an error surface: the strip
      // is a confirmation, not the screen.
      try {
        preview = (await api.photoWallPreview()).photos
      } catch {
        preview = []
      }
    } catch (err) {
      loadError = err instanceof Error ? err.message : String(err)
    } finally {
      loading = false
    }
  }

  $effect(() => {
    load()
  })

  /**
   * Poll while a listing is running, and only then.
   *
   * §3's listing of a large folder takes minutes, during which the wall is
   * empty — correct, but indistinguishable from a broken feature unless this
   * screen keeps saying "12,400 files listed so far". Outside that window the
   * screen makes no traffic at all, which matters on a machine designed to
   * work with no internet.
   */
  $effect(() => {
    if (!status?.listing) return
    const timer = setInterval(() => load(true), 3000)
    return () => clearInterval(timer)
  })

  async function replaceFolder() {
    saving = true
    saveError = null
    try {
      status = await api.setPhotoWallFolder(link.trim(), label.trim())
      link = ""
      label = ""
      toast.success("Folder replaced — the wall is rebuilding")
      await load(true)
    } catch (err) {
      // Shown as written. The server's messages name the accepted link forms,
      // or say the folder is not shared with this machine's Google account,
      // which is what it almost always is — and a "failed" with no cause is a
      // button nobody presses twice.
      saveError = err instanceof Error ? err.message : String(err)
    } finally {
      saving = false
    }
  }

  /* ------------------------------------------------- signing in to Google ---- */

  let googleOpen = $state(false)
  let googleUrl = $state("")
  let googleId = $state("")
  let googlePasteCommand = $state("")
  let googleCode = $state("")
  let googleError = $state<string | null>(null)
  let googleStartError = $state<string | null>(null)
  let googleStarting = $state(false)
  let googleFinishing = $state(false)

  async function startGoogle() {
    googleStarting = true
    googleStartError = null
    try {
      const result = await api.connectPhotoWallGoogle()
      googleUrl = result.url
      googleId = result.id
      googlePasteCommand = result.paste_command
      googleCode = ""
      googleError = null
      googleOpen = true
    } catch (err) {
      // 503 when rclone is not installed, with the install command in the
      // message. It belongs beside the button, not in a toast.
      googleStartError = err instanceof Error ? err.message : String(err)
    } finally {
      googleStarting = false
    }
  }

  async function finishGoogle() {
    googleFinishing = true
    googleError = null
    try {
      const wasConnected = status?.google_connected ?? false
      status = await api.finishPhotoWallGoogle(googleId, googleCode.trim())
      googleOpen = false
      googleCode = ""
      toast.success(wasConnected ? "Signed in to Google again" : "Signed in to Google — the photo wall is on")
      await load(true)
    } catch (err) {
      // "Google has not sent the code back yet" is the common one and is a
      // retry, not a failure — so the dialog stays open with the message in it.
      googleError = err instanceof Error ? err.message : String(err)
    } finally {
      googleFinishing = false
    }
  }

  async function rebuild() {
    rebuilding = true
    saveError = null
    try {
      status = await api.rebuildPhotoWall()
      toast.success("Re-listing the folder")
      await load(true)
    } catch (err) {
      saveError = err instanceof Error ? err.message : String(err)
    } finally {
      rebuilding = false
    }
  }

  /** A tile can be handed to a real sign-in and reaped while this is on screen. */
  function hideBrokenTile(event: Event) {
    const img = event.currentTarget
    if (img instanceof HTMLImageElement) img.style.visibility = "hidden"
  }

  const count = $derived(
    new Intl.NumberFormat(undefined).format(status?.photo_count ?? 0)
  )
  const listed = $derived(
    new Intl.NumberFormat(undefined).format(status?.listed_so_far ?? 0)
  )
</script>

<div data-density="compact" class="flex max-w-3xl flex-col gap-4">
  <div class="flex items-center gap-3">
    <div class="flex flex-col">
      <h1 class="text-base font-semibold text-fg">Photo wall</h1>
      <p class="text-xs text-fg-muted">
        The two scrolling columns behind the sign-in card. Decoration only — nothing here can stop
        anyone signing in or checking equipment out.
      </p>
    </div>
    <span class="flex-1"></span>
    <Button variant="ghost" size="icon-sm" aria-label="Reload" onclick={() => load()}>
      <RefreshIcon aria-hidden="true" />
    </Button>
  </div>

  {#if loadError}
    <EmptyState title="Couldn't load the photo wall" description={loadError}>
      {#snippet action()}
        <Button variant="secondary" onclick={() => load()}>Try again</Button>
      {/snippet}
    </EmptyState>
  {:else if loading || !status}
    <div class="flex flex-col gap-3" aria-busy="true" aria-label="Loading the photo wall">
      {#each Array(3) as _, index (index)}
        <Skeleton class="h-40 rounded-xl" />
      {/each}
    </div>
  {:else}
    <!-- ------------------------------------------------------- google ---- -->
    <section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
      <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
        <KeyIcon class="size-4 text-fg-muted" aria-hidden="true" />
        Google account
      </h2>

      {#if status.google_connected}
        <p class="flex items-start gap-2 text-xs text-fg-muted">
          <CheckIcon class="mt-0.5 size-4 shrink-0 text-fg" aria-hidden="true" />
          <span>
            Signed in. The wall reads Google Drive through
            <code class="font-mono">{status.remote}</code>, the same connection the Drive backup
            uses; it only ever reads from the photo folder.
          </span>
        </p>
      {:else}
        <p class="text-xs text-fg-muted">
          The wall's photographs come from Google Drive, so it stays off until somebody signs in.
          This is the same Google connection the Drive backup uses — signing in here, or pressing
          Connect under Settings → Google Drive, connects both. Use a Google account that can open
          the photo folder.
        </p>
      {/if}

      {#if !status.rclone_installed}
        <p class="flex items-start gap-2 text-xs text-fg-muted">
          <AlertTriangleIcon class="mt-0.5 size-4 shrink-0 text-status-due-soon" aria-hidden="true" />
          <span>
            <code class="font-mono">rclone</code> is not installed on this machine, and it is what
            talks to Google Drive. Install it (<code class="font-mono">brew install rclone</code> on
            macOS, <code class="font-mono">winget install Rclone.Rclone</code> on Windows), then
            reload this page.
          </span>
        </p>
      {/if}

      {#if googleStartError}
        <p class="text-sm text-status-overdue" role="alert">{googleStartError}</p>
      {/if}

      <div class="flex flex-wrap items-center gap-2">
        {#if status.google_connected}
          <Button
            variant="secondary"
            disabled={googleStarting || !status.can_connect_google}
            onclick={startGoogle}
          >
            {googleStarting ? "Starting…" : "Sign in again"}
          </Button>
          <p class="text-xs text-fg-faint">
            If the wall says the Google sign-in has expired, this is the fix.
          </p>
        {:else}
          <Button disabled={googleStarting || !status.can_connect_google} onclick={startGoogle}>
            {googleStarting ? "Starting…" : "Sign in with Google"}
          </Button>
          <span
            class="rounded-(--radius-sm) border border-line px-1.5 text-xs font-normal text-fg-muted"
          >
            Required
          </span>
        {/if}
      </div>
    </section>

    <!-- ------------------------------------------------------- status ---- -->
    <section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
      <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
        <ImagesIcon class="size-4 text-fg-muted" aria-hidden="true" />
        Status
      </h2>

      {#if !status.enabled}
        <!-- The common case on a fresh install, and not an error: the sign-in
             above is the switch. -->
        <p class="text-xs text-fg-muted">
          {#if status.google_connected}
            Signed in to Google, but the wall is not running on this server. The server log says
            why it could not start.
          {:else}
            Off until somebody signs in to Google above. Sign-in looks exactly as it does now until
            then.
          {/if}
        </p>
      {:else}
        <dl class="grid gap-x-6 gap-y-2 text-xs sm:grid-cols-2">
          <div>
            <dt class="text-fg-faint">Folder</dt>
            <dd class="text-fg">
              {#if status.folder_set}
                {status.folder_label || "(unnamed)"}
              {:else}
                None chosen yet — the wall stays empty
              {/if}
            </dd>
          </div>
          <div>
            <dt class="text-fg-faint">Last changed</dt>
            <dd class="text-fg">
              {#if status.changed_at}
                {dateTime(status.changed_at)}{status.changed_by ? ` by ${status.changed_by}` : ""}
              {:else}
                —
              {/if}
            </dd>
          </div>
          <div>
            <dt class="text-fg-faint">Photographs found</dt>
            <dd class="text-fg">
              {#if status.listing}
                Rebuilding — {listed} files listed so far
              {:else if status.built_at}
                {count}, listed {dateTime(status.built_at)}
              {:else}
                Not listed yet
              {/if}
            </dd>
          </div>
          <div>
            <dt class="text-fg-faint">Tiles ready</dt>
            <dd class="text-fg">
              {status.ready} waiting, {status.served} showing
            </dd>
          </div>
        </dl>

        {#if status.last_error}
          <!-- Hue on the icon, never as a fill: the five status colours belong
               to asset state, and an amber panel here reads as an item that is
               due soon (CLAUDE.md §13). -->
          <p class="flex items-start gap-2 text-xs text-fg-muted" role="status">
            <AlertTriangleIcon
              class="mt-0.5 size-4 shrink-0 text-status-due-soon"
              aria-hidden="true"
            />
            <span>
              {status.last_error}
              {#if status.last_error_at}
                <span class="text-fg-faint"> ({dateTime(status.last_error_at)})</span>
              {/if}
            </span>
          </p>
        {/if}

        {#if status.folder_set}
          <div>
            <Button variant="secondary" disabled={rebuilding || status.listing} onclick={rebuild}>
              {status.listing ? "Listing…" : rebuilding ? "Starting…" : "Re-list this folder"}
            </Button>
            <p class="mt-1 text-xs text-fg-faint">
              The folder is re-listed automatically about once a week. Press this after uploading a
              batch of photographs you want on the wall today.
            </p>
          </div>
        {/if}
      {/if}
    </section>

    <!-- ------------------------------------------------------ preview ---- -->
    {#if status.enabled}
      <section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
        <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
          <FolderIcon class="size-4 text-fg-muted" aria-hidden="true" />
          What the wall is showing
        </h2>
        <p class="text-xs text-fg-muted">
          Six of the tiles waiting to go out, at the size and crop the sign-in screen uses. This is
          the check that matters: everything in the live folder will eventually appear on the
          sign-in screen, which is the one screen anybody can see without signing in.
        </p>

        {#if preview.length > 0}
          <!-- Looking at these does not consume them: a preview that emptied
               the buffer would make this screen's own confirmation the thing
               that breaks the wall. -->
          <ul class="grid grid-cols-3 gap-2">
            {#each preview as src (src)}
              <li>
                <img
                  class="aspect-3/2 w-full rounded-(--radius-sm) bg-raised object-cover"
                  src={fileUrl(src)}
                  alt=""
                  onerror={hideBrokenTile}
                />
              </li>
            {/each}
          </ul>
        {:else}
          <p class="text-xs text-fg-faint">
            {#if !status.folder_set}
              Nothing yet — choose a folder below.
            {:else if status.listing}
              Nothing yet — the folder is still being listed.
            {:else}
              Nothing yet. The reel fills a photograph at a time over a few minutes, deliberately
              slowly, so it never earns a rate limit on the same Google account the nightly backup
              uses.
            {/if}
          </p>
        {/if}
      </section>
    {/if}

    <!-- ------------------------------------------------------- change ---- -->
    <section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
      <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
        <LinkIcon class="size-4 text-fg-muted" aria-hidden="true" />
        {status.folder_set ? "Replace the folder" : "Choose a folder"}
      </h2>
      {#if !status.can_set_folder}
        <!--
          The form is shown and disabled rather than hidden. Hidden, an admin
          who has been told "choose a folder in the admin panel" hunts for a
          box that is not there and concludes the screen is broken. Left
          enabled, every press is a 503 they had no way to see coming — which
          is exactly what happened the first time somebody used this screen.

          The reason is one line pointing upward, not a second copy of it:
          Status already says which piece is missing and what to do, and
          saying it twice in two wordings reads as two problems (CLAUDE.md
          §13).
        -->
        <p class="text-xs text-fg-muted">
          {#if !status.google_connected}
            Sign in with Google above first. A link pasted here is checked against Drive before it
            is saved, and that check needs the sign-in.
          {:else}
            Not available yet — this server has no Drive source to check a folder against, so a
            link pasted here could not be verified and would not be saved.
            <strong>Status</strong> above says which piece is missing.
          {/if}
        </p>
      {:else}
        <p class="text-xs text-fg-muted">
          Open the folder in Google Drive, press <strong>Share</strong> → <strong>Copy link</strong>,
          and paste it here. The Google account this machine is signed in to has to be able to open
          it, so either share the folder with that account or set the folder to anyone-with-the-link.
        </p>
      {/if}

      <div class="flex flex-col gap-1">
        <Label for="wall-label">Name for this folder</Label>
        <Input
          id="wall-label"
          bind:value={label}
          placeholder="Fall 2026 game photos"
          autocomplete="off"
          maxlength={120}
          disabled={!status.can_set_folder}
        />
        <p class="text-xs text-fg-faint">
          Shown on this screen and in the log in place of the link. Anything you would recognise.
        </p>
      </div>

      <div class="flex flex-col gap-1">
        <Label for="wall-link">Drive folder link</Label>
        <Input
          id="wall-link"
          bind:value={link}
          placeholder="https://drive.google.com/drive/folders/…"
          spellcheck={false}
          autocomplete="off"
          disabled={!status.can_set_folder}
        />
        <p class="text-xs text-fg-faint">
          Never shown back. A folder link is a key to the folder, so this box starts empty every
          time — including right after you have set one. To find out which folder is live, look at
          the name above and the photographs in the strip.
        </p>
      </div>

      {#if saveError}
        <p class="text-sm text-status-overdue" role="alert">{saveError}</p>
      {/if}

      <div class="flex items-center gap-3">
        <Button
          disabled={saving || !status.can_set_folder || !link.trim() || !label.trim()}
          onclick={replaceFolder}
        >
          {saving ? "Checking the folder…" : status.folder_set ? "Replace folder" : "Use this folder"}
        </Button>
        {#if status.can_set_folder}
          <p class="text-xs text-fg-faint">
            Checked against Drive before anything is saved. If it cannot be opened, nothing changes
            and the folder in use now stays live.
          </p>
        {/if}
      </div>
    </section>
  {/if}
</div>

<GoogleConnectDialog
  bind:open={googleOpen}
  bind:code={googleCode}
  url={googleUrl}
  pasteCommand={googlePasteCommand}
  title="Sign in with Google"
  description="Open the link, sign in to a Google account that can open the photo folder, and allow access. Google will say it hasn't verified this app — press Advanced, then continue. The Drive backup uses this same connection."
  busy={googleFinishing}
  error={googleError}
  onfinish={finishGoogle}
/>
