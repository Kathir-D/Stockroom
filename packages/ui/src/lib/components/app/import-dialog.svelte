<script lang="ts" generics="R">
  /**
   * Upload one file to an import endpoint and show what happened
   * (CLAUDE.md §13, Phase B). Used for the category tree and the asset list;
   * the roster keeps its own dialog because it also takes a photo folder.
   *
   * The result stays on screen until the dialog is closed. An import report is
   * the only place a per-row failure is ever shown, and a toast that vanishes
   * in four seconds is how "line 212: no such category" is never read.
   */
  import type { Snippet } from "svelte"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import { Input } from "@stockroom/ui/components/ui/input"

  let {
    open = $bindable(false),
    title,
    description,
    accept,
    upload,
    report,
    onDone,
  }: {
    open?: boolean
    title: string
    description: Snippet
    accept: string
    upload: (file: File) => Promise<R>
    report: Snippet<[R]>
    onDone?: () => void
  } = $props()

  let files = $state<FileList | undefined>(undefined)
  let busy = $state(false)
  let error = $state<string | null>(null)
  let result = $state<R | null>(null)

  $effect(() => {
    if (open) return
    files = undefined
    error = null
    result = null
  })

  async function run() {
    const file = files?.[0]
    if (!file) return
    busy = true
    error = null
    result = null
    try {
      result = await upload(file)
      onDone?.()
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      busy = false
    }
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="max-w-lg">
    <Dialog.Header>
      <Dialog.Title>{title}</Dialog.Title>
      <Dialog.Description>{@render description()}</Dialog.Description>
    </Dialog.Header>
    <Input type="file" {accept} bind:files />
    {#if error}
      <p class="text-sm text-status-overdue" role="alert">{error}</p>
    {/if}
    {#if result}
      <div class="max-h-72 overflow-y-auto" role="status">{@render report(result)}</div>
    {/if}
    <Dialog.Footer>
      <Button variant="ghost" onclick={() => (open = false)}>{result ? "Close" : "Cancel"}</Button>
      <Button disabled={busy || !files?.length} onclick={run}>{busy ? "Importing…" : "Import"}</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
