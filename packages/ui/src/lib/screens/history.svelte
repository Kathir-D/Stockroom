<script lang="ts">
  /**
   * "My history": the signed-in user's own custody trail.
   *
   * `GET /users/{id}/history` is the one history read a non-admin is allowed,
   * and only for themselves — an admin may read anyone's (CLAUDE.md §7). That
   * rule lives in `internal/stockroom`; this screen just asks for its own id, so
   * there is no path here that depends on the UI enforcing anything.
   *
   * `data-density="compact"`: it is a table an operator scans down, like the
   * admin lists (§4).
   */
  import { Skeleton } from "@stockroom/ui/components/ui/skeleton"
  import * as Table from "@stockroom/ui/components/ui/table"
  import { Button } from "@stockroom/ui/components/ui/button"
  import EmptyState from "@stockroom/ui/components/app/empty-state.svelte"
  import Serial from "@stockroom/ui/components/app/serial.svelte"
  import * as api from "../api/index"
  import type { CustodyRecord } from "../api/types"
  import { dateTime } from "../status"
  import { session } from "../stores/session.svelte"

  let rows = $state<CustodyRecord[]>([])
  let loading = $state(true)
  let error = $state<string | null>(null)

  async function load() {
    const id = session.profile?.id
    if (!id) return
    loading = true
    error = null
    try {
      rows = await api.userHistory(id)
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      loading = false
    }
  }

  $effect(() => {
    void session.profile?.id
    load()
  })

  /** Open, returned, or open-and-late. The word, never the colour alone (§1.3). */
  function rowState(row: CustodyRecord): { label: string; class: string } {
    if (row.checked_in_at) return { label: "Returned", class: "text-fg-muted" }
    if (row.overdue)
      return {
        label: `Overdue ${row.days_overdue} ${row.days_overdue === 1 ? "day" : "days"}`,
        class: "text-status-overdue",
      }
    return { label: "Out", class: "text-status-out" }
  }
</script>

<div data-density="compact" class="flex flex-col gap-3">
  <h1 class="text-lg font-semibold text-fg">My history</h1>

  {#if error}
    <EmptyState title="Couldn't load your history" description={error}>
      {#snippet action()}
        <Button variant="secondary" onclick={load}>Try again</Button>
      {/snippet}
    </EmptyState>
  {:else if loading}
    <div class="flex flex-col gap-px" aria-busy="true" aria-label="Loading history">
      {#each Array(5) as _, index (index)}
        <Skeleton class="h-(--row-h) rounded-none" />
      {/each}
    </div>
  {:else if rows.length === 0}
    <EmptyState
      title="Nothing checked out yet"
      description="Items you borrow show up here, with when they went out and when they came back."
    />
  {:else}
    <div class="overflow-hidden rounded-xl border border-line-strong">
      <Table.Root>
        <Table.Header>
          <Table.Row>
            <Table.Head>Item</Table.Head>
            <Table.Head>Serial</Table.Head>
            <Table.Head>Out</Table.Head>
            <Table.Head>Due</Table.Head>
            <Table.Head>Returned</Table.Head>
            <Table.Head>Status</Table.Head>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {#each rows as row (row.id)}
            {@const s = rowState(row)}
            <Table.Row>
              <Table.Cell class="text-fg">{row.asset_name}</Table.Cell>
              <Table.Cell><Serial value={row.serial_number ?? row.asset_tag} /></Table.Cell>
              <Table.Cell class="tabular-nums text-fg-muted">
                {dateTime(row.checked_out_at)}
              </Table.Cell>
              <Table.Cell class="tabular-nums text-fg-muted">{dateTime(row.due_at)}</Table.Cell>
              <Table.Cell class="tabular-nums text-fg-muted">
                {row.checked_in_at ? dateTime(row.checked_in_at) : "—"}
              </Table.Cell>
              <Table.Cell class={s.class}>{s.label}</Table.Cell>
            </Table.Row>
          {/each}
        </Table.Body>
      </Table.Root>
    </div>
  {/if}
</div>
