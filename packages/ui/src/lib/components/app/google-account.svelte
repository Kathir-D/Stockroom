<script lang="ts">
  /**
   * The one Google connection, as an admin sees it: who is signed in, and a
   * Sign in with Google button. Settings and Photo wall both render this, and
   * it is the same connection on both (google_admin.go).
   *
   * **One click.** The button opens Google's page itself (open-external.ts)
   * and then asks the server every couple of seconds whether Google has called
   * back. There is nothing to copy and nothing to press afterwards; the
   * dialog closes on its own. What rclone is doing underneath — a callback
   * port, a remote, a token in its config file — is never on screen.
   *
   * The paste box survives only behind "Signing in on a different computer?",
   * for a machine whose browser cannot reach Google's page.
   */
  import type { Snippet } from "svelte"
  import CircleCheckIcon from "@lucide/svelte/icons/circle-check"
  import LoaderIcon from "@lucide/svelte/icons/loader-circle"
  import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert"
  import { toast } from "svelte-sonner"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Skeleton } from "@stockroom/ui/components/ui/skeleton"
  import * as api from "../../api/index"
  import type { GoogleStatus } from "../../api/types"
  import { beginExternal } from "../../open-external"

  let {
    status = $bindable(null),
    purpose,
    onchange,
    children,
  }: {
    status?: GoogleStatus | null
    /** One line on what the connection is for, under the heading. */
    purpose: string
    /** After a sign-in, with the new status. */
    onchange?: (status: GoogleStatus) => void
    /** Anything else the caller puts in the card (Settings: the own-client form). */
    children?: Snippet
  } = $props()

  let loadError = $state<string | null>(null)

  async function load() {
    loadError = null
    try {
      status = await api.googleStatus()
    } catch (err) {
      loadError = err instanceof Error ? err.message : String(err)
    }
  }

  $effect(() => {
    load()
  })

  /* ------------------------------------------------------------ sign in ---- */

  let open = $state(false)
  let starting = $state(false)
  let startError = $state<string | null>(null)
  let error = $state<string | null>(null)
  let url = $state("")
  let pasteCommand = $state("")
  let code = $state("")
  let pasting = $state(false)
  /** False when no window could be opened, so the dialog offers the link. */
  let opened = $state(true)
  /** Which attempt is current; a poll for an older one stops itself. */
  let attempt = 0
  /** The current attempt's id, for the paste path. */
  let currentId = ""

  async function signIn() {
    // Before any await: a browser only lets a click open a window.
    const pending = beginExternal()
    starting = true
    startError = null
    error = null
    code = ""
    const mine = ++attempt
    try {
      const res = await api.connectGoogle()
      url = res.url
      currentId = res.id
      pasteCommand = res.paste_command
      opened = pending.go(res.url)
      open = true
      poll(res.id, mine)
    } catch (err) {
      pending.cancel()
      // 503 when rclone is missing, with the install command in the message.
      startError = err instanceof Error ? err.message : String(err)
    } finally {
      starting = false
    }
  }

  async function poll(id: string, mine: number) {
    while (open && mine === attempt) {
      try {
        const res = await api.finishGoogle(id)
        if (mine !== attempt) return
        if (res.done && res.google) {
          done(res.google)
          return
        }
      } catch (err) {
        if (mine !== attempt) return
        error = err instanceof Error ? err.message : String(err)
        return
      }
      await new Promise((resolve) => setTimeout(resolve, 1500))
    }
  }

  async function finishWithCode() {
    pasting = true
    error = null
    try {
      // The pasted block carries the token itself; the id only stops the
      // attempt still waiting on this machine.
      const res = await api.finishGoogle(currentId, code.trim())
      if (res.done && res.google) done(res.google)
      else error = "That block didn't finish the sign-in. Paste everything between the two arrows."
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      pasting = false
    }
  }

  function done(next: GoogleStatus) {
    attempt++
    open = false
    status = next
    toast.success(next.account ? `Signed in as ${next.account}` : "Signed in to Google")
    onchange?.(next)
  }

  function cancel() {
    attempt++
    open = false
  }

  // Leaving the screen mid-sign-in stops the polling; without this the loop
  // would keep asking the server every 1.5 s until rclone gave up.
  $effect(() => () => {
    attempt++
  })
</script>

<section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
  <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
    <svg class="size-4" viewBox="0 0 24 24" aria-hidden="true">
      <path fill="#4285F4" d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 0 1-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z" />
      <path fill="#34A853" d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84A11 11 0 0 0 12 23z" />
      <path fill="#FBBC05" d="M5.84 14.1A6.6 6.6 0 0 1 5.5 12c0-.73.13-1.44.34-2.1V7.06H2.18A11 11 0 0 0 1 12c0 1.77.43 3.45 1.18 4.94l3.66-2.84z" />
      <path fill="#EA4335" d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15A10.96 10.96 0 0 0 12 1 11 11 0 0 0 2.18 7.06l3.66 2.84C6.71 7.3 9.14 5.38 12 5.38z" />
    </svg>
    Google account
  </h2>
  <p class="text-xs text-fg-muted">{purpose}</p>

  {#if loadError}
    <p class="text-sm text-status-overdue" role="alert">{loadError}</p>
  {:else if !status}
    <Skeleton class="h-9 w-64" />
  {:else if !status.rclone_installed}
    <p class="flex items-start gap-2 text-xs text-fg-muted">
      <AlertTriangleIcon class="mt-0.5 size-4 shrink-0 text-status-due-soon" aria-hidden="true" />
      <span>
        One program is missing on this machine: <code class="font-mono">rclone</code>, which is what
        talks to Google. Install it once (<code class="font-mono">brew install rclone</code> on a
        Mac, <code class="font-mono">winget install Rclone.Rclone</code> on Windows), then
        <button type="button" class="underline underline-offset-2 hover:text-fg" onclick={load}>
          check again</button
        >.
      </span>
    </p>
  {:else if status.connected}
    <div class="flex flex-wrap items-center gap-3">
      <p class="flex items-center gap-2 text-sm text-fg">
        <CircleCheckIcon class="size-4 text-status-available" aria-hidden="true" />
        {#if status.account}
          Signed in as <strong class="font-medium">{status.account}</strong>
        {:else}
          Signed in to Google
        {/if}
      </p>
      <span class="flex-1"></span>
      <Button variant="ghost" size="sm" disabled={starting} onclick={signIn}>
        {starting ? "Opening Google…" : "Use a different account"}
      </Button>
    </div>
  {:else}
    <div>
      <Button disabled={starting} onclick={signIn}>
        {starting ? "Opening Google…" : "Sign in with Google"}
      </Button>
    </div>
  {/if}

  {#if startError}
    <p class="text-sm text-status-overdue" role="alert">{startError}</p>
  {/if}

  {@render children?.()}
</section>

<Dialog.Root
  bind:open
  onOpenChange={(next) => {
    if (!next) cancel()
  }}
>
  <Dialog.Content>
    <Dialog.Header>
      <Dialog.Title>Sign in with Google</Dialog.Title>
      <Dialog.Description>
        {#if opened}
          Google's sign-in page has opened in your browser. Pick the account, then press
          <strong>Continue</strong> or <strong>Allow</strong>. This window closes by itself when
          you're done.
        {:else}
          Open Google's sign-in page, pick the account, then press <strong>Continue</strong> or
          <strong>Allow</strong>. This window closes by itself when you're done.
        {/if}
      </Dialog.Description>
    </Dialog.Header>

    <div class="flex flex-col gap-3">
      {#if error}
        <p class="text-sm text-status-overdue" role="alert">{error}</p>
      {:else}
        <p class="flex items-center gap-2 text-sm text-fg-muted" role="status">
          <LoaderIcon class="size-4 animate-spin" aria-hidden="true" />
          Waiting for Google…
        </p>
      {/if}

      <div>
        <Button
          variant={opened ? "ghost" : "primary"}
          size={opened ? "sm" : "default"}
          href={url}
          target="_blank"
          rel="noreferrer"
        >
          {opened ? "Page didn't open? Open it again" : "Open Google sign-in"}
        </Button>
      </div>

      <p class="text-xs text-fg-faint">
        If Google says it <em>hasn't verified this app</em>, that's expected — the app is this
        machine's own. Press <strong>Advanced</strong>, then <strong>Go to … (unsafe)</strong>.
      </p>

      <details class="text-xs text-fg-muted">
        <summary class="cursor-pointer select-none hover:text-fg">
          Signing in on a different computer?
        </summary>
        <div class="mt-2 flex flex-col gap-2">
          <p>
            On a computer with <code class="font-mono">rclone</code>, run
            <code class="font-mono break-all">{pasteCommand}</code>, sign in, and paste what it prints
            between the arrows here.
          </p>
          <div class="flex gap-2">
            <Input
              bind:value={code}
              placeholder="Paste the block here"
              spellcheck={false}
              autocomplete="off"
              class="font-mono text-xs"
            />
            <Button
              variant="secondary"
              disabled={pasting || !code.trim()}
              onclick={finishWithCode}
            >
              {pasting ? "Checking…" : "Use this"}
            </Button>
          </div>
        </div>
      </details>
    </div>

    <Dialog.Footer>
      {#if error}
        <Button variant="secondary" onclick={signIn} disabled={starting}>Try again</Button>
      {/if}
      <Button variant="ghost" onclick={cancel}>Cancel</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
