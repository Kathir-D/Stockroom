<script lang="ts">
  /**
   * Signing in to Google through rclone — the dialog both Admin → Settings
   * (the backup's Drive target) and Admin → Photo wall open.
   *
   * By the time this opens, the server has started `rclone authorize` and it
   * is waiting on 127.0.0.1:53682 for Google's callback. The dialog is the
   * link to open, plus the fallback for when that callback never arrives:
   * the block `rclone authorize` prints, pasted by hand. One component rather
   * than a copy per screen, because the two would otherwise drift on the one
   * flow an admin is least likely to have seen before.
   *
   * What differs between the two callers is passed in: the wording, any extra
   * fields (the backup names its connection; the photo wall's is fixed), and
   * the command to run elsewhere for the pasted block — which for the photo
   * wall has to carry its read-only scope, since a pasted token's scope
   * cannot be checked.
   */
  import type { Snippet } from "svelte"
  import ExternalLinkIcon from "@lucide/svelte/icons/external-link"
  import { toast } from "svelte-sonner"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"

  let {
    open = $bindable(false),
    code = $bindable(""),
    url,
    title,
    description,
    busy = false,
    error = null,
    pasteCommand,
    fields,
    onfinish,
  }: {
    open?: boolean
    /** The pasted block, for when the browser's callback did not arrive. */
    code?: string
    /** rclone's local sign-in link. */
    url: string
    title: string
    description: string
    busy?: boolean
    error?: string | null
    /** The command to run on another machine to get a block to paste. */
    pasteCommand?: string
    /** Extra fields between the link and the paste box. */
    fields?: Snippet
    onfinish: () => void
  } = $props()

  async function copyLink() {
    try {
      await navigator.clipboard.writeText(url)
      toast.success("Link copied")
    } catch {
      // Clipboard access can be refused in a webview. The link is on screen
      // and selectable either way, so this is not worth an error surface.
      toast.info("Select the link and copy it by hand.")
    }
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Content>
    <Dialog.Header>
      <Dialog.Title>{title}</Dialog.Title>
      <Dialog.Description>{description}</Dialog.Description>
    </Dialog.Header>

    <div class="flex flex-col gap-3">
      <div class="flex flex-col gap-1">
        <Label for="connect-url">Sign-in link</Label>
        <!-- Readonly input rather than a bare anchor: the Wails webview may
             refuse to hand a link to the system browser, and a link nobody can
             copy is a dead end. Both affordances are here on purpose. -->
        <div class="flex gap-2">
          <Input id="connect-url" readonly value={url} class="font-mono text-xs" />
          <Button variant="secondary" onclick={copyLink}>Copy</Button>
        </div>
        <a
          href={url}
          target="_blank"
          rel="noreferrer"
          class="flex items-center gap-1 text-xs text-fg-muted underline underline-offset-2 hover:text-fg"
        >
          <ExternalLinkIcon class="size-3" aria-hidden="true" />
          Open in a browser
        </a>
      </div>

      {@render fields?.()}

      <div class="flex flex-col gap-1">
        <Label for="connect-code">If Google showed you a block of text, paste it here</Label>
        <Input
          id="connect-code"
          bind:value={code}
          placeholder="{'{'}&quot;access_token&quot;: …{'}'}"
          spellcheck={false}
          autocomplete="off"
          class="font-mono text-xs"
        />
        <p class="text-xs text-fg-faint">
          Usually not needed — the sign-in normally completes on its own. Leave it blank and press
          Finish.
          {#if pasteCommand}
            If the link will not open on this machine, run
            <code class="font-mono break-all">{pasteCommand}</code> on one where it does, and paste
            what it prints.
          {/if}
        </p>
      </div>

      {#if error}
        <p class="text-sm text-status-overdue" role="alert">{error}</p>
      {/if}
    </div>

    <Dialog.Footer>
      <Button variant="ghost" onclick={() => (open = false)} disabled={busy}>Cancel</Button>
      <Button onclick={onfinish} disabled={busy}>
        {busy ? "Finishing…" : "Finish"}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
