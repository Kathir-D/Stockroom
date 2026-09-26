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
  import PasswordInput from "../components/app/password-input.svelte"
  import PhotoWall from "../components/app/photo-wall.svelte"
  import FirstAdmin from "../components/app/first-admin.svelte"
  import * as api from "../api/index"
  import { attachScanner } from "../scanner"
  import {
    DIGITS_RULE,
    MAX_STUDENT_NUMBER_LENGTH,
    filterStudentNumber,
    formatHint,
    looksLikeStudentNumber,
    ruleFrom,
    type StudentNumberRule,
  } from "../student-number"
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
   * The install's student-number rule, served by `GET /signin/config`. Digits
   * until it arrives -- the historical rule, and what an install that never
   * changed the setting uses -- and digits if it never does: a server that
   * cannot answer this cannot sign anybody in either.
   */
  let rule = $state<StudentNumberRule>(DIGITS_RULE)
  /**
   * No accounts exist yet: this screen becomes "create the first admin"
   * (the setup wizard's step 2). The same request as the rule, so a fresh
   * install costs sign-in nothing extra.
   */
  let needsSetup = $state(false)
  $effect(() => {
    api.signInConfig().then(
      (raw) => {
        rule = ruleFrom(raw)
        needsSetup = (raw as { needs_setup?: unknown } | null)?.needs_setup === true
      },
      () => {},
    )
  })

  /**
   * The field's filter, as a function, so the burst handler can apply the same
   * one when it asks whether the buffer and the field still agree. It MUST be
   * the same filter in both places (lib/student-number.ts says why).
   */
  function fieldFilter(value: string): string {
    return filterStudentNumber(value, rule)
  }

  /**
   * Keep the student-number field to the characters the rule allows (digits,
   * unless the install says otherwise).
   *
   * `NormalizeStudentNumber` refuses anything else server-side, so letters were
   * never going to sign anyone in — but the field accepted them, and the person
   * only found out after pressing Enter. Filtering here means the rule is
   * visible while they type rather than reported afterwards.
   *
   * Applied on `input` rather than by swallowing keystrokes, so it catches every
   * way text arrives: typing, pasting, autofill, a mobile keyboard's
   * autocomplete. The caret is put back where it belongs, or a stripped
   * character mid-number would throw the cursor to the end.
   *
   * The scanner is unaffected — it reads keystrokes directly (`attachScanner`),
   * not this field — which matters, because an *item* barcode scanned here has
   * to produce "that looks like an item barcode", not a silently stripped
   * number that fails as a bad login (§15 Q4).
   */
  function keepToRule(event: Event & { currentTarget: HTMLInputElement }) {
    const el = event.currentTarget
    const digits = fieldFilter(el.value)
    if (digits !== el.value) {
      const caret = el.selectionStart ?? el.value.length
      const removedBeforeCaret = caret - fieldFilter(el.value.slice(0, caret)).length
      el.value = digits
      const next = Math.max(0, caret - removedBeforeCaret)
      el.setSelectionRange(next, next)
    }
    studentNumber = digits
  }

  /**
   * Keep focus on whichever field is live. A scanner can fire at any moment and
   * there is nobody at the machine to click first.
   *
   * It only reclaims focus that fell *nowhere*. Focus that moved to another
   * real control is left alone, and that is not a nicety — it is what stops the
   * screen locking up the machine.
   *
   * A modal dialog traps focus inside itself. Rendered over this screen, the
   * unconditional version fought it: the dialog pulled focus in, this handler
   * pulled it back out, the dialog pulled it in again, forever, at whatever
   * rate the event loop could manage. Measured: the tab stopped responding
   * entirely and had to be closed. The restore report is the first overlay to
   * appear above sign-in (a restore signs everybody out), but the bug was
   * always here waiting for one.
   *
   * Two guards, because they fail differently. `relatedTarget` names where
   * focus went and is the precise answer, but it is null in a few real cases —
   * so an open dialog anywhere on the page is also enough to stand down.
   *
   * `:not([data-state='closed'])` on that second guard, not a bare role match.
   * A dialog stays in the DOM through its close animation with
   * `data-state="closed"` on it (observed: roughly 150ms after the palette is
   * dismissed), and a guard that counted those would eventually meet one that
   * lingers — leaving the field unfocusable and the scanner, which types into
   * whatever has focus, dead with no symptom but "scanning stopped working".
   * The negated form also treats a dialog carrying no `data-state` as open,
   * which is the right way round: standing down for a beat too long costs a
   * refocus, mistaking an open dialog for a closed one costs the lock-up.
   */
  function refocus(event?: FocusEvent) {
    if (event?.relatedTarget instanceof HTMLElement) return
    if (
      typeof document !== "undefined" &&
      document.querySelector("[role='dialog']:not([data-state='closed'])")
    )
      return
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
      /**
       * A scan is the only thing in this app that signs somebody in without a
       * password, so it has to clear the higher bar: the keystrokes arrived at
       * scanner speed **and** the field ended up holding exactly what the buffer
       * saw. A keyboard-wedge scanner types straight into the focused input, so
       * for a real scan those two always agree.
       *
       * They disagree when a person edited in a way a keystroke buffer cannot
       * model — pasting over a selection, dragging text in, an autofill, or any
       * editing chord the scanner had to discard. The box is what the person can
       * actually see, so the box wins and the burst is treated as typed.
       *
       * The comparison runs through the field's own digit filter. Without it an
       * *item* barcode scanned at this screen could never match — its letters
       * and dashes are stripped on the way into the box — and the "that looks
       * like an item barcode" message (§15 Q4) would be lost with it.
       */
      onBurst: ({ code, fast }) => {
        const inSync = target.value === fieldFilter(code)
        handleBurst(inSync ? code : target.value, fast && inSync)
      },
    })
  })

  /**
   * When the scanner last reported a burst.
   *
   * The scanner ignores an Enter with an empty keystroke buffer, which is the
   * right call for a barcode but leaves the form dead for anything that fills
   * the field without keystrokes — a paste, autofill, or a mobile keyboard's
   * autocomplete. CLAUDE.md §10 wants manual entry to work identically as a
   * fallback, so the native submit below picks those up. This timestamp is how
   * it tells "the scanner already handled this Enter" from "nothing did".
   */
  let burstHandledAt = 0

  async function handleBurst(code: string, fast: boolean) {
    burstHandledAt = performance.now()
    const trimmed = code.trim()
    studentNumber = trimmed
    error = null

    if (!trimmed) return

    if (!looksLikeStudentNumber(trimmed, rule)) {
      // An item barcode scanned with nobody signed in. Say so explicitly instead
      // of failing as a bad login: the code is valid, it's just not a student
      // number (§15 Q4).
      error = fast
        ? "That looks like an item barcode. Scan your student ID to sign in first."
        : formatHint(rule)
      // Every scan is in the activity log, this one included: an item
      // scanned with nobody signed in is exactly what someone tracing a
      // missing item wants to see (ROADMAP §2.4). A typed mistake is not a
      // scan and is not logged.
      if (fast) void api.logUnattendedScan(trimmed, "item_at_signin")
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
  class="relative flex min-h-full flex-col items-center justify-center bg-ground p-(--gutter)"
>
  <!-- Behind the card, and positioned against this <main> rather than in the
       flow, so the input sits in exactly the same place whether the wall is
       there or not. `relative` above is the only change it asks of this screen;
       it moves nothing. -->
  <PhotoWall />

  <div class="relative w-full max-w-[420px]">
    <!-- Wordmark. Left-aligned like the rest of the app; the input below it is
         what the eye needs to land on. -->
    <p class="text-2xl font-semibold tracking-tight text-fg">Stockroom</p>
    <p class="mt-1 text-fg-muted">Equipment checkout</p>

    {#if needsSetup}
      <FirstAdmin
        onCreated={async (result) => {
          await session.adopt(result)
          onSignedIn()
        }}
      />
    {:else if step === "number"}
      <form
        class="mt-8 flex flex-col gap-2"
        onsubmit={(event) => {
          event.preventDefault()
          // The scanner owns Enter when it saw the keystrokes; handling it here
          // too would run the same burst twice. It saw nothing when the field
          // was filled without typing, and that is the case this catches —
          // never as a scan, because a paste has no rhythm to measure.
          if (performance.now() - burstHandledAt < 50) return
          if (!busy && studentNumber.trim()) handleBurst(studentNumber, false)
        }}
      >
        <Label for="student-number" class="flex items-center gap-2 text-fg">
          <ScanLineIcon class="size-4 text-fg-muted" aria-hidden="true" />
          Scan your student ID.
        </Label>
        <!-- `value` + `oninput` rather than `bind:value`: the handler rewrites
             what was typed, and a two-way binding would race its own update. -->
        <Input
          id="student-number"
          bind:ref={numberInput}
          value={studentNumber}
          oninput={keepToRule}
          inputmode={rule.format === "digits" ? "numeric" : "text"}
          pattern={rule.format === "digits" ? "[0-9]*" : undefined}
          maxlength={MAX_STUDENT_NUMBER_LENGTH}
          autocomplete="off"
          spellcheck={false}
          disabled={busy}
          placeholder="or type your student number and press Enter"
          class="h-(--control-h) text-lg"
          onblur={(event) => queueMicrotask(() => refocus(event))}
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
        <PasswordInput
          id="password"
          bind:ref={passwordInput}
          bind:value={password}
          autocomplete="current-password"
          disabled={busy}
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
        <PasswordInput
          id="new-password"
          bind:ref={passwordInput}
          bind:value={newPassword}
          autocomplete="new-password"
          minlength={8}
          maxlength={72}
          disabled={busy}
        />

        <Label for="confirm-password" class="mt-2 text-fg">Confirm password</Label>
        <PasswordInput
          id="confirm-password"
          bind:value={confirmPassword}
          autocomplete="new-password"
          minlength={8}
          maxlength={72}
          disabled={busy}
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
