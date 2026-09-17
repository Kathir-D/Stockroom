<script lang="ts">
  /**
   * What a restore did, shown after the restore has already signed everybody out.
   *
   * Rendered at the app root rather than on the backup screen, because by the
   * time it has something to say that screen no longer exists: `RestoreFromZip`
   * clears every session as its last act, so the shell has dropped back to
   * sign-in. See `stores/restore.svelte.ts`.
   *
   * **Deliberately not a `<Dialog>`**, which is what it was first. A modal
   * dialog traps focus, and the screen underneath this one is sign-in, whose
   * input reclaims focus on blur so a barcode scanner always has somewhere to
   * type (CLAUDE.md §10). The two fought: the dialog pulled focus in, sign-in
   * pulled it back out, forever — the tab stopped responding and had to be
   * closed. Sign-in now stands down when focus moves somewhere real, and this
   * is the other half of the same fix: a panel that traps nothing cannot start
   * that argument with anything else later either.
   *
   * The scanner is not blocked while this is open. That is correct rather than
   * merely tolerable: the database was just replaced, and someone holding a
   * camera should be able to return it without reading a report first.
   *
   * Deliberately detailed. "Who restored the database, from what, and how many
   * rows landed" is the first thing anybody asks afterwards, and the one moment
   * it is all in hand is right now.
   */
  import DatabaseBackupIcon from "@lucide/svelte/icons/database-backup"
  import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert"
  import XIcon from "@lucide/svelte/icons/x"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Table from "@stockroom/ui/components/ui/table"
  import { dateTime } from "../../status"
  import { restoreReport } from "../../stores/restore.svelte"

  const result = $derived(restoreReport.result)

  /**
   * Escape closes it, the one keyboard affordance a dialog would have given.
   * Bound to the window rather than to the panel, because nothing inside has
   * focus — that is the point of the panel not taking any.
   */
  function onKeydown(event: KeyboardEvent) {
    if (event.key === "Escape" && restoreReport.result) restoreReport.dismiss()
  }
</script>

<svelte:window onkeydown={onKeydown} />

{#if result}
  <!-- `pointer-events-none` on the frame and back on for the card: the page
       behind stays usable, and there is no overlay to click through. -->
  <div
    class="pointer-events-none fixed inset-0 z-50 flex items-start justify-center p-(--gutter) pt-[10vh]"
  >
    <div
      role="status"
      data-density="compact"
      class="pointer-events-auto flex max-h-[80vh] w-full max-w-[560px] flex-col gap-3 overflow-y-auto rounded-xl border border-line-strong bg-surface p-4 shadow-elev-3"
    >
      <div class="flex items-start gap-2">
        <DatabaseBackupIcon
          class="mt-0.5 size-5 shrink-0 text-status-available"
          aria-hidden="true"
        />
        <div class="min-w-0 flex-1">
          <p class="text-sm font-semibold text-fg">Database restored</p>
          <p class="text-xs text-fg-muted">
            {result.rows.toLocaleString()} rows across {result.tables.length} tables and
            {result.sequences}
            {result.sequences === 1 ? "sequence" : "sequences"}, from the backup taken
            {dateTime(result.archive_ran_at)}.
          </p>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Close the restore report"
          onclick={() => restoreReport.dismiss()}
        >
          <XIcon aria-hidden="true" />
        </Button>
      </div>

      <p class="text-sm text-fg-muted">
        Everyone was signed out, including you — the accounts are now the ones that were in the
        backup. Sign in again to carry on.
      </p>

      <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        <dt class="text-fg-faint">Restored at</dt>
        <dd class="text-fg-muted">{dateTime(result.ran_at)}</dd>
        <dt class="text-fg-faint">From</dt>
        <dd class="text-fg-muted">{result.source}</dd>
        <dt class="text-fg-faint">By</dt>
        <dd class="text-fg-muted">{result.by_name || result.by}</dd>
        <dt class="text-fg-faint">Schema</dt>
        <dd class="font-mono text-fg-muted">{result.schema_version || "—"}</dd>
      </dl>

      {#if result.warnings.length > 0}
        <!-- Named rather than hidden: these are the checks that could not be
             run, not checks that passed. -->
        <div class="flex flex-col gap-1 rounded-(--radius-lg) border border-line-strong p-3">
          <p class="flex items-center gap-1.5 text-sm text-fg">
            <AlertTriangleIcon class="size-4 text-status-due-soon" aria-hidden="true" />
            Worth knowing
          </p>
          <ul class="ml-5 list-disc text-xs text-fg-muted">
            {#each result.warnings as warning, index (index)}
              <li>{warning}</li>
            {/each}
          </ul>
        </div>
      {/if}

      <div class="max-h-64 overflow-y-auto rounded-(--radius-lg) border border-line">
        <Table.Root>
          <Table.Header>
            <Table.Row>
              <Table.Head>Table</Table.Head>
              <Table.Head class="text-right">Rows</Table.Head>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {#each result.tables as table (table.table)}
              <Table.Row>
                <Table.Cell class="text-fg">{table.table}</Table.Cell>
                <Table.Cell class="text-right tabular-nums text-fg-muted">
                  {table.rows.toLocaleString()}
                </Table.Cell>
              </Table.Row>
            {/each}
          </Table.Body>
        </Table.Root>
      </div>

      <div class="flex justify-end">
        <Button onclick={() => restoreReport.dismiss()}>Close</Button>
      </div>
    </div>
  </div>
{/if}
