<script lang="ts">
  /**
   * Admin → Backup (docs/design/backup.md §E.9).
   *
   * Six things, in the order somebody actually needs them: what is wrong, when
   * it last worked, a button to run one now, how to get the data back, the
   * photo mirror, and the log.
   *
   * **The warnings come from the server already worded.** `BackupStatus` builds
   * each sentence — stale, a target that failed, no failsafe admin, the photo
   * mirror running out of room — so the same fact reads the same way here, at a
   * student's sign-in, and in `backup.log`. This screen decides how to render
   * them, never what they say.
   *
   * **Restore is the one action in the app that asks for a word to be typed**,
   * and the server checks that word too: it is a rule in `internal/stockroom`,
   * not a courtesy in the UI, which is why `cmd/restore` is held to it as well.
   * Restoring also drops every session as its last act, so the result is handed
   * to `restoreReport` and rendered at the app root — see
   * `components/app/restore-report.svelte` for why it cannot live on this
   * screen.
   *
   * **Deleting a photo generation is the only deletion the mirror has**, and it
   * is a person pressing a button. Nothing is ever purged automatically:
   * auto-purging the only copy of a deleted photo defeats the mirror.
   */
  import DatabaseBackupIcon from "@lucide/svelte/icons/database-backup"
  import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert"
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw"
  import UploadIcon from "@lucide/svelte/icons/upload"
  import HistoryIcon from "@lucide/svelte/icons/history"
  import ImageIcon from "@lucide/svelte/icons/image"
  import TrashIcon from "@lucide/svelte/icons/trash-2"
  import FileTextIcon from "@lucide/svelte/icons/file-text"
  import { toast } from "svelte-sonner"
  import * as AlertDialog from "@stockroom/ui/components/ui/alert-dialog"
  import { Button } from "@stockroom/ui/components/ui/button"
  import { Checkbox } from "@stockroom/ui/components/ui/checkbox"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import { Skeleton } from "@stockroom/ui/components/ui/skeleton"
  import * as Table from "@stockroom/ui/components/ui/table"
  import EmptyState from "@stockroom/ui/components/app/empty-state.svelte"
  import PasswordInput from "@stockroom/ui/components/app/password-input.svelte"
  import * as api from "../../api/index"
  import type {
    BackupResult,
    BackupStatusResult,
    BackupVersion,
    PhotoGeneration,
  } from "../../api/types"
  import { ageWords, dateTime, formatBytes } from "../../status"
  import { router } from "../../stores/router.svelte"
  import { restoreReport } from "../../stores/restore.svelte"
  import { session } from "../../stores/session.svelte"

  type TargetName = "local" | "drive" | "github"

  const TARGET_LABEL: Record<string, string> = {
    local: "This machine",
    drive: "Google Drive",
    github: "GitHub",
  }

  let status = $state<BackupStatusResult | null>(null)
  let loading = $state(true)
  let loadError = $state<string | null>(null)
  let notConfigured = $state(false)

  let running = $state(false)
  let runResult = $state<BackupResult | null>(null)
  let runError = $state<string | null>(null)

  async function load() {
    loading = true
    loadError = null
    notConfigured = false
    try {
      status = await api.backupStatus()
    } catch (err) {
      if (err instanceof api.ApiError && err.isNotConfigured) notConfigured = true
      loadError = err instanceof Error ? err.message : String(err)
    } finally {
      loading = false
    }
  }

  $effect(() => {
    load()
  })

  async function runBackup() {
    running = true
    runError = null
    runResult = null
    try {
      const result = await api.backupNow()
      runResult = result
      if (result.skipped) {
        // Another process holds the advisory lock. That is a skip, not a
        // failure, and saying "backed up" would be a lie about what happened.
        toast.info("A backup was already running, so this one did nothing.")
      } else {
        const failed = result.targets.filter((t) => !t.ok)
        if (failed.length === 0) toast.success("Backup complete")
        else
          toast.warning(
            `Saved on this machine, but ${failed.map((t) => TARGET_LABEL[t.target] ?? t.target).join(" and ")} failed.`
          )
      }
      // Reload rather than patch: the run just rewrote the state file, the log
      // and the photo generations, and this is one cheap read.
      await load()
      await session.refresh()
    } catch (err) {
      if (err instanceof api.ApiError && err.isNotConfigured) notConfigured = true
      runError = err instanceof Error ? err.message : String(err)
    } finally {
      running = false
    }
  }

  /* ---------------------------------------------------------- restore ---- */

  /** Where a pending restore's bytes come from, and how it is described. */
  type RestoreSource =
    | { kind: "upload"; file: File }
    | { kind: "target"; target: TargetName; version: BackupVersion }

  let pending = $state<RestoreSource | null>(null)
  let confirmWord = $state("")
  let restorePassphrase = $state("")
  let restoreForce = $state(false)
  let restoring = $state(false)
  let restoreError = $state<string | null>(null)

  let uploadFiles = $state<FileList | undefined>(undefined)

  let versionsOpen = $state(false)
  let versionsTarget = $state<TargetName>("local")
  let versions = $state<BackupVersion[]>([])
  let versionsLoading = $state(false)
  let versionsError = $state<string | null>(null)

  const pendingLabel = $derived(
    pending === null
      ? ""
      : pending.kind === "upload"
        ? pending.file.name
        : `${TARGET_LABEL[pending.target] ?? pending.target} — ${pending.version.label}`
  )

  function startUploadRestore() {
    const file = uploadFiles?.[0]
    if (!file) return
    openConfirm({ kind: "upload", file })
  }

  function openConfirm(source: RestoreSource) {
    pending = source
    confirmWord = ""
    restorePassphrase = ""
    restoreForce = false
    restoreError = null
  }

  async function openVersions(target: TargetName) {
    versionsTarget = target
    versionsOpen = true
    versionsLoading = true
    versionsError = null
    versions = []
    try {
      versions = await api.backupVersions(target)
    } catch (err) {
      versionsError = err instanceof Error ? err.message : String(err)
    } finally {
      versionsLoading = false
    }
  }

  async function confirmRestore() {
    if (!pending || confirmWord !== api.RESTORE_CONFIRMATION) return
    restoring = true
    restoreError = null
    const options = {
      confirm: confirmWord,
      passphrase: restorePassphrase || undefined,
      force: restoreForce,
    }
    try {
      const result =
        pending.kind === "upload"
          ? await api.restoreFromFile(pending.file, options)
          : await api.restoreFromTarget(pending.target, pending.version.id, options)

      // Hand the result somewhere that outlives this screen *before* signing
      // out, then sign out deliberately rather than waiting for the next
      // request to come back 401. The server has already dropped every
      // session; making that visible on our own terms is the difference
      // between "restored, here is what landed" and the app silently returning
      // to sign-in.
      restoreReport.show(result)
      pending = null
      versionsOpen = false
      session.clear()
      router.go({ name: "browse" })
    } catch (err) {
      // Every failure here left the database untouched: the checks that can
      // fail run inside the transaction, before the commit. Say so, because
      // "restore failed" on its own reads as "the database is now half a
      // backup".
      restoreError = err instanceof Error ? err.message : String(err)
    } finally {
      restoring = false
    }
  }

  /* ----------------------------------------------------------- photos ---- */

  let photoBusy = $state<string | null>(null)
  let deleteTarget = $state<PhotoGeneration | null>(null)
  let photoError = $state<string | null>(null)

  async function restorePhotoGeneration(generation: PhotoGeneration) {
    photoBusy = generation.name
    photoError = null
    try {
      const result = await api.restorePhotos(generation.name)
      toast.success(`${result.restored.toLocaleString()} photos copied back`)
      await load()
    } catch (err) {
      photoError = err instanceof Error ? err.message : String(err)
    } finally {
      photoBusy = null
    }
  }

  async function confirmDeleteGeneration() {
    if (!deleteTarget) return
    photoBusy = deleteTarget.name
    photoError = null
    try {
      await api.deletePhotoGeneration(deleteTarget.name)
      toast.success(`Deleted ${deleteTarget.name}`)
      deleteTarget = null
      await load()
    } catch (err) {
      photoError = err instanceof Error ? err.message : String(err)
    } finally {
      photoBusy = null
    }
  }

  /** Newest first: the log is appended to, and the last line is the interesting one. */
  const logLines = $derived([...(status?.log ?? [])].reverse())
</script>

<div data-density="compact" class="flex max-w-3xl flex-col gap-4">
  <div class="flex items-center gap-3">
    <div class="flex flex-col">
      <h1 class="text-base font-semibold text-fg">Backup</h1>
      <p class="text-xs text-fg-muted">
        {#if status?.configured}
          Every table, the sequences and a readable inventory, written to
          <code class="font-mono">{status.dir}</code> and pushed to whichever targets are switched
          on. {status.schedule}.
        {:else}
          Where backups go is set on the Settings screen.
        {/if}
      </p>
    </div>
    <span class="flex-1"></span>
    <Button variant="ghost" size="icon-sm" aria-label="Reload backup status" onclick={load}>
      <RefreshIcon aria-hidden="true" />
    </Button>
  </div>

  <!--
    A load that failed and a backup that was never set up are different
    answers, and this used to give both the same one: any error left status
    null, so a server that was down reported "Backups aren't configured yet"
    with the network error printed underneath as if it were advice. The admin
    is then sent to a Settings screen where everything is already filled in.
    notConfigured is the 503 the server sends for that exact case, so it is
    what the wording follows.
  -->
  {#if notConfigured || (!loading && !status)}
    <EmptyState
      title={notConfigured ? "Backups aren't configured yet" : "Couldn't load the backup status"}
      description={notConfigured
        ? "Nothing is being backed up. Choose a backup folder on the Settings screen — no file editing needed."
        : (loadError ??
          "The server didn't answer. It may still be starting up, or it may have stopped.")}
    >
      {#snippet action()}
        {#if notConfigured}
          <Button onclick={() => router.go({ name: "admin", tab: "settings" })}>
            Open Settings
          </Button>
          <Button variant="secondary" onclick={load}>Try again</Button>
        {:else}
          <Button onclick={load}>Try again</Button>
          <Button variant="secondary" onclick={() => router.go({ name: "admin", tab: "settings" })}>
            Open Settings
          </Button>
        {/if}
      {/snippet}
    </EmptyState>
  {:else if loading && !status}
    <div class="flex flex-col gap-3" aria-busy="true" aria-label="Loading backup status">
      {#each Array(3) as _, index (index)}
        <Skeleton class="h-32 rounded-xl" />
      {/each}
    </div>
  {:else if status}
    <!-- ----------------------------------------------------- warnings ---- -->
    {#if status.warnings.length > 0}
      <div
        role="status"
        class="flex items-start gap-3 rounded-xl border border-line-strong bg-surface p-3"
      >
        <AlertTriangleIcon
          class="mt-0.5 size-4 shrink-0 text-status-due-soon"
          aria-hidden="true"
        />
        <ul class="flex min-w-0 flex-1 flex-col gap-1">
          {#each status.warnings as warning, index (index)}
            <li class="text-sm text-fg">{warning}</li>
          {/each}
        </ul>
      </div>
    {:else}
      <p class="text-sm text-status-available">Backups are up to date.</p>
    {/if}

    <!-- --------------------------------------------------- run + state ---- -->
    <section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
      <div class="flex flex-wrap items-center gap-3">
        <Button disabled={running} onclick={runBackup}>
          <DatabaseBackupIcon aria-hidden="true" />
          {running ? "Backing up…" : "Back up now"}
        </Button>
        <p class="text-xs text-fg-muted">
          {#if status.last_run_at}
            Last run {dateTime(status.last_run_at)}
            {#if status.last_run_source}({status.last_run_source}){/if}
          {:else}
            Nothing has run on this machine yet.
          {/if}
        </p>
      </div>

      {#if runError}
        <p class="text-sm text-status-overdue" role="alert">{runError}</p>
      {/if}

      {#if runResult && !runResult.skipped}
        <p class="text-xs text-fg-muted">
          {runResult.rows.toLocaleString()} rows across {runResult.tables.length} tables →
          <code class="font-mono">{runResult.archive}</code>{runResult.encrypted
            ? ", encrypted"
            : ""}{#if runResult.photos?.configured}, {runResult.photos.copied.toLocaleString()} photos
            mirrored{/if}.
        </p>
      {/if}

      <div class="overflow-hidden rounded-(--radius-lg) border border-line">
        <Table.Root>
          <Table.Header>
            <Table.Row>
              <Table.Head>Target</Table.Head>
              <Table.Head>Last success</Table.Head>
              <Table.Head>Where</Table.Head>
              <Table.Head class="text-right">Restore</Table.Head>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {#each status.targets as target (target.target)}
              <Table.Row>
                <Table.Cell class="text-fg">
                  {TARGET_LABEL[target.target] ?? target.target}
                  {#if !target.enabled}
                    <span class="ml-1 text-xs text-fg-faint">off</span>
                  {/if}
                </Table.Cell>
                <Table.Cell class="tabular-nums text-fg-muted">
                  {#if target.last_success}
                    {ageWords(target.age_hours)}
                    <span class="block text-xs text-fg-faint">{dateTime(target.last_success)}</span>
                  {:else if target.enabled}
                    <span class="text-status-overdue">never</span>
                  {:else}
                    —
                  {/if}
                  {#if target.last_error}
                    <!-- The error stays visible after a later success is
                         impossible: the state file clears it on success, so a
                         message here is the *current* state, not history. -->
                    <span class="block text-xs text-status-overdue">{target.last_error}</span>
                  {/if}
                </Table.Cell>
                <Table.Cell class="max-w-[16rem] truncate font-mono text-xs text-fg-muted">
                  {target.ref || "—"}
                </Table.Cell>
                <Table.Cell class="text-right">
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={!target.enabled}
                    onclick={() => openVersions(target.target as TargetName)}
                  >
                    <HistoryIcon aria-hidden="true" />
                    Pick a date
                  </Button>
                </Table.Cell>
              </Table.Row>
            {/each}
          </Table.Body>
        </Table.Root>
      </div>
    </section>

    <!-- ------------------------------------------------ restore upload ---- -->
    <section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
      <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
        <UploadIcon class="size-4 text-fg-muted" aria-hidden="true" />
        Restore from a file
      </h2>
      <p class="text-xs text-fg-muted">
        A <code class="font-mono">stockroom-backup-*.zip</code> written by this app. Restoring
        replaces every record in the database and signs everyone out — it is checked against the
        archive's own checksums first, and nothing is written if a check fails.
      </p>

      <div class="flex flex-wrap items-center gap-2">
        <Input
          type="file"
          accept=".zip,application/zip"
          bind:files={uploadFiles}
          aria-label="Backup archive to restore"
          class="max-w-sm"
        />
        <Button
          variant="secondary"
          disabled={!uploadFiles || uploadFiles.length === 0}
          onclick={startUploadRestore}
        >
          Restore this file
        </Button>
      </div>
    </section>

    <!-- ------------------------------------------------------- photos ---- -->
    {#if status.photo_mirror?.configured}
      {@const mirror = status.photo_mirror}
      <section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
        <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
          <ImageIcon class="size-4 text-fg-muted" aria-hidden="true" />
          Photo mirror
        </h2>
        <p class="text-xs text-fg-muted">
          {formatBytes(mirror.bytes)} in {mirror.generations.length}
          {mirror.generations.length === 1 ? "generation" : "generations"} under
          <code class="font-mono">{mirror.dir}</code>, {formatBytes(mirror.free_bytes)} free on that
          disk. Photos are mirrored on this machine only — a failed drive loses both copies.
        </p>

        {#if photoError}
          <p class="text-sm text-status-overdue" role="alert">{photoError}</p>
        {/if}

        {#if mirror.generations.length === 0}
          <p class="text-sm text-fg-muted">Nothing mirrored yet. The next backup writes one.</p>
        {:else}
          <div class="overflow-hidden rounded-(--radius-lg) border border-line">
            <Table.Root>
              <Table.Header>
                <Table.Row>
                  <Table.Head>Generation</Table.Head>
                  <Table.Head class="text-right">Files</Table.Head>
                  <Table.Head class="text-right">Size</Table.Head>
                  <Table.Head class="text-right">Actions</Table.Head>
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {#each mirror.generations as generation (generation.name)}
                  <Table.Row>
                    <Table.Cell class="text-fg">
                      <span class="font-mono text-xs">{generation.name}</span>
                      {#if generation.live}
                        <span class="ml-1 text-xs text-status-available">live</span>
                      {/if}
                      <span class="block text-xs text-fg-faint">{dateTime(generation.at)}</span>
                    </Table.Cell>
                    <Table.Cell class="text-right tabular-nums text-fg-muted">
                      {generation.files.toLocaleString()}
                    </Table.Cell>
                    <Table.Cell class="text-right tabular-nums text-fg-muted">
                      {formatBytes(generation.bytes)}
                    </Table.Cell>
                    <Table.Cell class="text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={photoBusy === generation.name}
                        onclick={() => restorePhotoGeneration(generation)}
                      >
                        {photoBusy === generation.name ? "Working…" : "Copy back"}
                      </Button>
                      <!-- The live generation is what the next run writes into,
                           so deleting it would delete the mirror itself. -->
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`Delete generation ${generation.name}`}
                        disabled={generation.live || photoBusy === generation.name}
                        onclick={() => (deleteTarget = generation)}
                      >
                        <TrashIcon aria-hidden="true" />
                      </Button>
                    </Table.Cell>
                  </Table.Row>
                {/each}
              </Table.Body>
            </Table.Root>
          </div>
        {/if}
      </section>
    {/if}

    <!-- ---------------------------------------------------------- log ---- -->
    {#if logLines.length > 0}
      <section class="flex flex-col gap-2 rounded-xl border border-line-strong bg-surface p-4">
        <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
          <FileTextIcon class="size-4 text-fg-muted" aria-hidden="true" />
          Recent runs
        </h2>
        <pre
          class="max-h-64 overflow-auto rounded-(--radius) bg-ground p-3 font-mono text-xs leading-relaxed text-fg-muted">{logLines.join(
            "\n"
          )}</pre>
      </section>
    {/if}
  {/if}
</div>

<!-- The by-date picker. `id` is opaque: a path on Drive, a commit on GitHub, a
     file locally, and the UI never has to know which. -->
<Dialog.Root bind:open={versionsOpen}>
  <Dialog.Content>
    <Dialog.Header>
      <Dialog.Title>Restore from {TARGET_LABEL[versionsTarget] ?? versionsTarget}</Dialog.Title>
      <Dialog.Description>
        Pick the backup to restore. The most recent is first.
      </Dialog.Description>
    </Dialog.Header>

    {#if versionsLoading}
      <div class="flex flex-col gap-2" aria-busy="true">
        {#each Array(3) as _, index (index)}
          <Skeleton class="h-(--row-h)" />
        {/each}
      </div>
    {:else if versionsError}
      <p class="text-sm text-status-overdue" role="alert">{versionsError}</p>
    {:else if versions.length === 0}
      <p class="text-sm text-fg-muted">No backups found there yet.</p>
    {:else}
      <ul class="max-h-80 overflow-y-auto">
        {#each versions as version (version.id)}
          <li class="flex items-center justify-between gap-3 border-b border-line py-2 last:border-b-0">
            <span class="flex min-w-0 flex-col">
              <span class="truncate text-sm text-fg">{version.label}</span>
              <span class="text-xs text-fg-faint">
                {dateTime(version.at)}
                {#if version.bytes > 0}· {formatBytes(version.bytes)}{/if}
                {#if version.note}· {version.note}{/if}
              </span>
            </span>
            <Button
              variant="secondary"
              size="sm"
              onclick={() => openConfirm({ kind: "target", target: versionsTarget, version })}
            >
              Restore
            </Button>
          </li>
        {/each}
      </ul>
    {/if}

    <Dialog.Footer>
      <Button variant="ghost" onclick={() => (versionsOpen = false)}>Close</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<!-- The typed confirmation. The word is checked by the server too; this dialog
     is what makes it hard to do by accident, not what makes it a rule. -->
<Dialog.Root
  open={pending !== null}
  onOpenChange={(open) => {
    if (!open && !restoring) pending = null
  }}
>
  <Dialog.Content>
    <Dialog.Header>
      <Dialog.Title class="flex items-center gap-2 text-status-overdue">
        <AlertTriangleIcon class="size-5" aria-hidden="true" />
        Restore the whole database?
      </Dialog.Title>
      <Dialog.Description>
        Every asset, account, category and custody record is replaced by what is in
        <span class="text-fg">{pendingLabel}</span>. Everyone, including you, is signed out. This
        cannot be undone.
      </Dialog.Description>
    </Dialog.Header>

    <div class="flex flex-col gap-3">
      <div class="flex flex-col gap-1">
        <Label for="restore-confirm">
          Type <span class="font-mono text-fg">{api.RESTORE_CONFIRMATION}</span> to confirm
        </Label>
        <Input
          id="restore-confirm"
          bind:value={confirmWord}
          autocomplete="off"
          spellcheck={false}
          disabled={restoring}
        />
      </div>

      <div class="flex flex-col gap-1">
        <Label for="restore-passphrase">Passphrase (only if the archive is encrypted)</Label>
        <PasswordInput
          id="restore-passphrase"
          bind:value={restorePassphrase}
          autocomplete="off"
          disabled={restoring}
          class="text-sm"
        />
        <p class="text-xs text-fg-faint">
          Leave blank to use the passphrase saved in Settings, which is right whenever this machine
          wrote the archive.
        </p>
      </div>

      <label class="flex items-start gap-2 text-sm text-fg">
        <Checkbox
          checked={restoreForce}
          disabled={restoring}
          onCheckedChange={(v) => (restoreForce = v === true)}
        />
        <span>
          Restore even if the archive was made on an older version of the database
          <span class="block text-xs text-fg-faint">
            Only tick this if the restore refused and named a schema mismatch.
          </span>
        </span>
      </label>

      {#if restoreError}
        <!-- Worth saying plainly: every check that can fail runs before the
             commit, so a failure here changed nothing. -->
        <div class="rounded-(--radius-lg) border border-line-strong p-3">
          <p class="text-sm text-status-overdue" role="alert">{restoreError}</p>
          <p class="mt-1 text-xs text-fg-muted">
            Nothing was changed — the database is exactly as it was.
          </p>
        </div>
      {/if}
    </div>

    <Dialog.Footer>
      <Button variant="ghost" disabled={restoring} onclick={() => (pending = null)}>Cancel</Button>
      <Button
        variant="destructive"
        disabled={restoring || confirmWord !== api.RESTORE_CONFIRMATION}
        onclick={confirmRestore}
      >
        {restoring ? "Restoring…" : "Restore"}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<AlertDialog.Root
  open={deleteTarget !== null}
  onOpenChange={(open) => {
    if (!open) deleteTarget = null
  }}
>
  <AlertDialog.Content>
    <AlertDialog.Header>
      <AlertDialog.Title>Delete {deleteTarget?.name}?</AlertDialog.Title>
      <AlertDialog.Description>
        {formatBytes(deleteTarget?.bytes)} across {deleteTarget?.files.toLocaleString()} photos. If a
        photo was deleted from the app after this generation was frozen, this is the only copy left
        of it.
      </AlertDialog.Description>
    </AlertDialog.Header>
    <AlertDialog.Footer>
      <AlertDialog.Cancel>Keep it</AlertDialog.Cancel>
      <AlertDialog.Action onclick={confirmDeleteGeneration}>Delete</AlertDialog.Action>
    </AlertDialog.Footer>
  </AlertDialog.Content>
</AlertDialog.Root>
