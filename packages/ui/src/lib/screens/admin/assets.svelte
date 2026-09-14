<script lang="ts">
  /**
   * Admin → Assets (design-system.md §8.7).
   *
   * The table stays **flat** — the same `<UnitRow>` the browse list uses, with no
   * `<ModelRow>` around it. An operator editing assets works unit by unit and the
   * model grouping only gets in the way (§8.2). Sharing the row component is the
   * point: the admin table cannot drift from what students see.
   *
   * Two server-side rules this screen has to surface *as written* rather than as a
   * generic failure: delete is refused for anything that has ever been checked out
   * (because `custody_events` cascades and the trail is the point), and the status
   * toggle cannot touch `checked_out`.
   */
  import PlusIcon from "@lucide/svelte/icons/plus"
  import PencilIcon from "@lucide/svelte/icons/pencil"
  import TrashIcon from "@lucide/svelte/icons/trash-2"
  import ImageIcon from "@lucide/svelte/icons/image"
  import CircleSlashIcon from "@lucide/svelte/icons/circle-slash"
  import CircleCheckIcon from "@lucide/svelte/icons/circle-check"
  import { toast } from "svelte-sonner"
  import * as AlertDialog from "@stockroom/ui/components/ui/alert-dialog"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import * as Select from "@stockroom/ui/components/ui/select"
  import * as Tooltip from "@stockroom/ui/components/ui/tooltip"
  import EmptyState from "@stockroom/ui/components/app/empty-state.svelte"
  import UnitRow from "@stockroom/ui/components/app/unit-row.svelte"
  import * as api from "../../api/index"
  import type { AssetInput, AssetListItem, CategoryNode } from "../../api/types"
  import { catalog } from "../../stores/catalog.svelte"

  let units = $state<AssetListItem[]>([])
  let loading = $state(true)
  let error = $state<string | null>(null)
  let search = $state("")

  let formOpen = $state(false)
  let editing = $state<AssetListItem | null>(null)
  let form = $state<AssetInput>(blankForm())
  let saving = $state(false)
  let formError = $state<string | null>(null)

  let deleteTarget = $state<AssetListItem | null>(null)
  let deleteError = $state<string | null>(null)

  let photoTarget = $state<AssetListItem | null>(null)
  let photoFiles = $state<FileList | undefined>(undefined)
  let uploading = $state(false)

  function blankForm(): AssetInput {
    return {
      asset_tag: "",
      name: "",
      description: null,
      category_id: null,
      serial_number: null,
      condition: null,
      purchase_date: null,
      purchase_price: null,
      warranty_expiration: null,
    }
  }

  /** Flatten the tree into indented options. An asset may file under any node. */
  function flatten(nodes: CategoryNode[], depth = 0): { id: string; label: string }[] {
    return nodes.flatMap((node) => [
      { id: node.id, label: `${" ".repeat(depth)}${node.name}` },
      ...flatten(node.children, depth + 1),
    ])
  }
  const categoryOptions = $derived(flatten(catalog.tree))
  const categoryLabel = $derived(
    () => categoryOptions.find((o) => o.id === form.category_id)?.label.trim() ?? "No category"
  )

  async function load() {
    loading = true
    error = null
    try {
      units = await api.listAssets({ q: search.trim() || undefined })
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      loading = false
    }
  }

  $effect(() => {
    void search
    load()
  })

  function openCreate() {
    editing = null
    form = blankForm()
    formError = null
    formOpen = true
  }

  function openEdit(unit: AssetListItem) {
    editing = unit
    form = {
      asset_tag: unit.asset_tag,
      name: unit.name,
      description: unit.description,
      category_id: unit.category_id,
      serial_number: unit.serial_number,
      condition: unit.condition,
      // The server takes YYYY-MM-DD; the payload carries a full timestamp.
      purchase_date: unit.purchase_date ? unit.purchase_date.slice(0, 10) : null,
      purchase_price: unit.purchase_price,
      warranty_expiration: unit.warranty_expiration ? unit.warranty_expiration.slice(0, 10) : null,
    }
    formError = null
    formOpen = true
  }

  async function save(event: SubmitEvent) {
    event.preventDefault()
    saving = true
    formError = null
    try {
      const payload: AssetInput = {
        ...form,
        description: form.description || null,
        serial_number: form.serial_number || null,
        condition: form.condition || null,
        purchase_date: form.purchase_date || null,
        warranty_expiration: form.warranty_expiration || null,
        purchase_price: form.purchase_price ?? null,
      }
      if (editing) await api.updateAsset(editing.id, payload)
      else await api.createAsset(payload)
      formOpen = false
      toast.success(editing ? "Asset updated" : "Asset created")
      await load()
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err)
    } finally {
      saving = false
    }
  }

  async function toggleStatus(unit: AssetListItem) {
    try {
      const next = unit.status === "unavailable" ? "available" : "unavailable"
      await api.setAssetStatus(unit.id, next)
      toast.success(next === "available" ? "Back in service" : "Marked unavailable")
      await load()
    } catch (err) {
      // "cannot touch checked_out" arrives here; show it as written.
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }

  async function confirmDelete() {
    if (!deleteTarget) return
    deleteError = null
    try {
      await api.deleteAsset(deleteTarget.id)
      toast.success("Asset deleted")
      deleteTarget = null
      await load()
    } catch (err) {
      // The server's message names the reason and the alternative ("mark it
      // unavailable instead"). Keep the dialog open and print it.
      deleteError = err instanceof Error ? err.message : String(err)
    }
  }

  async function uploadPhoto() {
    const file = photoFiles?.[0]
    if (!photoTarget || !file) return
    uploading = true
    try {
      await api.setAssetPhoto(photoTarget.id, file)
      toast.success("Photo uploaded")
      photoTarget = null
      photoFiles = undefined
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      uploading = false
    }
  }
</script>

<div data-density="compact" class="flex flex-col gap-3">
  <div class="flex items-center gap-3">
    <Input bind:value={search} placeholder="Search name, serial, tag…" class="max-w-xs" />
    <span class="flex-1"></span>
    <Button onclick={openCreate}>
      <PlusIcon aria-hidden="true" />
      New asset
    </Button>
  </div>

  {#if error}
    <EmptyState title="Couldn't load assets" description={error}>
      {#snippet action()}
        <Button variant="secondary" onclick={load}>Try again</Button>
      {/snippet}
    </EmptyState>
  {:else if loading}
    <p class="text-fg-muted">Loading…</p>
  {:else if units.length === 0}
    <EmptyState
      title="No assets"
      description={search ? "Nothing matches that search." : "Create the first unit to get started."}
    />
  {:else}
    <div class="overflow-hidden rounded-xl border border-line-strong">
      {#each units as unit (unit.id)}
        <UnitRow {unit} showName>
          {#snippet actions(row)}
            <Tooltip.Provider>
              <Tooltip.Root>
                <Tooltip.Trigger>
                  {#snippet child({ props })}
                    <Button
                      {...props}
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Edit ${row.name}`}
                      onclick={() => openEdit(row)}
                    >
                      <PencilIcon aria-hidden="true" />
                    </Button>
                  {/snippet}
                </Tooltip.Trigger>
                <Tooltip.Content>Edit</Tooltip.Content>
              </Tooltip.Root>

              <Tooltip.Root>
                <Tooltip.Trigger>
                  {#snippet child({ props })}
                    <Button
                      {...props}
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Upload a photo for ${row.name}`}
                      onclick={() => {
                        photoTarget = row
                        photoFiles = undefined
                      }}
                    >
                      <ImageIcon aria-hidden="true" />
                    </Button>
                  {/snippet}
                </Tooltip.Trigger>
                <Tooltip.Content>Photo</Tooltip.Content>
              </Tooltip.Root>

              <Tooltip.Root>
                <Tooltip.Trigger>
                  {#snippet child({ props })}
                    <Button
                      {...props}
                      variant="ghost"
                      size="icon-sm"
                      aria-label={
                        row.status === "unavailable"
                          ? `Put ${row.name} back in service`
                          : `Mark ${row.name} unavailable`
                      }
                      onclick={() => toggleStatus(row)}
                    >
                      {#if row.status === "unavailable"}
                        <CircleCheckIcon aria-hidden="true" />
                      {:else}
                        <CircleSlashIcon aria-hidden="true" />
                      {/if}
                    </Button>
                  {/snippet}
                </Tooltip.Trigger>
                <Tooltip.Content>
                  {row.status === "unavailable" ? "Back in service" : "Mark unavailable"}
                </Tooltip.Content>
              </Tooltip.Root>

              <Tooltip.Root>
                <Tooltip.Trigger>
                  {#snippet child({ props })}
                    <Button
                      {...props}
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Delete ${row.name}`}
                      onclick={() => {
                        deleteTarget = row
                        deleteError = null
                      }}
                    >
                      <TrashIcon class="text-destructive" aria-hidden="true" />
                    </Button>
                  {/snippet}
                </Tooltip.Trigger>
                <Tooltip.Content>Delete</Tooltip.Content>
              </Tooltip.Root>
            </Tooltip.Provider>
          {/snippet}
        </UnitRow>
      {/each}
    </div>
  {/if}
</div>

<!-- Create / edit. No photo field and no status field: SetAssetPhoto is that
     column's only writer and the status toggle is its own endpoint (CLAUDE.md
     §13, "one writer per table"). -->
<Dialog.Root bind:open={formOpen}>
  <Dialog.Content>
    <form onsubmit={save} class="flex flex-col gap-3">
      <Dialog.Header>
        <Dialog.Title>{editing ? `Edit ${editing.name}` : "New asset"}</Dialog.Title>
        <Dialog.Description>
          One row per physical unit. The serial number is what the barcode sticker encodes.
        </Dialog.Description>
      </Dialog.Header>

      <div class="grid grid-cols-2 gap-3">
        <div class="flex flex-col gap-1.5">
          <Label for="asset-tag">Asset tag</Label>
          <Input id="asset-tag" bind:value={form.asset_tag} required />
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="serial">Serial number</Label>
          <Input id="serial" value={form.serial_number ?? ""} oninput={(e) => (form.serial_number = e.currentTarget.value)} placeholder="T7iBat-001" />
        </div>
      </div>

      <div class="flex flex-col gap-1.5">
        <Label for="asset-name">Name</Label>
        <Input id="asset-name" bind:value={form.name} required />
      </div>

      <div class="flex flex-col gap-1.5">
        <Label for="asset-category">Category</Label>
        <Select.Root type="single" value={form.category_id ?? ""} onValueChange={(v) => (form.category_id = v || null)}>
          <Select.Trigger id="asset-category" class="w-full">{categoryLabel()}</Select.Trigger>
          <Select.Content>
            <Select.Item value="">No category</Select.Item>
            {#each categoryOptions as option (option.id)}
              <Select.Item value={option.id}>{option.label}</Select.Item>
            {/each}
          </Select.Content>
        </Select.Root>
      </div>

      <div class="flex flex-col gap-1.5">
        <Label for="asset-description">Description</Label>
        <Input id="asset-description" value={form.description ?? ""} oninput={(e) => (form.description = e.currentTarget.value)} />
      </div>

      <div class="grid grid-cols-2 gap-3">
        <div class="flex flex-col gap-1.5">
          <Label for="asset-condition">Condition</Label>
          <Input id="asset-condition" value={form.condition ?? ""} oninput={(e) => (form.condition = e.currentTarget.value)} />
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="asset-purchase">Purchase date</Label>
          <Input id="asset-purchase" type="date" value={form.purchase_date ?? ""} oninput={(e) => (form.purchase_date = e.currentTarget.value)} />
        </div>
      </div>

      {#if formError}
        <p class="text-status-overdue" role="alert">{formError}</p>
      {/if}

      <Dialog.Footer>
        <Button type="button" variant="ghost" onclick={() => (formOpen = false)}>Cancel</Button>
        <Button type="submit" disabled={saving}>{saving ? "Saving…" : "Save"}</Button>
      </Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>

<AlertDialog.Root open={deleteTarget !== null} onOpenChange={(open) => !open && (deleteTarget = null)}>
  <AlertDialog.Content>
    <AlertDialog.Header>
      <AlertDialog.Title>Delete {deleteTarget?.name}?</AlertDialog.Title>
      <AlertDialog.Description>
        This can't be undone. Anything with custody history is refused by the server, because the
        trail is the point — mark it unavailable instead.
      </AlertDialog.Description>
    </AlertDialog.Header>
    {#if deleteError}
      <p class="text-status-overdue" role="alert">{deleteError}</p>
    {/if}
    <AlertDialog.Footer>
      <AlertDialog.Cancel>Cancel</AlertDialog.Cancel>
      <AlertDialog.Action variant="destructive" onclick={confirmDelete}>Delete</AlertDialog.Action>
    </AlertDialog.Footer>
  </AlertDialog.Content>
</AlertDialog.Root>

<Dialog.Root open={photoTarget !== null} onOpenChange={(open) => !open && (photoTarget = null)}>
  <Dialog.Content>
    <Dialog.Header>
      <Dialog.Title>Photo for {photoTarget?.name}</Dialog.Title>
      <Dialog.Description>
        JPEG, PNG, GIF or WebP, up to 10 MB. It replaces any existing photo.
      </Dialog.Description>
    </Dialog.Header>
    <Input type="file" accept=".jpg,.jpeg,.png,.gif,.webp" bind:files={photoFiles} />
    <Dialog.Footer>
      <Button variant="ghost" onclick={() => (photoTarget = null)}>Cancel</Button>
      <Button disabled={uploading || !photoFiles?.length} onclick={uploadPhoto}>
        {uploading ? "Uploading…" : "Upload"}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
