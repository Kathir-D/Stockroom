<script lang="ts">
  /**
   * Admin → Out (design-system.md §8.7): "the highest-value admin screen".
   *
   * Two views of the roster of *who has what*: what is overdue, most late
   * first (`ListOverdueCustody`), and everything out, soonest due first
   * (`ListActiveCustody`, CLAUDE.md §7). CLAUDE.md §1.6 promised both lists;
   * only the first had a screen.
   *
   * Two row actions. **Check in** turns a row back into a shelved item without
   * hunting for it in the browse list. **Mark lost** closes a loan whose item
   * is not coming back (CLAUDE.md §7): before it, the only way out was to
   * record a return that never happened.
   *
   * This roster is admin-only, as distinct from the current holder of a
   * *named* item, which every signed-in user can see (CLAUDE.md §13,
   * 2026-09-12). Overdue reads the same `overdue_custody` view as sign-in and
   * `CheckOutAssets`, so "overdue" means one thing everywhere.
   */
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw"
  import { toast } from "svelte-sonner"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import { Skeleton } from "@stockroom/ui/components/ui/skeleton"
  import * as Table from "@stockroom/ui/components/ui/table"
  import EmptyState from "@stockroom/ui/components/app/empty-state.svelte"
  import Serial from "@stockroom/ui/components/app/serial.svelte"
  import * as api from "../../api/index"
  import type { CustodyRecord } from "../../api/types"
  import { dateTime } from "../../status"
  import { catalog } from "../../stores/catalog.svelte"
  import { session } from "../../stores/session.svelte"

  type View = "overdue" | "out"

  let view = $state<View>("overdue")
  let rows = $state<CustodyRecord[]>([])
  let loading = $state(true)
  let error = $state<string | null>(null)
  let busy = $state<string | null>(null)

  let lostTarget = $state<CustodyRecord | null>(null)
  let lostNote = $state("")
  let lostError = $state<string | null>(null)

  async function load() {
    loading = true
    error = null
    try {
      rows = view === "overdue" ? await api.overdueCustody() : await api.activeCustody()
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      loading = false
    }
  }

  $effect(() => {
    void view
    load()
  })

  async function checkIn(row: CustodyRecord) {
    busy = row.id
    try {
      const result = await api.checkIn(row.asset_id)
      toast.success(`${result.asset.name} checked in`)
      // Keep the browse list and the signed-in admin's own overdue flag honest
      // without a reload: both are read from the same rows this just changed.
      catalog.patchUnit(result.asset)
      await Promise.all([load(), session.refresh()])
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      busy = null
    }
  }

  function askLost(row: CustodyRecord) {
    lostTarget = row
    lostNote = ""
    lostError = null
  }

  async function confirmLost(event: SubmitEvent) {
    event.preventDefault()
    const row = lostTarget
    if (!row) return
    busy = row.id
    lostError = null
    try {
      const asset = await api.markLost(row.asset_id, lostNote)
      toast.success(`${asset.name} marked lost`)
      catalog.patchUnit(asset)
      lostTarget = null
      await Promise.all([load(), session.refresh()])
    } catch (err) {
      lostError = err instanceof Error ? err.message : String(err)
    } finally {
      busy = null
    }
  }
</script>

<div data-density="compact" class="flex flex-col gap-3">
  <div class="flex items-center gap-3">
    <div class="flex flex-col">
      <h1 class="text-base font-semibold text-fg">{view === "overdue" ? "Overdue" : "Everything out"}</h1>
      <p class="text-xs text-fg-muted">
        {view === "overdue"
          ? "Everything still out past its due date, most late first."
          : "Everything checked out right now, soonest due first."}
      </p>
    </div>
    <span class="flex-1"></span>
    <div class="flex rounded-lg border border-line-strong p-0.5" role="group" aria-label="Which items">
      <Button
        size="sm"
        variant={view === "overdue" ? "secondary" : "ghost"}
        aria-pressed={view === "overdue"}
        onclick={() => (view = "overdue")}>Overdue</Button
      >
      <Button
        size="sm"
        variant={view === "out" ? "secondary" : "ghost"}
        aria-pressed={view === "out"}
        onclick={() => (view = "out")}>Everything out</Button
      >
    </div>
    <Button variant="ghost" size="icon-sm" aria-label="Refresh the list" onclick={load}>
      <RefreshIcon aria-hidden="true" />
    </Button>
  </div>

  {#if error}
    <EmptyState title="Couldn't load the list" description={error}>
      {#snippet action()}
        <Button variant="secondary" onclick={load}>Try again</Button>
      {/snippet}
    </EmptyState>
  {:else if loading}
    <div class="flex flex-col gap-px" aria-busy="true" aria-label="Loading">
      {#each Array(4) as _, index (index)}
        <Skeleton class="h-(--row-h) rounded-none" />
      {/each}
    </div>
  {:else if rows.length === 0}
    <EmptyState
      title={view === "overdue" ? "Nothing is overdue" : "Nothing is checked out"}
      description={view === "overdue"
        ? "Every checked-out item is still inside its due date."
        : "Everything is on the shelf."}
    />
  {:else}
    <div class="overflow-hidden rounded-xl border border-line-strong">
      <Table.Root>
        <Table.Header>
          <Table.Row>
            <Table.Head>Custodian</Table.Head>
            <Table.Head>Student number</Table.Head>
            <Table.Head>Item</Table.Head>
            <Table.Head>Serial</Table.Head>
            <Table.Head>Due</Table.Head>
            <Table.Head>Days late</Table.Head>
            <Table.Head class="text-right">Action</Table.Head>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {#each rows as row (row.id)}
            <Table.Row>
              <Table.Cell class="text-fg">{row.custodian_name}</Table.Cell>
              <Table.Cell>
                <Serial value={row.custodian_student_number} label="student number" />
              </Table.Cell>
              <Table.Cell class="text-fg-muted">{row.asset_name}</Table.Cell>
              <Table.Cell><Serial value={row.serial_number ?? row.asset_tag} /></Table.Cell>
              <Table.Cell class="tabular-nums text-fg-muted">{dateTime(row.due_at)}</Table.Cell>
              <Table.Cell
                class="tabular-nums font-semibold {row.overdue ? 'text-status-overdue' : 'text-fg-faint'}"
              >
                {row.overdue ? row.days_overdue : "–"}
              </Table.Cell>
              <Table.Cell class="text-right whitespace-nowrap">
                <Button variant="ghost" size="sm" disabled={busy === row.id} onclick={() => askLost(row)}>
                  Mark lost
                </Button>
                <Button variant="secondary" size="sm" disabled={busy === row.id} onclick={() => checkIn(row)}>
                  {busy === row.id ? "Working…" : "Check in"}
                </Button>
              </Table.Cell>
            </Table.Row>
          {/each}
        </Table.Body>
      </Table.Root>
    </div>
  {/if}
</div>

<Dialog.Root open={lostTarget !== null} onOpenChange={(open) => !open && (lostTarget = null)}>
  <Dialog.Content>
    <form onsubmit={confirmLost} class="flex flex-col gap-3">
      <Dialog.Header>
        <Dialog.Title>Mark {lostTarget?.asset_name ?? "this item"} lost?</Dialog.Title>
        <Dialog.Description>
          The loan to {lostTarget?.custodian_name ?? ""} closes as lost, and the item becomes unavailable.
          {lostTarget?.custodian_name ?? "They"} will no longer be overdue on it. If it turns up, mark it
          available again under Assets.
        </Dialog.Description>
      </Dialog.Header>
      <div class="flex flex-col gap-1.5">
        <Label for="lost-note">What happened (optional)</Label>
        <Input id="lost-note" bind:value={lostNote} maxlength={500} />
      </div>
      {#if lostError}
        <p class="text-status-overdue" role="alert">{lostError}</p>
      {/if}
      <Dialog.Footer>
        <Button type="button" variant="ghost" onclick={() => (lostTarget = null)}>Cancel</Button>
        <Button type="submit" variant="destructive" disabled={busy !== null}>Mark lost</Button>
      </Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>
