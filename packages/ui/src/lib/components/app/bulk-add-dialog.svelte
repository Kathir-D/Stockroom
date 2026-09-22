<script lang="ts">
  /**
   * "Add 12 × Canon LP-E6 battery" (TEMPLATE-TODO Phase B).
   *
   * Two steps on purpose: the serials are shown before anything is written,
   * because a generator that writes first is one whose off-by-one is found as
   * twelve rows to delete by hand. Numbering continues after the highest
   * serial already using the prefix, so next year's re-order extends the
   * series instead of colliding with it.
   *
   * On success it hands the new units to `onCreated`, which the assets screen
   * uses to open the label printer on exactly those units: a battery with no
   * sticker cannot be scanned back in.
   */
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import * as Select from "@stockroom/ui/components/ui/select"
  import * as api from "../../api/index"
  import type { AssetDetail, BulkAddInput, BulkPreview } from "../../api/types"

  let {
    open = $bindable(false),
    categories,
    onCreated,
  }: {
    open?: boolean
    categories: { id: string; label: string }[]
    onCreated: (units: AssetDetail[]) => void
  } = $props()

  let name = $state("")
  let prefix = $state("")
  let count = $state("")
  let categoryId = $state<string | null>(null)
  let preview = $state<BulkPreview | null>(null)
  let busy = $state(false)
  let error = $state<string | null>(null)

  $effect(() => {
    if (open) return
    preview = null
    error = null
  })

  // Anything edited after a preview makes the preview a lie; drop it.
  $effect(() => {
    void name, prefix, count, categoryId
    preview = null
  })

  function input(): BulkAddInput {
    const n = Number(count.trim())
    if (!Number.isInteger(n) || n < 1) throw new Error("How many? A whole number, 1 or more.")
    return { name: name.trim(), prefix: prefix.trim(), count: n, category_id: categoryId }
  }

  async function step() {
    busy = true
    error = null
    try {
      if (!preview) {
        preview = await api.bulkPreview(input())
      } else {
        const made = await api.bulkAdd(input())
        open = false
        onCreated(made)
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      busy = false
    }
  }

  const categoryLabel = $derived(
    categories.find((c) => c.id === categoryId)?.label.trim() ?? "No category",
  )
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="max-w-lg">
    <Dialog.Header>
      <Dialog.Title>Add several identical units</Dialog.Title>
      <Dialog.Description>
        Batteries, SD cards, bags, cables: one name, numbered serials. Anything with a
        manufacturer's barcode already on it can use that as its serial instead — no sticker
        needed.
      </Dialog.Description>
    </Dialog.Header>

    <div class="flex flex-col gap-1.5">
      <Label for="bulk-name">Name</Label>
      <Input id="bulk-name" bind:value={name} placeholder="Canon LP-E6 battery" />
    </div>
    <div class="grid grid-cols-2 gap-3">
      <div class="flex flex-col gap-1.5">
        <Label for="bulk-prefix">Serial prefix</Label>
        <Input id="bulk-prefix" bind:value={prefix} placeholder="LPE6" class="font-mono" spellcheck={false} />
      </div>
      <div class="flex flex-col gap-1.5">
        <Label for="bulk-count">How many</Label>
        <Input id="bulk-count" bind:value={count} inputmode="numeric" placeholder="12" />
      </div>
    </div>
    <p class="-mt-1 text-xs text-fg-faint">
      Keep the prefix short: small battery labels fit about nine characters, and
      <code class="font-mono">LPE6-001</code> is eight.
    </p>
    <div class="flex flex-col gap-1.5">
      <Label for="bulk-category">Category</Label>
      <Select.Root type="single" value={categoryId ?? ""} onValueChange={(v) => (categoryId = v || null)}>
        <Select.Trigger id="bulk-category" class="w-full">{categoryLabel}</Select.Trigger>
        <Select.Content>
          <Select.Item value="">No category</Select.Item>
          {#each categories as option (option.id)}
            <Select.Item value={option.id}>{option.label}</Select.Item>
          {/each}
        </Select.Content>
      </Select.Root>
    </div>

    {#if preview}
      <div class="flex flex-col gap-1 rounded-(--radius) border border-line p-2" role="status">
        <p class="text-sm text-fg">
          {preview.serials.length} × “{preview.name}”:
        </p>
        <p class="font-mono text-xs break-words text-fg-muted">
          {preview.serials.length > 8
            ? `${preview.serials.slice(0, 3).join(", ")} … ${preview.serials.slice(-2).join(", ")}`
            : preview.serials.join(", ")}
        </p>
        {#if preview.existing.length}
          <p class="text-sm text-status-overdue">
            Already taken: {preview.existing.join(", ")}. Change the prefix.
          </p>
        {/if}
      </div>
    {/if}
    {#if error}
      <p class="text-sm text-status-overdue" role="alert">{error}</p>
    {/if}

    <Dialog.Footer>
      <Button variant="ghost" onclick={() => (open = false)}>Cancel</Button>
      <Button disabled={busy || (preview !== null && preview.existing.length > 0)} onclick={step}>
        {busy ? "Working…" : preview ? `Create ${preview.serials.length}` : "Show the serials"}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
