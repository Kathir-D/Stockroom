<script lang="ts">
  /**
   * Admin → Users → Paste names: add a class, club or camp from a pasted list,
   * with numbers from a range for people who have no ID card.
   *
   * It previews before it adds, like Add several for assets: the admin sees
   * every name and number the server worked out, and Add sends the same text
   * again. Changing the text or the first number throws the preview away, so
   * what is added is always what was last shown.
   *
   * People given a number from the range have no card to scan, and typing a
   * number needs a password they don't have yet, so the dialog ends by
   * offering to print their ID cards.
   */
  import { toast } from "svelte-sonner"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import * as Table from "@stockroom/ui/components/ui/table"
  import { Textarea } from "@stockroom/ui/components/ui/textarea"
  import * as api from "../../api/index"
  import type { AddNamesResult } from "../../api/types"
  import { saveBlob } from "../../download"

  let {
    open = $bindable(false),
    onAdded,
  }: {
    open?: boolean
    /** Called once the accounts exist, so the list can reload. */
    onAdded: () => void
  } = $props()

  let names = $state("")
  let firstNumber = $state("")
  let preview = $state<AddNamesResult | null>(null)
  let added = $state<AddNamesResult | null>(null)
  let busy = $state<"preview" | "add" | "cards" | null>(null)
  let error = $state<string | null>(null)

  $effect(() => {
    if (open) {
      names = ""
      firstNumber = ""
      preview = null
      added = null
      error = null
    }
  })

  /** Any edit makes the preview stale. */
  function edited() {
    preview = null
    error = null
  }

  const input = () => ({ names, first_number: firstNumber.trim() })

  async function runPreview() {
    busy = "preview"
    error = null
    try {
      preview = await api.previewAddNames(input())
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      busy = null
    }
  }

  async function runAdd() {
    busy = "add"
    error = null
    try {
      added = await api.addNames(input())
      preview = null
      toast.success(`Added ${added.rows.length} ${added.rows.length === 1 ? "person" : "people"}`)
      onAdded()
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      busy = null
    }
  }

  async function printCards() {
    const ids = (added?.rows ?? []).map((r) => r.id).filter((id): id is string => !!id)
    if (ids.length === 0) return
    busy = "cards"
    try {
      saveBlob(await api.userCards(ids), `stockroom-id-cards-${ids.length}.pdf`)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      busy = null
    }
  }

  const shown = $derived(added ?? preview)
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="max-w-2xl">
    <Dialog.Header>
      <Dialog.Title>Paste names</Dialog.Title>
      <Dialog.Description>
        One person per line: <code class="font-mono">Jane Doe</code>,
        <code class="font-mono">Doe, Jane</code>, or either with their student number after a
        comma. Columns pasted from a spreadsheet work too. This only adds people; to change existing
        accounts, import a roster.
      </Dialog.Description>
    </Dialog.Header>

    {#if !added}
      <div class="flex flex-col gap-3">
        <div class="flex flex-col gap-1.5">
          <Label for="add-names">Names</Label>
          <Textarea
            id="add-names"
            bind:value={names}
            oninput={edited}
            rows={8}
            spellcheck={false}
            placeholder={"Jane Doe\nDoe, John, 123456"}
          />
        </div>
        <div class="flex max-w-xs flex-col gap-1.5">
          <Label for="add-names-first">Number people without one from (optional)</Label>
          <Input
            id="add-names-first"
            bind:value={firstNumber}
            oninput={edited}
            spellcheck={false}
            autocomplete="off"
            placeholder="900001"
          />
          <p class="text-xs text-fg-faint">
            For groups with no ID numbers. It counts up from here and skips numbers already in use.
          </p>
        </div>
      </div>
    {/if}

    {#if error}
      <p class="text-sm text-status-overdue" role="alert">{error}</p>
    {/if}

    {#if shown}
      <div class="rounded-(--radius-lg) border border-line-strong p-3">
        <p class="text-sm text-fg">
          {#if added}
            Added {added.rows.length}. People with a new number have no card yet: print them one, or
            set them a password.
          {:else if shown.failed > 0}
            <span class="text-status-overdue">{shown.failed} of {shown.rows.length} lines have a problem.</span>
            Fix them above and preview again.
          {:else}
            {shown.rows.length} {shown.rows.length === 1 ? "person" : "people"} to add.
          {/if}
        </p>
        <div class="mt-2 max-h-64 overflow-y-auto">
          <Table.Root>
            <Table.Header>
              <Table.Row>
                <Table.Head>Line</Table.Head>
                <Table.Head>Name</Table.Head>
                <Table.Head>Student number</Table.Head>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {#each shown.rows as row (row.line)}
                <Table.Row>
                  <Table.Cell class="tabular-nums text-fg-muted">{row.line}</Table.Cell>
                  <Table.Cell>{[row.first_name, row.last_name].filter(Boolean).join(" ")}</Table.Cell>
                  <Table.Cell class={row.error ? "text-status-overdue" : "font-mono"}>
                    {#if row.error}
                      {row.error}
                    {:else}
                      {row.student_number}
                      {#if row.assigned}<span class="font-sans text-xs text-fg-faint">new</span>{/if}
                    {/if}
                  </Table.Cell>
                </Table.Row>
              {/each}
            </Table.Body>
          </Table.Root>
        </div>
      </div>
    {/if}

    <Dialog.Footer>
      <Button variant="ghost" onclick={() => (open = false)}>{added ? "Done" : "Cancel"}</Button>
      {#if added}
        <Button disabled={busy === "cards"} onclick={printCards}>
          {busy === "cards" ? "Making cards…" : "Print their ID cards"}
        </Button>
      {:else if preview && preview.failed === 0}
        <Button disabled={busy !== null} onclick={runAdd}>
          {busy === "add" ? "Adding…" : `Add ${preview.rows.length} ${preview.rows.length === 1 ? "person" : "people"}`}
        </Button>
      {:else}
        <Button disabled={busy !== null || !names.trim()} onclick={runPreview}>
          {busy === "preview" ? "Checking…" : "Preview"}
        </Button>
      {/if}
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
