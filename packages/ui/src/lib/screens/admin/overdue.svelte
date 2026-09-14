<script lang="ts">
  /**
   * Admin → Overdue (design-system.md §8.7): "the highest-value admin screen".
   *
   * `ListOverdueCustody` sorted by days-late descending, with **Check in** as the
   * row action — the whole point of the screen is turning a late row back into a
   * shelved item without hunting for it in the browse list.
   *
   * This is the roster of *who has what*, which is admin-only, as distinct from
   * the current holder of a *named* item, which every signed-in user can see
   * (CLAUDE.md §13, 2026-09-12). Both read the same `overdue_custody` view, so
   * "overdue" means one thing here, at sign-in, and inside `CheckOutAssets`.
   */
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw"
  import { toast } from "svelte-sonner"
  import { Button } from "@stockroom/ui/components/ui/button"
  import { Skeleton } from "@stockroom/ui/components/ui/skeleton"
  import * as Table from "@stockroom/ui/components/ui/table"
  import EmptyState from "@stockroom/ui/components/app/empty-state.svelte"
  import Serial from "@stockroom/ui/components/app/serial.svelte"
  import * as api from "../../api/index"
  import type { CustodyRecord } from "../../api/types"
  import { dateTime } from "../../status"
  import { catalog } from "../../stores/catalog.svelte"
  import { session } from "../../stores/session.svelte"

  let rows = $state<CustodyRecord[]>([])
  let loading = $state(true)
  let error = $state<string | null>(null)
  let checkingIn = $state<string | null>(null)

  async function load() {
    loading = true
    error = null
    try {
      rows = await api.overdueCustody()
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      loading = false
    }
  }

  $effect(() => {
    load()
  })

  async function checkIn(row: CustodyRecord) {
    checkingIn = row.id
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
      checkingIn = null
    }
  }
</script>

<div data-density="compact" class="flex flex-col gap-3">
  <div class="flex items-center gap-3">
    <div class="flex flex-col">
      <h1 class="text-base font-semibold text-fg">Overdue</h1>
      <p class="text-xs text-fg-muted">
        Everything still out past its due date, most late first.
      </p>
    </div>
    <span class="flex-1"></span>
    <Button variant="ghost" size="icon-sm" aria-label="Refresh the overdue list" onclick={load}>
      <RefreshIcon aria-hidden="true" />
    </Button>
  </div>

  {#if error}
    <EmptyState title="Couldn't load the overdue list" description={error}>
      {#snippet action()}
        <Button variant="secondary" onclick={load}>Try again</Button>
      {/snippet}
    </EmptyState>
  {:else if loading}
    <div class="flex flex-col gap-px" aria-busy="true" aria-label="Loading overdue items">
      {#each Array(4) as _, index (index)}
        <Skeleton class="h-(--row-h) rounded-none" />
      {/each}
    </div>
  {:else if rows.length === 0}
    <EmptyState
      title="Nothing is overdue"
      description="Every checked-out item is still inside its due date."
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
              <Table.Cell class="tabular-nums font-semibold text-status-overdue">
                {row.days_overdue}
              </Table.Cell>
              <Table.Cell class="text-right">
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={checkingIn === row.id}
                  onclick={() => checkIn(row)}
                >
                  {checkingIn === row.id ? "Checking in…" : "Check in"}
                </Button>
              </Table.Cell>
            </Table.Row>
          {/each}
        </Table.Body>
      </Table.Root>
    </div>
  {/if}
</div>
