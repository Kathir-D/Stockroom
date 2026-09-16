<script lang="ts">
  /**
   * A password field with a reveal toggle.
   *
   * Every password in this app is typed on a machine in a shared closet, at a
   * keyboard the person did not choose, often in a hurry — which is the exact
   * situation a masked field is worst in. Without a way to check what you typed,
   * the only feedback a typo gets is a failed sign-in, and the failure looks
   * identical to "wrong password" (the server deliberately does not distinguish
   * them, §7). So: a reveal toggle, on every password field, not just some.
   *
   * It is a `<button type="button">` on purpose. A bare `<button>` inside a form
   * submits it, so a toggle that forgot this would sign the user in each time
   * they tried to look at what they had typed.
   *
   * Toggling returns focus to the field, with the caret where it was. Nothing
   * else does: the sign-in screen's refocus-on-blur handler is attached to the
   * student-number input, not to these, so leaving focus on the button means the
   * next keystroke of a half-typed password goes nowhere.
   */
  import { tick } from "svelte"
  import EyeIcon from "@lucide/svelte/icons/eye"
  import EyeOffIcon from "@lucide/svelte/icons/eye-off"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { cn } from "@stockroom/ui/utils"
  import type { HTMLInputAttributes } from "svelte/elements"

  let {
    value = $bindable(""),
    ref = $bindable(null),
    class: className,
    ...rest
    // `files` is omitted as well as `type`: shadcn's Input splits into a file
    // branch and a text branch, and the text branch's props type forbids it.
  }: Omit<HTMLInputAttributes, "type" | "value" | "files"> & {
    value?: string
    ref?: HTMLInputElement | null
    class?: string
  } = $props()

  let revealed = $state(false)

  async function toggle() {
    const el = ref
    // Read the caret before the type changes: swapping `password` for `text`
    // rebuilds the field's value in some engines and drops the caret to the end.
    const caret = el?.selectionStart ?? null
    revealed = !revealed
    await tick()
    if (!el) return
    el.focus()
    if (caret !== null) el.setSelectionRange(caret, caret)
  }
</script>

<div class="relative">
  <Input
    bind:ref
    bind:value
    type={revealed ? "text" : "password"}
    class={cn("h-(--control-h) pr-(--control-h) text-lg", className)}
    {...rest}
  />
  <button
    type="button"
    onclick={toggle}
    disabled={rest.disabled}
    aria-label={revealed ? "Hide password" : "Show password"}
    aria-pressed={revealed}
    title={revealed ? "Hide password" : "Show password"}
    class="absolute inset-y-0 right-0 grid w-(--control-h) place-items-center rounded-r-lg text-fg-muted transition-colors hover:text-fg focus-visible:ring-3 focus-visible:ring-ring/50 outline-none disabled:pointer-events-none disabled:opacity-50"
  >
    {#if revealed}
      <EyeOffIcon class="size-4" aria-hidden="true" />
    {:else}
      <EyeIcon class="size-4" aria-hidden="true" />
    {/if}
  </button>
</div>
