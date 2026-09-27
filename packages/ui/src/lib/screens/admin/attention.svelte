<script lang="ts">
  /**
   * Admin → Needs attention (ROADMAP §3.2, §3.3).
   *
   * Returns an admin should look at, newest first:
   *
   *  - **Damage reported.** The item stayed available, as decided on
   *    2026-09-26, so the next student can take it; this is where somebody
   *    decides whether it should be. Until then every screen shows the report.
   *  - **Returned without a scan.** A student typed the serial or pressed a
   *    button, so nothing shows the item actually came back. Worth a look at
   *    the shelf.
   *
   * **Reviewed** clears a row. The reasons stay on the loan, so its history
   * still says why it was listed. Marking the item unavailable, if the damage
   * is real, is a separate press under Assets.
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
  let busy = $state<string | null>(null)

  async function load() {
    loading = true
    error = null
    try {
      rows = await api.needsReview()
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      loading = false
    }
  }

  $effect(() => {
    load()
  })

  const REASONS: Record<string, string> = {
    damage: "Damage reported",
    not_scanned: "Returned without a scan",
  }

  const HOW: Record<string, string> = {
    scan: "scanned",
    typed: "serial typed",
    button: "Check in button",
    kit: "whole-kit return",
  }

  async function resolve(row: CustodyRecord) {
    busy = row.id
    try {
      await api.resolveReview(row.id)
      rows = rows.filter((r) => r.id !== row.id)
      // A cleared damage report disappears from the browse row too.
      void catalog.reload()
      void session.refresh()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      busy = null
    }
  }
</script>

<div data-density="compact" class="flex flex-col gap-3">
  <div class="flex items-center gap-3">
    <div class="flex flex-col">
      <h1 class="text-base font-semibold text-fg">Needs attention</h1>
      <p class="text-xs text-fg-muted">
        Returns with a damage report, and returns no scan backs up. Look, then mark each one reviewed.
      </p>
    </div>
    <span class="flex-1"></span>
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
      {#each Array(3) as _, index (index)}
        <Skeleton class="h-(--row-h) rounded-none" />
      {/each}
    </div>
  {:else if rows.length === 0}
    <EmptyState title="Nothing needs attention" description="Every return has been scanned and nobody reported damage." />
  {:else}
    <div class="overflow-hidden rounded-xl border border-line-strong">
      <Table.Root>
        <Table.Header>
          <Table.Row>
            <Table.Head>Item</Table.Head>
            <Table.Head>Serial</Table.Head>
            <Table.Head>Why</Table.Head>
            <Table.Head>Returned</Table.Head>
            <Table.Head>Borrowed by</Table.Head>
            <Table.Head class="text-right">Action</Table.Head>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {#each rows as row (row.id)}
            <Table.Row>
              <Table.Cell class="text-fg">{row.asset_name}</Table.Cell>
              <Table.Cell><Serial value={row.serial_number ?? row.asset_tag} /></Table.Cell>
              <Table.Cell>
                <ul class="flex flex-col gap-0.5">
                  {#each row.review_reasons ?? [] as reason (reason)}
                    <li class="text-fg">{REASONS[reason] ?? reason}</li>
                  {/each}
                </ul>
                {#if row.condition_in}
                  <p class="text-fg-muted">"{row.condition_in}"</p>
                {/if}
              </Table.Cell>
              <Table.Cell class="text-fg-muted">
                <span class="tabular-nums">{dateTime(row.checked_in_at)}</span>
                {#if row.checked_in_by_name}
                  <span class="block">by {row.checked_in_by_name}{row.returned_via ? `, ${HOW[row.returned_via] ?? row.returned_via}` : ""}</span>
                {/if}
              </Table.Cell>
              <Table.Cell class="text-fg-muted">{row.custodian_name}</Table.Cell>
              <Table.Cell class="text-right">
                <Button variant="secondary" size="sm" disabled={busy === row.id} onclick={() => resolve(row)}>
                  {busy === row.id ? "Saving…" : "Reviewed"}
                </Button>
              </Table.Cell>
            </Table.Row>
          {/each}
        </Table.Body>
      </Table.Root>
    </div>
  {/if}
</div>
