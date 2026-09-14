<script lang="ts">
  /**
   * The sign-in screen (design-system.md §8.1).
   *
   * Full-bleed, `data-density="kiosk"`, no sidebar and no top bar. One large
   * input that is **always focused and refocuses on blur**: a keyboard-wedge
   * scanner types into whatever has focus, so losing focus breaks scanning
   * entirely (CLAUDE.md §10).
   *
   * This screen owns its own keystroke listener, scoped to that input, rather
   * than taking bursts from the root `<ScanListener>`. The root listener is the
   * item-scan path and is inactive while nobody is signed in — which is what §9.1
   * is actually protecting against, two listeners firing for one burst. Scoping
   * it here is also the only way the password step works: with a global listener
   * running, pressing Enter after typing a password would hand the password to
   * `/auth/scan` as a student number.
   *
   * Errors render inline beneath the input, never as a toast: a toast can vanish
   * before someone reads it.
   */
  import ScanLineIcon from "@lucide/svelte/icons/scan-line"
  import { Button } from "@stockroom/ui/components/ui/button"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import * as api from "../api/index"
  import { attachScanner, looksLikeStudentNumber } from "../scanner"
  import { session } from "../stores/session.svelte"

  let { onSignedIn }: { onSignedIn: () => void } = $props()

  type Step = "number" | "password" | "set-password"

  let step = $state<Step>("number")
  let numberInput = $state<HTMLInputElement | null>(null)
  let passwordInput = $state<HTMLInputElement | null>(null)

  let studentNumber = $state("")
  let password = $state("")
  let newPassword = $state("")
  let confirmPassword = $state("")

  let error = $state<string | null>(null)
  let busy = $state(false)

  /**
   * Keep focus on whichever field is live. A scanner can fire at any moment and
   * there is nobody at the machine to click first.
   */
  function refocus() {
    if (step === "number") numberInput?.focus()
    else passwordInput?.focus()
  }

  $effect(() => {
    void step
    // A microtask is enough: the field exists by the time the browser paints.
    queueMicrotask(refocus)
  })

  // The scoped listener. Measures the same threshold as every other scan in the
  // app because it is the same function (lib/scanner.ts).
  $effect(() => {
    const target = numberInput
    if (!target || step !== "number") return
    return attachScanner({
      target,
      captureInsideFields: true,
      onBurst: ({ code, fast }) => handleBurst(code, fast),
    })
  })

  async function handleBurst(code: string, fast: boolean) {
    const trimmed = code.trim()
    studentNumber = trimmed
    error = null

    if (!trimmed) return

    if (!looksLikeStudentNumber(trimmed)) {
      // An item barcode scanned with nobody signed in. Say so explicitly instead
      // of failing as a bad login: the code is valid, it's just not a student
      // number (§15 Q4).
      error = fast
        ? "That looks like an item barcode. Scan your student ID to sign in first."
        : "Student numbers are digits only."
      studentNumber = ""
      return
    }

    if (fast) {
      // A scan signs in with no password (CLAUDE.md §7).
      await submitScan(trimmed)
    } else {
      // Typed by hand: the password is required.
      step = "password"
    }
  }

  async function submitScan(number: string) {
    busy = true
    try {
      const result = await api.loginByScan(number)
      await session.adopt(result)
      if (result.needs_password) step = "set-password"
      else onSignedIn()
    } catch (err) {
      error = messageFor(err)
      studentNumber = ""
    } finally {
      busy = false
    }
  }

  async function submitPassword(event: SubmitEvent) {
    event.preventDefault()
    busy = true
    error = null
    try {
      const result = await api.loginByPassword(studentNumber, password)
      await session.adopt(result)
      password = ""
      if (result.needs_password) step = "set-password"
      else onSignedIn()
    } catch (err) {
      error = messageFor(err)
      password = ""
    } finally {
      busy = false
    }
  }

  async function submitNewPassword(event: SubmitEvent) {
    event.preventDefault()
    error = null
    if (newPassword !== confirmPassword) {
      error = "The two passwords don't match."
      return
    }
    busy = true
    try {
      await api.setInitialPassword(newPassword)
      await session.completePasswordSetup()
      newPassword = ""
      confirmPassword = ""
      onSignedIn()
    } catch (err) {
      error = messageFor(err)
    } finally {
      busy = false
    }
  }

  function messageFor(err: unknown): string {
    if (err instanceof api.ApiError) {
      // 401 from either login route is "we don't know that number, or the
      // password is wrong" — the server deliberately doesn't distinguish them.
      if (err.status === 401) return err.message
      return err.message
    }
    return err instanceof Error ? err.message : String(err)
  }

  function restart() {
    step = "number"
    studentNumber = ""
    password = ""
    error = null
  }
</script>

<main
  data-density="kiosk"
  class="flex min-h-full flex-col items-center justify-center bg-ground p-(--gutter)"
>
  <div class="w-full max-w-[420px]">
    <!-- Wordmark. Left-aligned like the rest of the app; the input below it is
         what the eye needs to land on. -->
    <p class="text-2xl font-semibold tracking-tight text-fg">Stockroom</p>
    <p class="mt-1 text-fg-muted">Media department equipment checkout</p>

    {#if step === "number"}
      <form
        class="mt-8 flex flex-col gap-2"
        onsubmit={(event) => {
          // The scoped scanner listener owns Enter here; a native submit would
          // handle the same keystroke a second time.
          event.preventDefault()
        }}
      >
        <Label for="student-number" class="flex items-center gap-2 text-fg">
          <ScanLineIcon class="size-4 text-fg-muted" aria-hidden="true" />
          Scan your student ID.
        </Label>
        <Input
          id="student-number"
          bind:ref={numberInput}
          bind:value={studentNumber}
          inputmode="numeric"
          autocomplete="off"
          spellcheck={false}
          disabled={busy}
          placeholder="or type your student number and press Enter"
          class="h-(--control-h) text-lg"
          onblur={() => queueMicrotask(refocus)}
        />
        <p class="text-fg-faint">
          A scanned ID signs you in straight away. Typing the number asks for your password.
        </p>
      </form>
    {:else if step === "password"}
      <form class="mt-8 flex flex-col gap-2" onsubmit={submitPassword}>
        <Label for="password" class="text-fg">
          Password for <span class="font-mono">{studentNumber}</span>
        </Label>
        <Input
          id="password"
          type="password"
          bind:ref={passwordInput}
          bind:value={password}
          autocomplete="current-password"
          disabled={busy}
          class="h-(--control-h) text-lg"
        />
        <div class="mt-2 flex items-center gap-2">
          <Button type="submit" size="tap" disabled={busy || !password}>
            {busy ? "Signing in…" : "Sign in"}
          </Button>
          <Button type="button" size="tap" variant="ghost" onclick={restart} disabled={busy}>
            Back
          </Button>
        </div>
      </form>
    {:else}
      <!-- The set-password step, with no way to skip: a roster-imported account
           has no password, and typed login is impossible until it sets one
           (CLAUDE.md §7). -->
      <form class="mt-8 flex flex-col gap-2" onsubmit={submitNewPassword}>
        <p class="text-fg">Set a password before you continue.</p>
        <p class="text-fg-faint">
          You'll need it whenever you type your number instead of scanning your ID. At least 8
          characters.
        </p>

        <Label for="new-password" class="mt-2 text-fg">New password</Label>
        <Input
          id="new-password"
          type="password"
          bind:ref={passwordInput}
          bind:value={newPassword}
          autocomplete="new-password"
          minlength={8}
          maxlength={72}
          disabled={busy}
          class="h-(--control-h) text-lg"
        />

        <Label for="confirm-password" class="mt-2 text-fg">Confirm password</Label>
        <Input
          id="confirm-password"
          type="password"
          bind:value={confirmPassword}
          autocomplete="new-password"
          minlength={8}
          maxlength={72}
          disabled={busy}
          class="h-(--control-h) text-lg"
        />

        <Button type="submit" size="tap" class="mt-3" disabled={busy || newPassword.length < 8}>
          {busy ? "Saving…" : "Set password and continue"}
        </Button>
      </form>
    {/if}

    {#if error}
      <p class="mt-3 text-status-overdue" role="alert">{error}</p>
    {/if}
  </div>
</main>
