<script lang="ts">
  /**
   * Kits (TODO Phase 8, CLAUDE.md §2): a named bundle of units that goes out
   * and comes back together.
   *
   * **One screen, not two.** Everybody sees the same list — a student reads it
   * to put a kit in the cart, an admin edits it in place — because a separate
   * admin-panel copy would be a second list of the same rows and the admin's is
   * the one that would go stale. The admin controls are simply absent for
   * everyone else, the way the sidebar's Admin group is (design-system.md §8.7).
   *
   * **A kit is added whole or not at all.** Half a kit is a camera with no lens,
   * and the person carrying it finds out at the shoot, so **Add to cart** is
   * disabled unless every unit is on the shelf. The press still re-checks per
   * unit (`lib/kits.ts`): `checkable` was true when this list was fetched, and
   * on a shared closet machine somebody else can take a unit in between.
   *
   * **Returning is the other way round, deliberately.** One press checks in
   * every unit that is out and reports the rest, because the units are
   * physically on the counter and refusing all four because one was already
   * back would leave the database claiming somebody still holds items they
   * returned.
   */
  import BoxesIcon from "@lucide/svelte/icons/boxes"
  import PlusIcon from "@lucide/svelte/icons/plus"
  import PencilIcon from "@lucide/svelte/icons/pencil"
  import TrashIcon from "@lucide/svelte/icons/trash-2"
  import UndoIcon from "@lucide/svelte/icons/undo-2"
  import XIcon from "@lucide/svelte/icons/x"
  import { toast } from "svelte-sonner"
  import * as AlertDialog from "@stockroom/ui/components/ui/alert-dialog"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import { Skeleton } from "@stockroom/ui/components/ui/skeleton"
  import EmptyState from "@stockroom/ui/components/app/empty-state.svelte"
  import StatusDot from "@stockroom/ui/components/app/status-dot.svelte"
  import UnitRow from "@stockroom/ui/components/app/unit-row.svelte"
  import * as api from "../api/index"
  import type { AssetListItem, KitCheckInResult, KitDetail } from "../api/types"
  import { kitCartPlan, kitIsAddable, kitPlanMessage } from "../kits"
  import { groupStatus } from "../status"
  import { cart } from "../stores/cart.svelte"
  import { kits } from "../stores/kits.svelte"
  import { session } from "../stores/session.svelte"

  let {
    /**
     * A kit came back. The shell reloads the browse list and the viewer's own
     * overdue flag, exactly as a single check-in does.
     */
    onCheckedIn,
  }: {
    onCheckedIn: (result: KitCheckInResult) => void | Promise<void>
  } = $props()

  let busy = $state(false)

  /** The create/rename dialog. `editing` null means "create". */
  let formOpen = $state(false)
  let editing = $state<KitDetail | null>(null)
  let formName = $state("")
  let formDescription = $state("")
  let formError = $state<string | null>(null)

  let deleteTarget = $state<KitDetail | null>(null)
  let deleteError = $state<string | null>(null)

  /** The add-a-unit picker, open over one kit at a time. */
  let pickerFor = $state<KitDetail | null>(null)
  let pickerQuery = $state("")
  let pickerResults = $state<AssetListItem[]>([])
  let pickerError = $state<string | null>(null)
  let pickerSearching = $state(false)
  /** Matches the browse screen, so the two searches feel like one. */
  const SEARCH_DEBOUNCE_MS = 200
  const MAX_RESULTS = 8
  let searchTimer: ReturnType<typeof setTimeout> | null = null

  $effect(() => {
    if (!kits.loaded && !kits.loading) void kits.reload()
  })

  // The picker's own search. Debounced and server-side for the same reason the
  // command palette's is: `GET /assets?q=` has the index and matches partial
  // identifiers a client-side `includes` over one page cannot.
  $effect(() => {
    const kit = pickerFor
    const query = pickerQuery.trim()
    if (!kit) return
    if (searchTimer) clearTimeout(searchTimer)
    pickerSearching = true
    searchTimer = setTimeout(async () => {
      try {
        const found = await api.listAssets(query ? { q: query } : {})
        // Units already in this kit are dropped rather than shown and refused:
        // the server would answer 409, and the press buys nothing either way.
        const inKit = new Set(kit.items.map((u) => u.id))
        pickerResults = found.filter((u) => !inKit.has(u.id)).slice(0, MAX_RESULTS)
        pickerError = null
      } catch (error) {
        pickerError = error instanceof Error ? error.message : String(error)
        pickerResults = []
      } finally {
        pickerSearching = false
      }
    }, SEARCH_DEBOUNCE_MS)
    return () => {
      if (searchTimer) clearTimeout(searchTimer)
    }
  })

  function openCreate() {
    editing = null
    formName = ""
    formDescription = ""
    formError = null
    formOpen = true
  }

  function openRename(kit: KitDetail) {
    editing = kit
    formName = kit.name
    formDescription = kit.description ?? ""
    formError = null
    formOpen = true
  }

  async function save(event: SubmitEvent) {
    event.preventDefault()
    const name = formName.trim()
    if (!name) {
      formError = "A name is required."
      return
    }
    const description = formDescription.trim() || null
    busy = true
    formError = null
    try {
      const saved = editing
        ? await api.updateKit(editing.id, { name, description })
        : await api.createKit({ name, description })
      kits.patch(saved)
      toast.success(editing ? "Kit renamed" : "Kit created")
      formOpen = false
    } catch (error) {
      // "a kit with that name already exists" — the server's wording, which
      // says the match is case-insensitive without saying "index".
      formError = error instanceof Error ? error.message : String(error)
    } finally {
      busy = false
    }
  }

  async function confirmDelete() {
    if (!deleteTarget) return
    deleteError = null
    try {
      await api.deleteKit(deleteTarget.id)
      kits.remove(deleteTarget.id)
      toast.success("Kit deleted")
      deleteTarget = null
    } catch (error) {
      deleteError = error instanceof Error ? error.message : String(error)
    }
  }

  async function addItem(kit: KitDetail, unit: AssetListItem) {
    busy = true
    try {
      kits.patch(await api.addKitItem(kit.id, unit.id))
      // The picker stays open: adding six units to a kit is six presses, and
      // reopening it each time is five presses that buy nothing.
      pickerFor = kits.kits.find((k) => k.id === kit.id) ?? kit
      pickerResults = pickerResults.filter((u) => u.id !== unit.id)
      toast.success(`${unit.name} added to ${kit.name}`)
    } catch (error) {
      // 409 names the kit that already has the unit. Show it as written.
      toast.error(error instanceof Error ? error.message : String(error))
    } finally {
      busy = false
    }
  }

  async function removeItem(kit: KitDetail, unit: AssetListItem) {
    busy = true
    try {
      kits.patch(await api.removeKitItem(kit.id, unit.id))
      toast.success(`${unit.name} removed from ${kit.name}`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : String(error))
    } finally {
      busy = false
    }
  }

  function addKitToCart(kit: KitDetail) {
    if (session.checkoutBlocked) return
    const plan = kitCartPlan(kit, cart.ids)
    const message = kitPlanMessage(kit, plan)
    if (!kitIsAddable(plan)) {
      toast.error(message)
      // The screen disagreed with the server about what is on the shelf, which
      // means somebody else moved a unit. Reload rather than leave the row
      // saying the kit is ready to go.
      void kits.reload()
      return
    }
    for (const unit of plan.addable) cart.add(unit.id)
    toast.success(message)
  }

  async function returnKit(kit: KitDetail) {
    busy = true
    try {
      const result = await api.checkInKit(kit.id)
      kits.patch(await api.getKit(kit.id))
      if (result.failed.length > 0) {
        // Never a silent partial: the units that did not come back are named,
        // with the server's own reason.
        toast.error(
          `${result.failed.length} item${result.failed.length === 1 ? "" : "s"} could not be checked in: ` +
            result.failed.map((f) => `${f.name} (${f.reason})`).join("; ")
        )
      } else if (result.returned.length === 0) {
        toast.info(`Nothing in ${kit.name} was checked out.`)
      } else {
        toast.success(
          `${result.returned.length} item${result.returned.length === 1 ? "" : "s"} from ${kit.name} checked in`
        )
      }
      await onCheckedIn(result)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : String(error))
    } finally {
      busy = false
    }
  }
</script>

<div class="flex flex-col gap-3">
  <div class="flex items-center gap-3">
    <div class="flex flex-col">
      <h1 class="text-base font-semibold text-fg">Kits</h1>
      <p class="text-xs text-fg-muted">
        A bundle that goes out and comes back together. A kit is added to the cart whole, and
        checked out like any other cart.
      </p>
    </div>
    <span class="flex-1"></span>
    {#if session.isAdmin}
      <Button onclick={openCreate}>
        <PlusIcon aria-hidden="true" />
        New kit
      </Button>
    {/if}
  </div>

  {#if kits.error}
    <EmptyState title="Couldn't load the kits" description={kits.error} class="border-status-overdue/40">
      {#snippet action()}
        <Button variant="secondary" onclick={() => kits.reload()}>Try again</Button>
      {/snippet}
    </EmptyState>
  {:else if kits.loading && !kits.loaded}
    <div class="flex flex-col gap-2" aria-busy="true" aria-label="Loading kits">
      {#each Array(3) as _, index (index)}
        <Skeleton class="h-28 rounded-xl" />
      {/each}
    </div>
  {:else if kits.kits.length === 0}
    <EmptyState
      title="No kits yet"
      description={session.isAdmin
        ? "A kit is a named bundle — a camera, its lens and the bag it lives in — added to the cart in one press."
        : "Nobody has built a kit yet. An admin can create one."}
    >
      {#snippet action()}
        {#if session.isAdmin}
          <Button variant="secondary" onclick={openCreate}>New kit</Button>
        {/if}
      {/snippet}
    </EmptyState>
  {:else}
    <div class="flex flex-col gap-2">
      {#each kits.kits as kit (kit.id)}
        {@const plan = kitCartPlan(kit, cart.ids)}
        {@const addable = kitIsAddable(plan) && !session.checkoutBlocked}
        <section class="rounded-xl border border-line-strong">
          <header class="flex flex-wrap items-center gap-2 p-3">
            <BoxesIcon class="size-4 shrink-0 text-fg-faint" aria-hidden="true" />
            <div class="flex min-w-0 flex-col">
              <h2 class="truncate text-sm font-medium text-fg">{kit.name}</h2>
              {#if kit.description}
                <p class="truncate text-xs text-fg-muted">{kit.description}</p>
              {/if}
            </div>
            <StatusDot status={groupStatus(kit.available, kit.items.length)} class="ml-1" />
            <span class="flex-1"></span>

            {#if kit.checked_out > 0}
              <Button variant="secondary" size="sm" disabled={busy} onclick={() => returnKit(kit)}>
                <UndoIcon aria-hidden="true" />
                Return kit
              </Button>
            {/if}
            <!-- Disabled rather than hidden: the reason a kit cannot go out is
                 the count line right beside it, and a button that vanishes
                 leaves the person looking for it. -->
            <Button size="sm" disabled={!addable} onclick={() => addKitToCart(kit)}>
              <PlusIcon aria-hidden="true" />
              Add to cart
            </Button>

            {#if session.isAdmin}
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`Add an item to ${kit.name}`}
                onclick={() => {
                  pickerFor = kit
                  pickerQuery = ""
                  pickerResults = []
                  pickerError = null
                }}
              >
                <PlusIcon aria-hidden="true" />
              </Button>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`Rename ${kit.name}`}
                onclick={() => openRename(kit)}
              >
                <PencilIcon aria-hidden="true" />
              </Button>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`Delete ${kit.name}`}
                onclick={() => {
                  deleteTarget = kit
                  deleteError = null
                }}
              >
                <TrashIcon class="text-destructive" aria-hidden="true" />
              </Button>
            {/if}
          </header>

          {#if kit.items.length === 0}
            <p class="border-t border-line px-3 py-4 text-xs text-fg-muted">
              This kit is empty.{session.isAdmin ? " Use + to put units in it." : ""}
            </p>
          {:else}
            <div class="flex flex-col border-t border-line">
              {#each kit.items as unit (unit.id)}
                <UnitRow
                  {unit}
                  viewerId={session.profile?.id ?? null}
                  isAdmin={session.isAdmin}
                  inCart={cart.has(unit.id)}
                  canAdd={false}
                >
                  {#snippet actions(item)}
                    {#if session.isAdmin}
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`Remove ${item.name} from ${kit.name}`}
                        disabled={busy}
                        onclick={() => removeItem(kit, item)}
                      >
                        <XIcon aria-hidden="true" />
                      </Button>
                    {/if}
                  {/snippet}
                </UnitRow>
              {/each}
            </div>
          {/if}
        </section>
      {/each}
    </div>
  {/if}
</div>

<Dialog.Root bind:open={formOpen}>
  <Dialog.Content>
    <form onsubmit={save} class="flex flex-col gap-3">
      <Dialog.Header>
        <Dialog.Title>{editing ? `Rename ${editing.name}` : "New kit"}</Dialog.Title>
        <Dialog.Description>
          Name it after the thing it lives in, the way it is labelled on the shelf. Units go in one
          at a time afterwards.
        </Dialog.Description>
      </Dialog.Header>

      <div class="flex flex-col gap-1.5">
        <Label for="kit-name">Name</Label>
        <Input id="kit-name" bind:value={formName} required />
      </div>
      <div class="flex flex-col gap-1.5">
        <Label for="kit-description">Description</Label>
        <Input id="kit-description" bind:value={formDescription} placeholder="Optional" />
      </div>

      {#if formError}
        <p class="text-status-overdue" role="alert">{formError}</p>
      {/if}

      <Dialog.Footer>
        <Button type="button" variant="ghost" onclick={() => (formOpen = false)}>Cancel</Button>
        <Button type="submit" disabled={busy}>{busy ? "Saving…" : "Save"}</Button>
      </Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>

<Dialog.Root
  open={pickerFor !== null}
  onOpenChange={(open) => !open && (pickerFor = null)}
>
  <!-- Wider than the 560px default: the rows inside are <UnitRow>, the same
       component the browse list uses, and at the default width the name column
       truncates to "Large t…" — which is the one thing somebody choosing
       between two identical tripods needs to read. -->
  <Dialog.Content class="max-w-[min(760px,calc(100%-2rem))]">
    <Dialog.Header>
      <Dialog.Title>Add an item to {pickerFor?.name}</Dialog.Title>
      <Dialog.Description>
        Search by name or serial. An item belongs to one kit, so a unit already in another is
        refused with the name of that kit.
      </Dialog.Description>
    </Dialog.Header>

    <Input bind:value={pickerQuery} placeholder="Search equipment…" aria-label="Search equipment" />

    {#if pickerError}
      <p class="text-status-overdue" role="alert">{pickerError}</p>
    {:else if pickerSearching && pickerResults.length === 0}
      <p class="py-2 text-xs text-fg-muted">Searching…</p>
    {:else if pickerResults.length === 0}
      <p class="py-2 text-xs text-fg-muted">Nothing matched.</p>
    {:else}
      <ul class="flex max-h-72 flex-col overflow-y-auto">
        {#each pickerResults as unit (unit.id)}
          <li>
            <UnitRow
              {unit}
              viewerId={session.profile?.id ?? null}
              isAdmin={session.isAdmin}
              canAdd={false}
            >
              {#snippet actions(item)}
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={busy}
                  onclick={() => pickerFor && addItem(pickerFor, item)}
                >
                  Add
                </Button>
              {/snippet}
            </UnitRow>
          </li>
        {/each}
      </ul>
    {/if}

    <Dialog.Footer>
      <Button variant="ghost" onclick={() => (pickerFor = null)}>Done</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<AlertDialog.Root open={deleteTarget !== null} onOpenChange={(open) => !open && (deleteTarget = null)}>
  <AlertDialog.Content>
    <AlertDialog.Header>
      <AlertDialog.Title>Delete {deleteTarget?.name}?</AlertDialog.Title>
      <AlertDialog.Description>
        This removes the bundle only. Every unit in it, its status and its whole history stay
        exactly as they are.
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
