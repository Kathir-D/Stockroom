<script lang="ts">
  /**
   * Admin → Users (design-system.md §8.7).
   *
   * Table of name, student number, admin flag and overdue count, with edit, set
   * password, view history and delete per row. **Name leads**, here and in the
   * create/edit form: an admin looking someone up knows who they are, not what
   * their number is — the number is how the scanner addresses them.
   *
   * **Roster CSV import gets its own dialog** and is never fire-and-forget: the
   * file is parsed by the server, which returns a per-row result, and that list
   * is what the dialog shows afterwards. An import that silently half-applied is
   * the failure this screen exists to prevent.
   *
   * No password field on create. A new account has `password_hash = null` and
   * sets one at its first *scan* login; the admin can set one here afterwards
   * (CLAUDE.md §7).
   */
  import PlusIcon from "@lucide/svelte/icons/plus"
  import PencilIcon from "@lucide/svelte/icons/pencil"
  import KeyIcon from "@lucide/svelte/icons/key-round"
  import ClockIcon from "@lucide/svelte/icons/history"
  import TrashIcon from "@lucide/svelte/icons/trash-2"
  import UploadIcon from "@lucide/svelte/icons/upload"
  import { toast } from "svelte-sonner"
  import * as AlertDialog from "@stockroom/ui/components/ui/alert-dialog"
  import { Button } from "@stockroom/ui/components/ui/button"
  import { Checkbox } from "@stockroom/ui/components/ui/checkbox"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import * as Table from "@stockroom/ui/components/ui/table"
  import * as Tooltip from "@stockroom/ui/components/ui/tooltip"
  import EmptyState from "@stockroom/ui/components/app/empty-state.svelte"
  import Serial from "@stockroom/ui/components/app/serial.svelte"
  import * as api from "../../api/index"
  import type { CustodyRecord, Profile, RosterResult, UserInput } from "../../api/types"
  import { dateTime } from "../../status"
  import { session } from "../../stores/session.svelte"

  /** The server's own bound, mirrored so the field can say it before the 400. */
  const MIN_PASSWORD = 8

  let users = $state<Profile[]>([])
  let overdueByUser = $state<Record<string, number>>({})
  let loading = $state(true)
  let error = $state<string | null>(null)
  let search = $state("")

  let formOpen = $state(false)
  let editing = $state<Profile | null>(null)
  let form = $state<UserInput>(blankForm())
  let formError = $state<string | null>(null)
  let saving = $state(false)

  let passwordTarget = $state<Profile | null>(null)
  let newPassword = $state("")
  let passwordError = $state<string | null>(null)

  let historyTarget = $state<Profile | null>(null)
  let historyRows = $state<CustodyRecord[]>([])
  let historyError = $state<string | null>(null)

  let deleteTarget = $state<Profile | null>(null)
  let deleteError = $state<string | null>(null)

  let importOpen = $state(false)
  let importFiles = $state<FileList | undefined>(undefined)
  let importPhotoDir = $state("")
  let importResult = $state<RosterResult | null>(null)
  let importError = $state<string | null>(null)
  let importing = $state(false)

  function blankForm(): UserInput {
    return { student_number: "", first_name: "", last_name: "", email: null, is_admin: false }
  }

  const visible = $derived(
    search.trim()
      ? users.filter((u) => {
          const needle = search.trim().toLowerCase()
          return (
            (u.student_number ?? "").includes(needle) ||
            `${u.first_name ?? ""} ${u.last_name ?? ""}`.toLowerCase().includes(needle) ||
            (u.full_name ?? "").toLowerCase().includes(needle)
          )
        })
      : users
  )

  function displayName(user: Profile) {
    const parts = [user.first_name, user.last_name].filter(Boolean).join(" ")
    return parts || user.full_name || user.student_number || "—"
  }

  async function load() {
    loading = true
    error = null
    try {
      users = await api.listUsers()
      // The overdue column comes from the admin roster read, which is the one
      // definition of overdue the sign-in warning and the checkout block share.
      try {
        const rows = await api.overdueCustody()
        const counts: Record<string, number> = {}
        for (const row of rows) counts[row.custodian_id] = (counts[row.custodian_id] ?? 0) + 1
        overdueByUser = counts
      } catch {
        // A missing overdue count is a blank column, not a broken screen.
        overdueByUser = {}
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      loading = false
    }
  }

  $effect(() => {
    load()
  })

  function openCreate() {
    editing = null
    form = blankForm()
    formError = null
    formOpen = true
  }

  function openEdit(user: Profile) {
    editing = user
    form = {
      student_number: user.student_number ?? "",
      first_name: user.first_name ?? "",
      last_name: user.last_name ?? "",
      email: user.email,
      is_admin: user.is_admin,
    }
    formError = null
    formOpen = true
  }

  async function save(event: SubmitEvent) {
    event.preventDefault()
    saving = true
    formError = null
    try {
      const payload: UserInput = { ...form, email: form.email || null }
      if (editing) await api.updateUser(editing.id, payload)
      else await api.createUser(payload)
      formOpen = false
      toast.success(
        editing ? "User updated" : "User created — they set a password at their first scan login"
      )
      await load()
      // An admin who just edited themselves needs the shell to notice.
      if (editing?.id === session.profile?.id) await session.refresh()
    } catch (err) {
      // "cannot remove your own admin flag" and friends arrive here; as written.
      formError = err instanceof Error ? err.message : String(err)
    } finally {
      saving = false
    }
  }

  async function savePassword(event: SubmitEvent) {
    event.preventDefault()
    if (!passwordTarget) return
    passwordError = null
    if (newPassword.length < MIN_PASSWORD) {
      passwordError = `At least ${MIN_PASSWORD} characters.`
      return
    }
    try {
      await api.setUserPassword(passwordTarget.id, newPassword)
      toast.success(`Password set for ${displayName(passwordTarget)}`)
      passwordTarget = null
      newPassword = ""
    } catch (err) {
      passwordError = err instanceof Error ? err.message : String(err)
    }
  }

  async function openHistory(user: Profile) {
    historyTarget = user
    historyRows = []
    historyError = null
    try {
      historyRows = await api.userHistory(user.id)
    } catch (err) {
      historyError = err instanceof Error ? err.message : String(err)
    }
  }

  async function confirmDelete() {
    if (!deleteTarget) return
    deleteError = null
    try {
      await api.deleteUser(deleteTarget.id)
      toast.success("User deleted")
      deleteTarget = null
      await load()
    } catch (err) {
      // Refused for yourself, and for anyone with custody history — the trail is
      // the point and the FK enforces it. The message names which.
      deleteError = err instanceof Error ? err.message : String(err)
    }
  }

  async function runImport() {
    const file = importFiles?.[0]
    if (!file) return
    importing = true
    importError = null
    importResult = null
    try {
      importResult = await api.importRoster(file, importPhotoDir.trim() || undefined)
      await load()
    } catch (err) {
      importError = err instanceof Error ? err.message : String(err)
    } finally {
      importing = false
    }
  }
</script>

<div data-density="compact" class="flex flex-col gap-3">
  <div class="flex items-center gap-3">
    <Input bind:value={search} placeholder="Search name or number…" class="max-w-xs" />
    <span class="flex-1"></span>
    <Button
      variant="secondary"
      onclick={() => {
        importOpen = true
        importResult = null
        importError = null
        importFiles = undefined
      }}
    >
      <UploadIcon aria-hidden="true" />
      Import roster
    </Button>
    <Button onclick={openCreate}>
      <PlusIcon aria-hidden="true" />
      New user
    </Button>
  </div>

  {#if error}
    <EmptyState title="Couldn't load users" description={error}>
      {#snippet action()}
        <Button variant="secondary" onclick={load}>Try again</Button>
      {/snippet}
    </EmptyState>
  {:else if loading}
    <p class="text-fg-muted">Loading…</p>
  {:else if visible.length === 0}
    <EmptyState
      title="No users"
      description={search ? "Nothing matches that search." : "Import the roster, or create an account."}
    />
  {:else}
    <div class="overflow-hidden rounded-xl border border-line-strong">
      <Table.Root>
        <Table.Header>
          <Table.Row>
            <!-- Name first: an admin looking someone up knows who they are, not
                 what their number is. The number is how the *scanner* addresses
                 them, not how a person does. -->
            <Table.Head>Name</Table.Head>
            <Table.Head>Student number</Table.Head>
            <Table.Head>Role</Table.Head>
            <Table.Head>Overdue</Table.Head>
            <Table.Head class="text-right">Actions</Table.Head>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {#each visible as user (user.id)}
            {@const overdue = overdueByUser[user.id] ?? 0}
            <Table.Row>
              <Table.Cell class="text-fg">{displayName(user)}</Table.Cell>
              <Table.Cell>
                <Serial value={user.student_number} label="student number" />
              </Table.Cell>
              <Table.Cell class="text-fg-muted">{user.is_admin ? "Admin" : "Student"}</Table.Cell>
              <Table.Cell class={overdue > 0 ? "text-status-overdue tabular-nums" : "text-fg-faint tabular-nums"}>
                {overdue > 0 ? `${overdue} overdue` : "—"}
              </Table.Cell>
              <Table.Cell class="text-right">
                <Tooltip.Provider>
                  <div class="flex justify-end gap-0.5">
                    <Tooltip.Root>
                      <Tooltip.Trigger>
                        {#snippet child({ props })}
                          <Button
                            {...props}
                            variant="ghost"
                            size="icon-sm"
                            aria-label={`Edit ${displayName(user)}`}
                            onclick={() => openEdit(user)}
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
                            aria-label={`Set a password for ${displayName(user)}`}
                            onclick={() => {
                              passwordTarget = user
                              newPassword = ""
                              passwordError = null
                            }}
                          >
                            <KeyIcon aria-hidden="true" />
                          </Button>
                        {/snippet}
                      </Tooltip.Trigger>
                      <Tooltip.Content>Set password</Tooltip.Content>
                    </Tooltip.Root>

                    <Tooltip.Root>
                      <Tooltip.Trigger>
                        {#snippet child({ props })}
                          <Button
                            {...props}
                            variant="ghost"
                            size="icon-sm"
                            aria-label={`History for ${displayName(user)}`}
                            onclick={() => openHistory(user)}
                          >
                            <ClockIcon aria-hidden="true" />
                          </Button>
                        {/snippet}
                      </Tooltip.Trigger>
                      <Tooltip.Content>History</Tooltip.Content>
                    </Tooltip.Root>

                    <Tooltip.Root>
                      <Tooltip.Trigger>
                        {#snippet child({ props })}
                          <Button
                            {...props}
                            variant="ghost"
                            size="icon-sm"
                            aria-label={`Delete ${displayName(user)}`}
                            onclick={() => {
                              deleteTarget = user
                              deleteError = null
                            }}
                          >
                            <TrashIcon class="text-destructive" aria-hidden="true" />
                          </Button>
                        {/snippet}
                      </Tooltip.Trigger>
                      <Tooltip.Content>Delete</Tooltip.Content>
                    </Tooltip.Root>
                  </div>
                </Tooltip.Provider>
              </Table.Cell>
            </Table.Row>
          {/each}
        </Table.Body>
      </Table.Root>
    </div>
  {/if}
</div>

<Dialog.Root bind:open={formOpen}>
  <Dialog.Content>
    <form onsubmit={save} class="flex flex-col gap-3">
      <Dialog.Header>
        <Dialog.Title>{editing ? `Edit ${displayName(editing)}` : "New user"}</Dialog.Title>
        <Dialog.Description>
          The student number is what the ID card barcode encodes. A new account has no password; it
          sets one at its first scan login, or you can set one here afterwards.
        </Dialog.Description>
      </Dialog.Header>

      <!-- Name before number, in the order the table lists them: filling a form
           in one order and reading the result in another is a small tax paid
           every time. -->
      <div class="grid grid-cols-2 gap-3">
        <div class="flex flex-col gap-1.5">
          <Label for="user-first">First name</Label>
          <Input id="user-first" bind:value={form.first_name} required />
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="user-last">Last name</Label>
          <Input id="user-last" bind:value={form.last_name} required />
        </div>
      </div>

      <div class="flex flex-col gap-1.5">
        <Label for="user-number">Student number</Label>
        <Input
          id="user-number"
          inputmode="numeric"
          pattern="[0-9]*"
          bind:value={form.student_number}
          required
        />
        <p class="text-xs text-fg-faint">Digits only. This is what the ID card barcode encodes.</p>
      </div>

      <div class="flex flex-col gap-1.5">
        <Label for="user-email">Email (optional)</Label>
        <Input
          id="user-email"
          type="email"
          value={form.email ?? ""}
          oninput={(e) => (form.email = e.currentTarget.value)}
        />
      </div>

      <div class="flex items-center gap-2">
        <Checkbox
          id="user-admin"
          checked={form.is_admin}
          onCheckedChange={(v) => (form.is_admin = v === true)}
        />
        <Label for="user-admin">Administrator</Label>
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

<Dialog.Root open={passwordTarget !== null} onOpenChange={(open) => !open && (passwordTarget = null)}>
  <Dialog.Content>
    <form onsubmit={savePassword} class="flex flex-col gap-3">
      <Dialog.Header>
        <Dialog.Title>Set a password</Dialog.Title>
        <Dialog.Description>
          For {passwordTarget ? displayName(passwordTarget) : ""}. This signs them out of any open
          session, which the server does itself.
        </Dialog.Description>
      </Dialog.Header>
      <div class="flex flex-col gap-1.5">
        <Label for="reset-password">New password</Label>
        <Input id="reset-password" type="password" bind:value={newPassword} required />
        <p class="text-xs text-fg-faint">At least {MIN_PASSWORD} characters.</p>
      </div>
      {#if passwordError}
        <p class="text-status-overdue" role="alert">{passwordError}</p>
      {/if}
      <Dialog.Footer>
        <Button type="button" variant="ghost" onclick={() => (passwordTarget = null)}>Cancel</Button>
        <Button type="submit">Set password</Button>
      </Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>

<Dialog.Root open={historyTarget !== null} onOpenChange={(open) => !open && (historyTarget = null)}>
  <Dialog.Content class="max-w-2xl">
    <Dialog.Header>
      <Dialog.Title>{historyTarget ? displayName(historyTarget) : ""} — custody history</Dialog.Title>
    </Dialog.Header>
    {#if historyError}
      <p class="text-status-overdue" role="alert">{historyError}</p>
    {:else if historyRows.length === 0}
      <p class="text-fg-muted">Nothing checked out yet.</p>
    {:else}
      <div class="max-h-96 overflow-y-auto">
        <Table.Root>
          <Table.Header>
            <Table.Row>
              <Table.Head>Item</Table.Head>
              <Table.Head>Out</Table.Head>
              <Table.Head>Due</Table.Head>
              <Table.Head>Returned</Table.Head>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {#each historyRows as row (row.id)}
              <Table.Row>
                <Table.Cell class="text-fg">{row.asset_name}</Table.Cell>
                <Table.Cell class="tabular-nums text-fg-muted">{dateTime(row.checked_out_at)}</Table.Cell>
                <Table.Cell class="tabular-nums text-fg-muted">{dateTime(row.due_at)}</Table.Cell>
                <Table.Cell
                  class={row.checked_in_at
                    ? "tabular-nums text-fg-muted"
                    : row.overdue
                      ? "text-status-overdue"
                      : "text-status-out"}
                >
                  {row.checked_in_at ? dateTime(row.checked_in_at) : row.overdue ? "Overdue" : "Out"}
                </Table.Cell>
              </Table.Row>
            {/each}
          </Table.Body>
        </Table.Root>
      </div>
    {/if}
  </Dialog.Content>
</Dialog.Root>

<AlertDialog.Root open={deleteTarget !== null} onOpenChange={(open) => !open && (deleteTarget = null)}>
  <AlertDialog.Content>
    <AlertDialog.Header>
      <AlertDialog.Title>Delete {deleteTarget ? displayName(deleteTarget) : ""}?</AlertDialog.Title>
      <AlertDialog.Description>
        Anyone who has ever checked something out cannot be deleted: the custody trail is the point.
        You can't delete yourself either.
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

<!-- Roster import. Never fire-and-forget: the per-row result list is the point
     of the dialog, so it stays open until the admin has read it (§8.7). -->
<Dialog.Root bind:open={importOpen}>
  <Dialog.Content class="max-w-2xl">
    <Dialog.Header>
      <Dialog.Title>Import a roster</Dialog.Title>
      <Dialog.Description>
        CSV with <code class="font-mono">first_name, last_name, student_number, photo_path</code> in
        any column order. Existing accounts match by student number; admin flags and passwords
        survive a re-import.
      </Dialog.Description>
    </Dialog.Header>

    <div class="flex flex-col gap-3">
      <div class="flex flex-col gap-1.5">
        <Label for="roster-file">CSV file</Label>
        <Input id="roster-file" type="file" accept=".csv,text/csv" bind:files={importFiles} />
      </div>
      <div class="flex flex-col gap-1.5">
        <Label for="roster-photos">Photo folder (optional)</Label>
        <Input
          id="roster-photos"
          bind:value={importPhotoDir}
          placeholder="/path/the/photo_path values resolve against"
        />
      </div>

      {#if importError}
        <p class="text-status-overdue" role="alert">{importError}</p>
      {/if}

      {#if importResult}
        <div class="rounded-(--radius-lg) border border-line-strong p-3">
          <p class="text-sm text-fg">
            {importResult.created} created · {importResult.updated} updated ·
            <span class={importResult.failed > 0 ? "text-status-overdue" : ""}>
              {importResult.failed} failed
            </span>
          </p>
          <div class="mt-2 max-h-64 overflow-y-auto">
            <Table.Root>
              <Table.Header>
                <Table.Row>
                  <Table.Head>Row</Table.Head>
                  <Table.Head>Student number</Table.Head>
                  <Table.Head>Result</Table.Head>
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {#each importResult.rows as row (row.row)}
                  <Table.Row>
                    <Table.Cell class="tabular-nums text-fg-muted">{row.row}</Table.Cell>
                    <Table.Cell class="font-mono">{row.student_number}</Table.Cell>
                    <Table.Cell class={row.action === "error" ? "text-status-overdue" : "text-fg-muted"}>
                      {row.action === "error" ? (row.error ?? "error") : row.action}
                    </Table.Cell>
                  </Table.Row>
                {/each}
              </Table.Body>
            </Table.Root>
          </div>
        </div>
      {/if}
    </div>

    <Dialog.Footer>
      <Button variant="ghost" onclick={() => (importOpen = false)}>
        {importResult ? "Done" : "Cancel"}
      </Button>
      <Button disabled={importing || !importFiles?.length} onclick={runImport}>
        {importing ? "Importing…" : "Import"}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
