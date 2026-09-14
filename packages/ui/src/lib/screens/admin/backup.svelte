<script lang="ts">
  /**
   * Admin → Backup (design-system.md §8.7): one **Backup Now** button, the
   * destination path, and the result of the last run.
   *
   * It runs the same `ExportAllTablesToCSV` the nightly `cmd/backup` CLI does
   * (CLAUDE.md §11), so this button is a manual trigger of the scheduled job
   * rather than a second export path. It writes the CSVs locally; the `rclone`
   * push to Drive is the CLI's half and does not happen here.
   *
   * An unset `BACKUP_DIR` is a **503** with its message intact, not a 500:
   * neither the client's fault nor a bug, and the admin reading it is the person
   * who edits `.env` (CLAUDE.md §8.1). So it renders as an instruction, not an
   * error.
   */
  import DatabaseBackupIcon from "@lucide/svelte/icons/database-backup"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Table from "@stockroom/ui/components/ui/table"
  import * as api from "../../api/index"
  import type { BackupResult } from "../../api/types"
  import { dateTime } from "../../status"

  let running = $state(false)
  let result = $state<BackupResult | null>(null)
  let error = $state<string | null>(null)
  let notConfigured = $state(false)

  async function run() {
    running = true
    error = null
    notConfigured = false
    try {
      result = await api.backupNow()
    } catch (err) {
      if (err instanceof api.ApiError && err.isNotConfigured) {
        notConfigured = true
        error = err.message
      } else {
        error = err instanceof Error ? err.message : String(err)
      }
    } finally {
      running = false
    }
  }
</script>

<div data-density="compact" class="flex max-w-2xl flex-col gap-4">
  <div class="flex flex-col">
    <h1 class="text-base font-semibold text-fg">Backup</h1>
    <p class="text-xs text-fg-muted">
      Writes every table to CSV under <code class="font-mono">BACKUP_DIR/&lt;yyyy-mm-dd&gt;/</code>.
      The nightly job runs the same export and then pushes the folder to Google Drive with
      <code class="font-mono">rclone</code>.
    </p>
  </div>

  <div>
    <Button disabled={running} onclick={run}>
      <DatabaseBackupIcon aria-hidden="true" />
      {running ? "Exporting…" : "Backup now"}
    </Button>
  </div>

  {#if notConfigured}
    <div class="rounded-(--radius-lg) border border-line-strong p-3">
      <p class="text-sm text-fg">Backups aren't configured yet.</p>
      <p class="mt-1 text-xs text-fg-muted">
        {error} — set <code class="font-mono">BACKUP_DIR</code> in the repo's
        <code class="font-mono">.env</code> and restart the server.
      </p>
    </div>
  {:else if error}
    <p class="text-status-overdue" role="alert">{error}</p>
  {/if}

  {#if result}
    <div class="flex flex-col gap-2 rounded-(--radius-lg) border border-line-strong p-3">
      <p class="text-sm text-fg">
        {result.rows.toLocaleString()} rows across {result.tables.length} tables
      </p>
      <p class="text-xs text-fg-muted">
        Written to <code class="font-mono">{result.dir}</code> at {dateTime(result.ran_at)}
      </p>
      <div class="max-h-80 overflow-y-auto">
        <Table.Root>
          <Table.Header>
            <Table.Row>
              <Table.Head>Table</Table.Head>
              <Table.Head>File</Table.Head>
              <Table.Head class="text-right">Rows</Table.Head>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {#each result.tables as table (table.table)}
              <Table.Row>
                <Table.Cell class="text-fg">{table.table}</Table.Cell>
                <Table.Cell class="font-mono text-fg-muted">{table.file}</Table.Cell>
                <Table.Cell class="text-right tabular-nums text-fg-muted">{table.rows}</Table.Cell>
              </Table.Row>
            {/each}
          </Table.Body>
        </Table.Root>
      </div>
    </div>
  {/if}
</div>
