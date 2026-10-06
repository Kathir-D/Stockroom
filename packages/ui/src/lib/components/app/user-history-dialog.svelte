<script lang="ts">
  /**
   * One person's custody trail, in a dialog.
   *
   * **This is the admin panel's Users → History popup, extracted** (2026-09-16).
   * It was already the established shape for "show me this person's trail", and
   * a second, differently-shaped answer to the same question was the drift this
   * codebase keeps avoiding by having one component serve every caller — the
   * same reason `<UnitRow>` serves both the browse list and the admin asset
   * table. `screens/admin/users.svelte` now renders this rather than its own
   * copy, so the popup an admin gets is literally the same one wherever they
   * ask for it.
   *
   * Three callers, all admin-only in practice:
   *  - the status of a held row in the browse list and the admin asset table
   *  - **Held by** inside `<AssetDetailDialog>`, where it stacks over the item
   *  - the History button on a row of the admin user table
   *
   * It reads `GET /users/{id}/history`, which the package allows for the actor's
   * own id or for any admin (CLAUDE.md §7). The permission is therefore a server
   * rule; this component only avoids making a request it knows would be refused.
   *
   * An admin gets two actions on a row that is still out. **Remove** takes the
   * item off the person and puts it back in the inventory; **Lost** closes the
   * checkout as lost and makes the item unavailable (CLAUDE.md §7). Lost asks
   * once in the row first, because the item leaves the shelf.
   */
  import { toast } from "svelte-sonner"
  import { Button } from "@stockroom/ui/components/ui/button"
  import { Skeleton } from "@stockroom/ui/components/ui/skeleton"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import * as Table from "@stockroom/ui/components/ui/table"
  import * as api from "../../api/index"
  import type { AssetDetail, CustodyRecord } from "../../api/types"
  import { dateTime } from "../../status"
  import { catalog } from "../../stores/catalog.svelte"
  import { session } from "../../stores/session.svelte"

  let {
    /** Whose history. Null means there is nothing to load. */
    userId = null,
    /** Their display name, unabbreviated — there is room for it in a title. */
    userName = "",
    open = $bindable(false),
    /** An admin removed an item or marked it lost: the asset as it now stands. */
    onChanged,
  }: {
    userId?: string | null
    userName?: string
    open?: boolean
    onChanged?: (asset: AssetDetail) => void
  } = $props()

  let rows = $state<CustodyRecord[]>([])
  let loading = $state(false)
  let error = $state<string | null>(null)
  /** The row an action is running on, and the row Lost is asking about. */
  let busy = $state<string | null>(null)
  let confirmingLost = $state<string | null>(null)

  /**
   * Load on open, and again if the dialog is retargeted at somebody else while
   * it is up. `cancelled` is what stops a slow first response from landing on
   * top of a fast second one and showing the wrong person's rows under the right
   * person's name.
   *
   * A closing dialog keeps whatever it was showing, deliberately: clearing on
   * `open === false` emptied it *before* the exit animation finished, so every
   * close ended on a one-frame "Nothing checked out yet" — which reads as a fact
   * about the person rather than as a teardown. The skeleton covers the stale
   * window on the way back in instead. The users panel had the same gap from the
   * other direction, fetching after it opened with no loading state at all.
   */
  $effect(() => {
    const id = userId
    if (!open || !id) return
    let cancelled = false
    loading = true
    error = null
    confirmingLost = null
    api
      .userHistory(id)
      .then((result) => {
        if (!cancelled) rows = result
      })
      .catch((err) => {
        if (!cancelled) error = err instanceof Error ? err.message : String(err)
      })
      .finally(() => {
        if (!cancelled) loading = false
      })
    return () => {
      cancelled = true
    }
  })

  /**
   * Run one row action, then re-read the trail in place. No skeleton on the way:
   * the dialog is already showing the rows, and only one of them changes.
   */
  async function act(row: CustodyRecord, run: () => Promise<AssetDetail>, done: (asset: AssetDetail) => string) {
    const id = userId
    busy = row.id
    try {
      const asset = await run()
      toast.success(done(asset))
      confirmingLost = null
      // Keep the browse list and the signed-in admin's own overdue flag honest
      // without a reload, as the Out screen does.
      catalog.patchUnit(asset)
      onChanged?.(asset)
      const [fresh] = await Promise.all([id ? api.userHistory(id) : rows, session.refresh()])
      if (id === userId) rows = fresh
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      busy = null
    }
  }

  function remove(row: CustodyRecord) {
    return act(
      row,
      async () => (await api.readdAsset(row.asset_id)).asset,
      (asset) => `${asset.name} is back in the inventory`
    )
  }

  function markLost(row: CustodyRecord) {
    return act(
      row,
      () => api.markLost(row.asset_id),
      (asset) => `${asset.name} marked lost`
    )
  }
</script>

<Dialog.Root bind:open>
  <!-- Wide enough for five columns and the row actions, and never wider than
       the window. The cells below may wrap, so a narrow window makes the rows
       taller instead of scrolling the table sideways. -->
  <Dialog.Content class="max-w-[min(56rem,calc(100%_-_2rem))]">
    <Dialog.Header>
      <Dialog.Title>{userName} — custody history</Dialog.Title>
    </Dialog.Header>
    {#if error}
      <p class="text-status-overdue" role="alert">{error}</p>
    {:else if loading}
      <div class="flex flex-col gap-px" aria-busy="true" aria-label="Loading history">
        {#each Array(4) as _, index (index)}
          <Skeleton class="h-(--row-h) rounded-none" />
        {/each}
      </div>
    {:else if rows.length === 0}
      <p class="text-fg-muted">Nothing checked out yet.</p>
    {:else}
      <div class="max-h-[60vh] overflow-x-hidden overflow-y-auto">
        <Table.Root>
          <Table.Header>
            <Table.Row>
              <Table.Head>Item</Table.Head>
              <Table.Head>Out</Table.Head>
              <Table.Head>Due</Table.Head>
              <Table.Head>Returned</Table.Head>
              {#if session.isAdmin}
                <Table.Head class="text-right"><span class="sr-only">Action</span></Table.Head>
              {/if}
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {#each rows as row (row.id)}
              <Table.Row>
                <Table.Cell class="py-1 break-words whitespace-normal text-fg">{row.asset_name}</Table.Cell>
                <Table.Cell class="py-1 whitespace-normal tabular-nums text-fg-muted">
                  {dateTime(row.checked_out_at)}
                </Table.Cell>
                <Table.Cell class="py-1 whitespace-normal tabular-nums text-fg-muted">{dateTime(row.due_at)}</Table.Cell>
                <Table.Cell
                  class="py-1 whitespace-normal {row.checked_in_at
                    ? 'tabular-nums text-fg-muted'
                    : row.overdue
                      ? 'text-status-overdue'
                      : 'text-status-out'}"
                >
                  {row.outcome === "lost" ? "Lost" : row.checked_in_at ? dateTime(row.checked_in_at) : row.overdue ? "Overdue" : "Out"}
                </Table.Cell>
                {#if session.isAdmin}
                  <Table.Cell class="py-1 text-right whitespace-normal">
                    {#if !row.checked_in_at}
                      <div class="flex flex-wrap items-center justify-end gap-1">
                      {#if confirmingLost === row.id}
                        <span class="text-xs text-fg-muted">Mark lost?</span>
                        <Button variant="ghost" size="xs" disabled={busy !== null} onclick={() => (confirmingLost = null)}>
                          Cancel
                        </Button>
                        <Button variant="destructive" size="xs" disabled={busy !== null} onclick={() => markLost(row)}>
                          {busy === row.id ? "Working…" : "Confirm"}
                        </Button>
                      {:else}
                        <Button variant="secondary" size="xs" disabled={busy !== null} onclick={() => remove(row)}>
                          {busy === row.id ? "Working…" : "Remove"}
                        </Button>
                        <Button
                          variant="destructive"
                          size="xs"
                          disabled={busy !== null}
                          onclick={() => (confirmingLost = row.id)}
                        >
                          Lost
                        </Button>
                      {/if}
                      </div>
                    {/if}
                  </Table.Cell>
                {/if}
              </Table.Row>
            {/each}
          </Table.Body>
        </Table.Root>
      </div>
    {/if}
  </Dialog.Content>
</Dialog.Root>
