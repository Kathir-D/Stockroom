<script lang="ts">
  /**
   * Step 2 of the setup wizard, shown in place of the sign-in form while the
   * install has no accounts at all (internal/stockroom/setup.go).
   *
   * The ID format is chosen here, first, because the admin's own number has
   * to fit it -- and there is no session yet to save it with afterwards, so it
   * travels with the account in one request.
   *
   * Copy from the plan's "words themselves" section, kept close: the
   * explanation of why a scannable account also has a password is the
   * question every teacher asks.
   */
  import { Button } from "@stockroom/ui/components/ui/button"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import PasswordInput from "./password-input.svelte"
  import StudentNumberFormat from "./student-number-format.svelte"
  import * as api from "../../api/index"
  import type { LoginResult } from "../../api/types"
  import type { StudentNumberFormat as Format } from "../../student-number"

  let { onCreated }: { onCreated: (result: LoginResult) => void } = $props()

  let format = $state<Format>("digits")
  let pattern = $state("")
  let number = $state("")
  let first = $state("")
  let last = $state("")
  let password = $state("")
  let confirm = $state("")
  let busy = $state(false)
  let error = $state<string | null>(null)
  let whyOpen = $state(false)

  async function submit(event: SubmitEvent) {
    event.preventDefault()
    error = null
    if (password !== confirm) {
      error = "The two passwords don't match."
      return
    }
    busy = true
    try {
      const result = await api.createFirstAdmin({
        student_number: number.trim(),
        first_name: first.trim(),
        last_name: last.trim(),
        password,
        student_number_format: format,
        student_number_pattern: pattern,
      })
      onCreated(result)
    } catch (err) {
      error = err instanceof Error ? err.message.replace(/^invalid input: /, "") : String(err)
    } finally {
      busy = false
    }
  }
</script>

<form class="mt-8 flex flex-col gap-3" onsubmit={submit}>
  <div>
    <h1 class="text-lg font-semibold text-fg">Welcome. Let's set Stockroom up.</h1>
    <p class="mt-1 text-sm text-fg-muted">
      First, your account. You'll use it to add equipment, manage people, and see who has what.
      The rest takes about twenty minutes, and you can stop at any point and pick up where you left
      off.
    </p>
  </div>

  <div class="flex flex-col gap-1.5">
    <Label>What do ID numbers look like at your school?</Label>
    <StudentNumberFormat idPrefix="first-admin" bind:format bind:pattern />
  </div>

  <div class="flex flex-col gap-1.5">
    <Label for="first-admin-number">Your ID number</Label>
    <Input id="first-admin-number" bind:value={number} required autocomplete="off" spellcheck={false} />
    <p class="text-xs text-fg-faint">
      The number on your school ID card. If your card has a barcode, scan it into this box.
    </p>
  </div>

  <div class="grid grid-cols-2 gap-3">
    <div class="flex flex-col gap-1.5">
      <Label for="first-admin-first">First name</Label>
      <Input id="first-admin-first" bind:value={first} required autocomplete="given-name" />
    </div>
    <div class="flex flex-col gap-1.5">
      <Label for="first-admin-last">Last name</Label>
      <Input id="first-admin-last" bind:value={last} autocomplete="family-name" />
    </div>
  </div>

  <div class="flex flex-col gap-1.5">
    <Label for="first-admin-password">A password</Label>
    <PasswordInput
      id="first-admin-password"
      bind:value={password}
      autocomplete="new-password"
      minlength={8}
      maxlength={72}
      required
    />
    <p class="text-xs text-fg-faint">
      At least 8 characters. You only need it when you type your number instead of scanning it.
      <button type="button" class="underline" onclick={() => (whyOpen = !whyOpen)}>Why?</button>
    </p>
    {#if whyOpen}
      <p class="text-xs text-fg-muted">
        Scanning is the fast way in at the counter. Typing your number needs a password, so somebody
        who simply knows your number can't sign in as you.
      </p>
    {/if}
  </div>

  <div class="flex flex-col gap-1.5">
    <Label for="first-admin-confirm">The password again</Label>
    <PasswordInput
      id="first-admin-confirm"
      bind:value={confirm}
      autocomplete="new-password"
      minlength={8}
      maxlength={72}
      required
    />
  </div>

  {#if error}
    <p class="text-sm text-status-overdue" role="alert">{error}</p>
  {/if}

  <Button type="submit" size="tap" disabled={busy}>
    {busy ? "Creating your account…" : "Create my account"}
  </Button>
</form>
