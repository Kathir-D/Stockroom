<script lang="ts">
  /**
   * Admin → Settings (docs/design/backup.md §E.9).
   *
   * The rule this screen exists to satisfy is one sentence: after the one-time
   * software install, **no admin ever edits a file**. Where backups go, how long
   * they are kept, which Google account they are pushed to and whether the
   * archive is encrypted are all forms here, not lines in `.env`. `.env` seeds
   * these columns once on a fresh database and is ignored afterwards, so a value
   * typed here is never silently out-voted by an environment variable on the
   * next restart (§C.2).
   *
   * **One card, one save.** `SettingsInput` is a partial update — every field is
   * a pointer and a nil one leaves the column alone — which is what lets the
   * GitHub card save without carrying, and possibly clobbering, whatever is
   * half-typed in the Drive card. A single page-wide Save would make every
   * unrelated field a hazard.
   *
   * **Secrets come back blank, never masked.** A mask round-trips: the panel
   * renders `••••••••`, the admin edits some other field, saves, and the mask is
   * written back as the literal new token. So the field is empty with a line
   * saying whether one is stored, leaving it empty means "leave it alone", and
   * removing one is its own button.
   *
   * **Numbers are validated here as well as in the package and in the DDL.** Not
   * duplication for its own sake: the server's message is correct but arrives
   * after a round trip and lands in a card-level error, while the admin is
   * looking at the field. Both messages say the same thing because both are
   * quoting the same bounds.
   */
  import CheckIcon from "@lucide/svelte/icons/check"
  import CloudIcon from "@lucide/svelte/icons/cloud"
  import GithubIcon from "@lucide/svelte/icons/git-branch"
  import FolderIcon from "@lucide/svelte/icons/folder"
  import ClockIcon from "@lucide/svelte/icons/clock"
  import ImageIcon from "@lucide/svelte/icons/image"
  import LockIcon from "@lucide/svelte/icons/lock"
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw"
  import ExternalLinkIcon from "@lucide/svelte/icons/external-link"
  import { toast } from "svelte-sonner"
  import { Button } from "@stockroom/ui/components/ui/button"
  import { Checkbox } from "@stockroom/ui/components/ui/checkbox"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import { Skeleton } from "@stockroom/ui/components/ui/skeleton"
  import EmptyState from "@stockroom/ui/components/app/empty-state.svelte"
  import PasswordInput from "@stockroom/ui/components/app/password-input.svelte"
  import StudentNumberFormat from "@stockroom/ui/components/app/student-number-format.svelte"
  import IdCardIcon from "@lucide/svelte/icons/id-card"
  import type { StudentNumberFormat as Format } from "../../student-number"
  import * as api from "../../api/index"
  import type { Settings, SettingsInput } from "../../api/types"
  import { dateTime } from "../../status"
  import { router } from "../../stores/router.svelte"

  /** Which card is mid-save, so only that card's button says "Saving…". */
  type Card = "signin" | "folders" | "schedule" | "photos" | "drive" | "github" | "encryption"

  let settings = $state<Settings | null>(null)
  let loading = $state(true)
  let loadError = $state<string | null>(null)

  let saving = $state<Card | null>(null)
  let cardError = $state<Partial<Record<Card, string>>>({})
  let testing = $state<"drive" | "github" | null>(null)

  /**
   * The editable copy.
   *
   * Numbers are held as strings, and the fields are `type="text"` with
   * `inputmode="numeric"` rather than `type="number"` — which looks like the
   * wrong call until you clear one.
   *
   * A number input does not hand back what is in the box. Svelte binds it
   * through `valueAsNumber`, so an emptied field arrives as `null` and `12abc`
   * arrives as `null` too, indistinguishable from each other and from a field
   * nobody touched. An omitted field in a partial update means "leave it
   * alone", so clearing a box and pressing Save would either throw on the null
   * or quietly save nothing while looking like it worked. Measured: it threw,
   * and the admin got "Cannot read properties of null" where the message should
   * have said the field was blank.
   *
   * A text field binds the characters that are actually on screen, which is the
   * only value anybody can reason about. `whole()` below still guards against a
   * non-string, because a binding is a thing that can be changed back.
   */
  let draft = $state({
    backup_dir: "",
    photo_backup_dir: "",
    keep_days: "",
    stale_hours: "",
    schedule_hour: "",
    photo_min_free_gb: "",
    photo_max_generations: "",
    drive_enabled: false,
    drive_remote: "",
    drive_path: "",
    github_enabled: false,
    github_repo: "",
    student_number_format: "digits" as Format,
    student_number_pattern: "",
  })

  /** Secrets live outside `draft`: blank means unchanged, not blank-it-out. */
  let githubToken = $state("")
  let passphrase = $state("")
  let passphraseConfirm = $state("")

  /* ---------------------------------------------------------- loading ---- */

  function adopt(next: Settings) {
    settings = next
    draft = {
      backup_dir: next.backup_dir,
      photo_backup_dir: next.photo_backup_dir,
      keep_days: String(next.keep_days),
      stale_hours: String(next.stale_hours),
      schedule_hour: String(next.schedule_hour),
      photo_min_free_gb: String(next.photo_min_free_gb),
      photo_max_generations: String(next.photo_max_generations),
      drive_enabled: next.drive_enabled,
      drive_remote: next.drive_remote,
      drive_path: next.drive_path,
      github_enabled: next.github_enabled,
      github_repo: next.github_repo,
      student_number_format: next.student_number_format,
      student_number_pattern: next.student_number_pattern,
    }
    // The secret fields are cleared on every adopt, including after a save that
    // just stored one. Leaving a token sitting in a text box on a shared closet
    // machine is the thing the blank-not-masked rule is about.
    githubToken = ""
    passphrase = ""
    passphraseConfirm = ""
  }

  async function load() {
    loading = true
    loadError = null
    try {
      adopt(await api.getSettings())
    } catch (err) {
      loadError = err instanceof Error ? err.message : String(err)
    } finally {
      loading = false
    }
  }

  $effect(() => {
    load()
  })

  /* ----------------------------------------------------------- saving ---- */

  /**
   * Parse a whole number, refusing everything a number field would otherwise
   * let through: blank, a decimal, or text. Throws, so a card's save handler
   * reads as a straight line and the message lands in that card's error slot.
   */
  function whole(value: unknown, label: string, min: number, max?: number): number {
    // `unknown`, not `string`: this is the last line of defence for the binding
    // hazard described above. A null arriving here should read as "blank", the
    // same as an empty box, not as a crash in the middle of a save.
    const trimmed = typeof value === "number" ? String(value) : String(value ?? "").trim()
    if (!trimmed) throw new Error(`${label} can't be blank.`)
    const parsed = Number(trimmed)
    if (!Number.isInteger(parsed)) throw new Error(`${label} must be a whole number.`)
    if (parsed < min) throw new Error(`${label} must be at least ${min}.`)
    if (max !== undefined && parsed > max) throw new Error(`${label} must be ${max} or less.`)
    return parsed
  }

  async function save(card: Card, build: () => SettingsInput) {
    saving = card
    cardError = { ...cardError, [card]: undefined }
    try {
      const input = build()
      adopt(await api.saveSettings(input))
      toast.success("Settings saved")
    } catch (err) {
      // The server's messages are written for the person reading them ("a
      // remote name cannot contain spaces", "github_repo must be
      // owner/repository") so they are shown as written, beside the card that
      // sent them rather than in a toast that scrolls away.
      cardError = { ...cardError, [card]: err instanceof Error ? err.message : String(err) }
    } finally {
      saving = null
    }
  }

  const saveSignIn = () =>
    save("signin", () => ({
      student_number_format: draft.student_number_format,
      student_number_pattern: draft.student_number_pattern.trim(),
    }))

  const saveFolders = () =>
    save("folders", () => ({
      backup_dir: draft.backup_dir.trim(),
      photo_backup_dir: draft.photo_backup_dir.trim(),
    }))

  const saveSchedule = () =>
    save("schedule", () => ({
      schedule_hour: whole(draft.schedule_hour, "The backup hour", 0, 23),
      keep_days: whole(draft.keep_days, "Keep backups for", 1, 3650),
      stale_hours: whole(draft.stale_hours, "Warn after", 1, 8760),
    }))

  const savePhotos = () =>
    save("photos", () => ({
      photo_min_free_gb: whole(draft.photo_min_free_gb, "Free-space warning", 0, 10000),
      photo_max_generations: whole(draft.photo_max_generations, "Keep at most", 1, 1000),
    }))

  const saveDrive = () =>
    save("drive", () => ({
      drive_enabled: draft.drive_enabled,
      drive_remote: draft.drive_remote.trim(),
      drive_path: draft.drive_path.trim(),
    }))

  const saveGithub = () =>
    save("github", () => {
      const input: SettingsInput = {
        github_enabled: draft.github_enabled,
        github_repo: draft.github_repo.trim(),
      }
      // Omitted entirely when the box is empty, which is what "leave the stored
      // token alone" means on the wire. Sending "" is how a token is removed,
      // and that is the Remove button, never a field somebody didn't type in.
      if (githubToken.trim()) input.github_token = githubToken.trim()
      return input
    })

  const removeGithubToken = () =>
    save("github", () => ({ github_token: "", github_enabled: false }))

  const saveEncryption = () =>
    save("encryption", () => {
      if (passphrase !== passphraseConfirm) {
        throw new Error("The two passphrases don't match.")
      }
      if (passphrase.length < 8) {
        throw new Error("A passphrase must be at least 8 characters.")
      }
      return { archive_passphrase: passphrase }
    })

  const removePassphrase = () => save("encryption", () => ({ archive_passphrase: "" }))

  /* ------------------------------------------------------------ tests ---- */

  async function test(target: "drive" | "github") {
    testing = target
    cardError = { ...cardError, [target]: undefined }
    try {
      await api.testBackupTarget(target)
      toast.success(target === "drive" ? "Google Drive is reachable" : "GitHub is reachable")
    } catch (err) {
      // A "test connection" that says only "failed" is a button nobody presses
      // twice, so whatever rclone or the GitHub API said is shown verbatim.
      cardError = { ...cardError, [target]: err instanceof Error ? err.message : String(err) }
    } finally {
      testing = null
    }
  }

  /* --------------------------------------------------- connecting drive ---- */

  let connectOpen = $state(false)
  let connectUrl = $state("")
  let connectId = $state("")
  let connectCode = $state("")
  let connectRemote = $state("stockroom-drive")
  let connectError = $state<string | null>(null)
  let connecting = $state(false)

  async function startConnect() {
    connecting = true
    connectError = null
    try {
      const result = await api.connectDrive()
      connectUrl = result.url
      connectId = result.id
      connectCode = ""
      connectRemote = draft.drive_remote.trim() || "stockroom-drive"
      connectOpen = true
    } catch (err) {
      // 503 when rclone is not installed, with the install command in the
      // message. It belongs beside the button, not in a toast.
      cardError = { ...cardError, drive: err instanceof Error ? err.message : String(err) }
    } finally {
      connecting = false
    }
  }

  async function finishConnect() {
    connecting = true
    connectError = null
    try {
      adopt(await api.finishDriveConnect(connectId, connectCode.trim(), connectRemote.trim()))
      connectOpen = false
      toast.success("Google Drive connected")
    } catch (err) {
      // "Google has not sent the code back yet" is the common one and is a
      // retry, not a failure — so the dialog stays open with the message in it.
      connectError = err instanceof Error ? err.message : String(err)
    } finally {
      connecting = false
    }
  }

  async function copyLink() {
    try {
      await navigator.clipboard.writeText(connectUrl)
      toast.success("Link copied")
    } catch {
      // Clipboard access can be refused in a webview. The link is on screen
      // and selectable either way, so this is not worth an error surface.
      toast.info("Select the link and copy it by hand.")
    }
  }

  /** `02:00`, matching the sentence `BackupStatus` builds for the schedule. */
  const scheduleLabel = $derived(
    /^\d{1,2}$/.test(draft.schedule_hour.trim())
      ? `${draft.schedule_hour.trim().padStart(2, "0")}:00`
      : "—"
  )
</script>

<div data-density="compact" class="flex max-w-3xl flex-col gap-4">
  <div class="flex items-center gap-3">
    <div class="flex flex-col">
      <h1 class="text-base font-semibold text-fg">Settings</h1>
      <p class="text-xs text-fg-muted">
        Where backups go and when they run. Saved in the database, not in a file — nothing here
        needs a restart.
      </p>
    </div>
    <span class="flex-1"></span>
    <Button variant="ghost" size="icon-sm" aria-label="Reload settings" onclick={load}>
      <RefreshIcon aria-hidden="true" />
    </Button>
  </div>

  {#if loadError}
    <EmptyState title="Couldn't load settings" description={loadError}>
      {#snippet action()}
        <Button variant="secondary" onclick={load}>Try again</Button>
      {/snippet}
    </EmptyState>
  {:else if loading || !settings}
    <div class="flex flex-col gap-3" aria-busy="true" aria-label="Loading settings">
      {#each Array(4) as _, index (index)}
        <Skeleton class="h-40 rounded-xl" />
      {/each}
    </div>
  {:else}
    <!-- ------------------------------------------------------- sign-in ---- -->
    <section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
      <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
        <IdCardIcon class="size-4 text-fg-muted" aria-hidden="true" />
        Student numbers
      </h2>
      <p class="text-xs text-fg-muted">
        What your school's ID numbers look like. Sign-in, the roster import and new accounts all
        follow this. Change it only if IDs at your school are not plain digits — and test a real
        one below before saving, because a rule that refuses your IDs stops everybody signing in.
      </p>
      <StudentNumberFormat
        idPrefix="settings-sn"
        bind:format={draft.student_number_format}
        bind:pattern={draft.student_number_pattern}
      />
      {#if cardError.signin}
        <p class="text-sm text-status-overdue" role="alert">{cardError.signin}</p>
      {/if}
      <div>
        <Button disabled={saving === "signin"} onclick={saveSignIn}>
          {saving === "signin" ? "Saving…" : "Save student numbers"}
        </Button>
      </div>
    </section>

    <!-- ------------------------------------------------------ folders ---- -->
    <section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
      <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
        <FolderIcon class="size-4 text-fg-muted" aria-hidden="true" />
        Folders
      </h2>
      <p class="text-xs text-fg-muted">
        Absolute paths on this machine. The backup folder is also what
        <code class="font-mono">rclone</code> copies to Google Drive, so it should be somewhere
        with room to grow.
      </p>

      <div class="flex flex-col gap-1">
        <Label for="backup-dir">Backup folder</Label>
        <Input
          id="backup-dir"
          bind:value={draft.backup_dir}
          placeholder="/Users/media/stockroom-backups"
          spellcheck={false}
          autocomplete="off"
        />
        <p class="text-xs text-fg-faint">
          Empty means backups are switched off entirely and nothing is being kept.
        </p>
      </div>

      <div class="flex flex-col gap-1">
        <Label for="photo-dir">Photo mirror folder</Label>
        <Input
          id="photo-dir"
          bind:value={draft.photo_backup_dir}
          placeholder="/Users/media/stockroom-photos"
          spellcheck={false}
          autocomplete="off"
        />
        <p class="text-xs text-fg-faint">
          Photos are mirrored on this machine only — they are never pushed off-site. Leave it empty
          to skip the mirror.
        </p>
      </div>

      {#if cardError.folders}
        <p class="text-sm text-status-overdue" role="alert">{cardError.folders}</p>
      {/if}
      <div>
        <Button disabled={saving === "folders"} onclick={saveFolders}>
          {saving === "folders" ? "Saving…" : "Save folders"}
        </Button>
      </div>
    </section>

    <!-- ----------------------------------------------------- schedule ---- -->
    <section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
      <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
        <ClockIcon class="size-4 text-fg-muted" aria-hidden="true" />
        Schedule and retention
      </h2>
      <p class="text-xs text-fg-muted">
        The backup runs inside the server, so the machine only has to be switched on. If it was off
        at the scheduled time, the next start runs one immediately.
      </p>

      <div class="grid gap-3 sm:grid-cols-3">
        <div class="flex flex-col gap-1">
          <Label for="schedule-hour">Run at (hour, 0–23)</Label>
          <Input
            id="schedule-hour"
            inputmode="numeric"
            pattern="[0-9]*"
            bind:value={draft.schedule_hour}
          />
          <p class="text-xs text-fg-faint">{scheduleLabel} every day</p>
        </div>

        <div class="flex flex-col gap-1">
          <Label for="keep-days">Keep backups for (days)</Label>
          <Input
            id="keep-days"
            inputmode="numeric"
            pattern="[0-9]*"
            bind:value={draft.keep_days}
          />
          <p class="text-xs text-fg-faint">Older local folders are pruned.</p>
        </div>

        <div class="flex flex-col gap-1">
          <Label for="stale-hours">Warn after (hours)</Label>
          <Input
            id="stale-hours"
            inputmode="numeric"
            pattern="[0-9]*"
            bind:value={draft.stale_hours}
          />
          <p class="text-xs text-fg-faint">Everyone who signs in is told.</p>
        </div>
      </div>

      {#if cardError.schedule}
        <p class="text-sm text-status-overdue" role="alert">{cardError.schedule}</p>
      {/if}
      <div>
        <Button disabled={saving === "schedule"} onclick={saveSchedule}>
          {saving === "schedule" ? "Saving…" : "Save schedule"}
        </Button>
      </div>
    </section>

    <!-- ------------------------------------------------- photo limits ---- -->
    <section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
      <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
        <ImageIcon class="size-4 text-fg-muted" aria-hidden="true" />
        Photo mirror limits
      </h2>
      <p class="text-xs text-fg-muted">
        The mirror never deletes anything by itself, so it only grows. These two numbers decide when
        it starts warning you — photos are copied in the same run that writes the database backup,
        so a full disk stops the backup as well.
      </p>

      <div class="grid gap-3 sm:grid-cols-2">
        <div class="flex flex-col gap-1">
          <Label for="min-free">Warn below (GB free)</Label>
          <Input
            id="min-free"
            inputmode="numeric"
            pattern="[0-9]*"
            bind:value={draft.photo_min_free_gb}
          />
          <p class="text-xs text-fg-faint">0 turns the free-space warning off.</p>
        </div>

        <div class="flex flex-col gap-1">
          <Label for="max-gens">Warn above (generations kept)</Label>
          <Input
            id="max-gens"
            inputmode="numeric"
            pattern="[0-9]*"
            bind:value={draft.photo_max_generations}
          />
          <p class="text-xs text-fg-faint">
            Deleting one is always a button on the Backup screen, never automatic.
          </p>
        </div>
      </div>

      {#if cardError.photos}
        <p class="text-sm text-status-overdue" role="alert">{cardError.photos}</p>
      {/if}
      <div>
        <Button disabled={saving === "photos"} onclick={savePhotos}>
          {saving === "photos" ? "Saving…" : "Save limits"}
        </Button>
      </div>
    </section>

    <!-- -------------------------------------------------- google drive ---- -->
    <section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
      <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
        <CloudIcon class="size-4 text-fg-muted" aria-hidden="true" />
        Google Drive
      </h2>
      <p class="text-xs text-fg-muted">
        Needs <code class="font-mono">rclone</code> installed on this machine. Press Connect and
        sign in to the Google account the backups should live in — you'll see a
        "Google hasn't verified this app" screen, which is expected.
      </p>

      <label class="flex items-center gap-2 text-sm text-fg">
        <Checkbox
          checked={draft.drive_enabled}
          onCheckedChange={(v) => (draft.drive_enabled = v === true)}
        />
        Back up to Google Drive every night
      </label>

      <div class="grid gap-3 sm:grid-cols-2">
        <div class="flex flex-col gap-1">
          <Label for="drive-remote">rclone remote name</Label>
          <Input
            id="drive-remote"
            bind:value={draft.drive_remote}
            placeholder="stockroom-drive"
            spellcheck={false}
            autocomplete="off"
          />
          <p class="text-xs text-fg-faint">Connect fills this in for you.</p>
        </div>
        <div class="flex flex-col gap-1">
          <Label for="drive-path">Folder in Drive</Label>
          <Input
            id="drive-path"
            bind:value={draft.drive_path}
            placeholder="stockroom-backups"
            spellcheck={false}
            autocomplete="off"
          />
          <p class="text-xs text-fg-faint">Created if it isn't there.</p>
        </div>
      </div>

      {#if cardError.drive}
        <p class="text-sm text-status-overdue" role="alert">{cardError.drive}</p>
      {/if}

      <div class="flex flex-wrap gap-2">
        <Button disabled={saving === "drive"} onclick={saveDrive}>
          {saving === "drive" ? "Saving…" : "Save Drive settings"}
        </Button>
        <Button variant="secondary" disabled={connecting} onclick={startConnect}>
          {connecting ? "Starting…" : settings.drive_remote ? "Reconnect" : "Connect"}
        </Button>
        <Button
          variant="ghost"
          disabled={testing === "drive" || !settings.drive_remote}
          onclick={() => test("drive")}
        >
          {testing === "drive" ? "Testing…" : "Test connection"}
        </Button>
      </div>
    </section>

    <!-- -------------------------------------------------------- github ---- -->
    <section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
      <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
        <GithubIcon class="size-4 text-fg-muted" aria-hidden="true" />
        GitHub
      </h2>
      <p class="text-xs text-fg-muted">
        Needs nothing installed, which is why it is the target that still works when Drive is
        blocked. <strong class="text-fg">The repository must be private</strong> — the backup
        contains student numbers, and a student number signs its owner in with no password.
      </p>

      <label class="flex items-center gap-2 text-sm text-fg">
        <Checkbox
          checked={draft.github_enabled}
          onCheckedChange={(v) => (draft.github_enabled = v === true)}
        />
        Back up to GitHub every night
      </label>

      <div class="flex flex-col gap-1">
        <Label for="github-repo">Repository</Label>
        <Input
          id="github-repo"
          bind:value={draft.github_repo}
          placeholder="your-username/stockroom-backup"
          spellcheck={false}
          autocomplete="off"
        />
      </div>

      <div class="flex flex-col gap-1">
        <Label for="github-token">Personal access token</Label>
        <PasswordInput
          id="github-token"
          bind:value={githubToken}
          autocomplete="off"
          placeholder={settings.github_token_set ? "Leave blank to keep the saved token" : "ghp_…"}
        />
        <p class="text-xs text-fg-faint">
          {#if settings.github_token_set}
            A token is saved. Type a new one to replace it, or leave this blank to keep it.
          {:else}
            A fine-grained token with <span class="font-mono">Contents: Read and write</span> on
            that one repository.
          {/if}
        </p>
      </div>

      {#if cardError.github}
        <p class="text-sm text-status-overdue" role="alert">{cardError.github}</p>
      {/if}

      <div class="flex flex-wrap gap-2">
        <Button disabled={saving === "github"} onclick={saveGithub}>
          {saving === "github" ? "Saving…" : "Save GitHub settings"}
        </Button>
        <Button
          variant="ghost"
          disabled={testing === "github" || !settings.github_token_set}
          onclick={() => test("github")}
        >
          {testing === "github" ? "Testing…" : "Test connection"}
        </Button>
        {#if settings.github_token_set}
          <Button variant="ghost" disabled={saving === "github"} onclick={removeGithubToken}>
            Remove token
          </Button>
        {/if}
      </div>
    </section>

    <!-- ---------------------------------------------------- encryption ---- -->
    <section class="flex flex-col gap-3 rounded-xl border border-line-strong bg-surface p-4">
      <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
        <LockIcon class="size-4 text-fg-muted" aria-hidden="true" />
        Archive encryption
        <span class="text-xs font-normal text-fg-faint">optional</span>
      </h2>
      <p class="text-xs text-fg-muted">
        Off by default. With a passphrase set, the nightly archive is encrypted before it leaves
        this machine, so a leaked Drive folder or repository is unreadable.
      </p>
      <p class="text-xs text-fg-muted">
        <strong class="text-fg">Write the passphrase down somewhere that is not this machine.</strong>
        It is not stored in either backup target, and an archive whose passphrase is lost cannot be
        restored by anyone — including us. Changing it does not re-encrypt archives already written:
        those still need the old one.
      </p>

      {#if settings.archive_passphrase_set}
        <p class="flex items-center gap-1.5 text-sm text-status-available">
          <CheckIcon class="size-4" aria-hidden="true" />
          Archives are being encrypted.
        </p>
      {:else}
        <p class="text-sm text-fg-muted">Archives are written unencrypted.</p>
      {/if}

      <div class="grid gap-3 sm:grid-cols-2">
        <div class="flex flex-col gap-1">
          <Label for="passphrase">
            {settings.archive_passphrase_set ? "New passphrase" : "Passphrase"}
          </Label>
          <PasswordInput
            id="passphrase"
            bind:value={passphrase}
            autocomplete="new-password"
            minlength={8}
          />
        </div>
        <div class="flex flex-col gap-1">
          <Label for="passphrase-confirm">Confirm</Label>
          <PasswordInput
            id="passphrase-confirm"
            bind:value={passphraseConfirm}
            autocomplete="new-password"
            minlength={8}
          />
        </div>
      </div>

      {#if cardError.encryption}
        <p class="text-sm text-status-overdue" role="alert">{cardError.encryption}</p>
      {/if}

      <div class="flex flex-wrap gap-2">
        <Button disabled={saving === "encryption" || !passphrase} onclick={saveEncryption}>
          {saving === "encryption" ? "Saving…" : "Set passphrase"}
        </Button>
        {#if settings.archive_passphrase_set}
          <Button variant="ghost" disabled={saving === "encryption"} onclick={removePassphrase}>
            Turn encryption off
          </Button>
        {/if}
      </div>
    </section>

    <p class="text-xs text-fg-faint">
      Last changed {dateTime(settings.updated_at) || "—"}. See what these are doing on the
      <button
        type="button"
        class="underline underline-offset-2 hover:text-fg-muted"
        onclick={() => router.go({ name: "admin", tab: "backup" })}
      >
        Backup screen</button
      >.
    </p>
  {/if}
</div>

<!-- The Drive connect flow. rclone is already running and waiting for Google's
     callback by the time this opens; the dialog is the link plus the fallback
     for when the callback doesn't arrive. -->
<Dialog.Root bind:open={connectOpen}>
  <Dialog.Content>
    <Dialog.Header>
      <Dialog.Title>Connect Google Drive</Dialog.Title>
      <Dialog.Description>
        Open the link, sign in to the Google account the backups should live in, and allow access.
        Google will say it hasn't verified this app — press Advanced, then continue.
      </Dialog.Description>
    </Dialog.Header>

    <div class="flex flex-col gap-3">
      <div class="flex flex-col gap-1">
        <Label for="connect-url">Sign-in link</Label>
        <!-- Readonly input rather than a bare anchor: the Wails webview may
             refuse to hand a link to the system browser, and a link nobody can
             copy is a dead end. Both affordances are here on purpose. -->
        <div class="flex gap-2">
          <Input id="connect-url" readonly value={connectUrl} class="font-mono text-xs" />
          <Button variant="secondary" onclick={copyLink}>Copy</Button>
        </div>
        <a
          href={connectUrl}
          target="_blank"
          rel="noreferrer"
          class="flex items-center gap-1 text-xs text-fg-muted underline underline-offset-2 hover:text-fg"
        >
          <ExternalLinkIcon class="size-3" aria-hidden="true" />
          Open in a browser
        </a>
      </div>

      <div class="flex flex-col gap-1">
        <Label for="connect-remote">Name this connection</Label>
        <Input id="connect-remote" bind:value={connectRemote} spellcheck={false} autocomplete="off" />
        <p class="text-xs text-fg-faint">No spaces, colons or slashes.</p>
      </div>

      <div class="flex flex-col gap-1">
        <Label for="connect-code">If Google showed you a block of text, paste it here</Label>
        <Input
          id="connect-code"
          bind:value={connectCode}
          placeholder="{'{'}&quot;access_token&quot;: …{'}'}"
          spellcheck={false}
          autocomplete="off"
          class="font-mono text-xs"
        />
        <p class="text-xs text-fg-faint">
          Usually not needed — the sign-in normally completes on its own. Leave it blank and press
          Finish.
        </p>
      </div>

      {#if connectError}
        <p class="text-sm text-status-overdue" role="alert">{connectError}</p>
      {/if}
    </div>

    <Dialog.Footer>
      <Button variant="ghost" onclick={() => (connectOpen = false)} disabled={connecting}>
        Cancel
      </Button>
      <Button onclick={finishConnect} disabled={connecting}>
        {connecting ? "Finishing…" : "Finish"}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
