<script lang="ts">
  /**
   * Picks what a student number looks like: digits only, letters and numbers,
   * or a custom pattern (TEMPLATE-TODO Phase B). Used by Admin → Settings and
   * by the first-run wizard, so the two cannot describe the rule differently.
   *
   * The test field is the point. A bad pattern that ships silently locks
   * everybody out of sign-in, and the failure is invisible until somebody scans
   * a card -- so the admin pastes a real ID here and sees the answer before
   * saving. The check runs in the browser, whose regex dialect is close to but
   * not the server's (RE2): a pattern using lookaround passes here and the
   * server refuses it on save, which is the safe direction.
   */
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import type { StudentNumberFormat } from "../../student-number"

  let {
    format = $bindable<StudentNumberFormat>("digits"),
    pattern = $bindable(""),
    idPrefix = "sn",
  }: { format?: StudentNumberFormat; pattern?: string; idPrefix?: string } = $props()

  let sample = $state("")

  const options: { value: StudentNumberFormat; label: string; hint: string }[] = [
    { value: "digits", label: "Digits only", hint: "123456 — what most ID cards carry." },
    { value: "alphanumeric", label: "Letters and numbers", hint: "AB12345 — letters are kept exactly as scanned." },
    { value: "custom", label: "Custom pattern", hint: "A regular expression, for anything else." },
  ]

  /** null = nothing to say yet; otherwise whether the sample would be accepted. */
  const verdict = $derived.by((): { ok: boolean; text: string } | null => {
    const s = sample.trim()
    if (!s) return null
    if (s.length > 32) return { ok: false, text: "Too long — student numbers are at most 32 characters." }
    let re: RegExp
    switch (format) {
      case "digits":
        re = /^[0-9]+$/
        break
      case "alphanumeric":
        re = /^[A-Za-z0-9]+$/
        break
      default:
        if (!pattern.trim()) return { ok: false, text: "Write a pattern first." }
        try {
          re = new RegExp(`^(?:${pattern.trim()})$`)
        } catch {
          return { ok: false, text: "That pattern is not a valid regular expression." }
        }
    }
    return re.test(s)
      ? { ok: true, text: `“${s}” would be accepted.` }
      : { ok: false, text: `“${s}” would be refused — nobody with an ID like that could sign in.` }
  })
</script>

<fieldset class="flex flex-col gap-2">
  <legend class="sr-only">Student number format</legend>
  {#each options as option (option.value)}
    <label class="flex cursor-pointer items-start gap-2 rounded-(--radius) p-1 hover:bg-raised">
      <input
        type="radio"
        name="{idPrefix}-format"
        value={option.value}
        bind:group={format}
        class="mt-1 accent-(--primary)"
      />
      <span class="flex flex-col">
        <span class="text-sm text-fg">{option.label}</span>
        <span class="text-xs text-fg-faint">{option.hint}</span>
      </span>
    </label>
  {/each}
</fieldset>

{#if format === "custom"}
  <div class="flex flex-col gap-1">
    <Label for="{idPrefix}-pattern">Pattern</Label>
    <Input
      id="{idPrefix}-pattern"
      bind:value={pattern}
      placeholder="[A-Z]{'{2}'}[0-9]{'{5}'}"
      class="font-mono"
      spellcheck={false}
      autocomplete="off"
    />
    <p class="text-xs text-fg-faint">
      The whole ID has to match; you don't need <code class="font-mono">^</code> or
      <code class="font-mono">$</code>.
    </p>
  </div>
{/if}

<div class="flex flex-col gap-1">
  <Label for="{idPrefix}-sample">Test it with a real ID</Label>
  <Input
    id="{idPrefix}-sample"
    bind:value={sample}
    placeholder="Paste or scan one here"
    spellcheck={false}
    autocomplete="off"
  />
  {#if verdict}
    <p class="text-xs {verdict.ok ? 'text-fg-muted' : 'text-status-overdue'}" role="status">
      {verdict.text}
    </p>
  {/if}
</div>
