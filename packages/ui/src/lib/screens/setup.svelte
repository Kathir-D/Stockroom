<script lang="ts">
  /**
   * The first-run wizard (TEMPLATE-TODO Phase B), steps 1 and 3 to 7. Step 2,
   * creating the admin, happens on the sign-in screen, because it is the one
   * step with no session (components/app/first-admin.svelte).
   *
   * Design rules, from the plan and kept:
   *  - Each step saves as it completes (PUT /setup), so closing the laptop
   *    resumes here.
   *  - Every step is skippable except the safety net. A person who cannot skip
   *    is a person who enters rubbish to get past a screen.
   *  - Plain words. Never "instance", "schema", "migration", "seed".
   *  - A progress rail, so the end is visible from the start.
   *
   * Everything a step does is an ordinary admin-panel call -- the imports,
   * settings, Back up now -- so nothing here is a second way to do a thing
   * that could drift from the first.
   */
  import CheckIcon from "@lucide/svelte/icons/check"
  import { toast } from "svelte-sonner"
  import { Button } from "@stockroom/ui/components/ui/button"
  import { Checkbox } from "@stockroom/ui/components/ui/checkbox"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import BulkAddDialog from "@stockroom/ui/components/app/bulk-add-dialog.svelte"
  import ImportDialog from "@stockroom/ui/components/app/import-dialog.svelte"
  import PasswordInput from "@stockroom/ui/components/app/password-input.svelte"
  import PrintLabelsDialog, { type LabelItem } from "@stockroom/ui/components/app/print-labels-dialog.svelte"
  import StudentNumberFormat from "@stockroom/ui/components/app/student-number-format.svelte"
  import { cn } from "@stockroom/ui/utils"
  import * as api from "../api/index"
  import type {
    AssetDetail,
    AssetImportResult,
    BackupResult,
    CategoryImportResult,
    CategoryNode,
    RosterResult,
    SetupState,
  } from "../api/types"
  import { catalog } from "../stores/catalog.svelte"
  import { router } from "../stores/router.svelte"
  import { session } from "../stores/session.svelte"
  import type { StudentNumberFormat as Format } from "../student-number"

  const STEPS = [
    "Welcome",
    "Your account",
    "The safety net",
    "Lending rules",
    "Your equipment",
    "Your people",
    "Backups",
  ] as const

  let setup = $state<SetupState | null>(null)
  /** 1-based, matching the plan's table. */
  let step = $state(1)
  let error = $state<string | null>(null)

  $effect(() => {
    api.getSetup().then(
      (s) => {
        setup = s
        // Step 2 is done by definition: somebody is signed in as an admin.
        step = Math.min(Math.max(s.step, 3), STEPS.length + 1)
        if (s.step <= 1) step = 1
      },
      (err) => (error = err instanceof Error ? err.message : String(err)),
    )
  })

  async function go(next: number) {
    error = null
    try {
      setup = await api.saveSetup(next, false)
      step = next
      if (typeof window !== "undefined") window.scrollTo({ top: 0 })
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    }
  }

  async function finish() {
    try {
      await api.saveSetup(STEPS.length, true)
      await catalog.loadTree().catch(() => {})
      router.go({ name: "browse" })
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    }
  }

  /* ------------------------------------------------ step 3: safety net ---- */

  let spareNumber = $state("999999")
  let sparePassword = $state(generatePassword())
  let spareSaved = $state(false)
  let spareBusy = $state(false)

  /**
   * Sixteen characters from an alphabet with nothing that reads two ways
   * (no 0/O, 1/l/I) and nothing the settings file would need escaping: this
   * is going to be written on paper and typed back in during an emergency.
   */
  function generatePassword(): string {
    const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
    const bytes = new Uint8Array(16)
    crypto.getRandomValues(bytes)
    return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join("")
  }

  async function saveSpare() {
    spareBusy = true
    error = null
    try {
      await api.configureFailsafe(spareNumber.trim(), sparePassword)
      setup = await api.getSetup()
      await go(4)
    } catch (err) {
      error = err instanceof Error ? err.message.replace(/^invalid input: /, "") : String(err)
    } finally {
      spareBusy = false
    }
  }

  /* ------------------------------------------------ step 5: equipment ---- */

  let categoriesOpen = $state(false)
  let assetsOpen = $state(false)
  let bulkOpen = $state(false)
  let printOpen = $state(false)
  let printItems = $state<LabelItem[]>([])
  let examplesBusy = $state(false)
  let examplesNote = $state<string | null>(null)

  function flatten(nodes: CategoryNode[], depth = 0): { id: string; label: string }[] {
    return nodes.flatMap((node) => [
      { id: node.id, label: `${" ".repeat(depth)}${node.name}` },
      ...flatten(node.children, depth + 1),
    ])
  }
  const categoryOptions = $derived(flatten(catalog.tree))

  async function loadExamples() {
    examplesBusy = true
    error = null
    try {
      const r = await api.loadExamples()
      examplesNote =
        `Added ${r.assets.created} example items and ${r.people.created} example people, ` +
        `and the example category tree. You can remove the examples from Admin → Assets whenever ` +
        `you're ready.` + (r.skipped?.length ? " " + r.skipped.join(" ") : "")
      setup = await api.getSetup()
      await catalog.loadTree().catch(() => {})
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      examplesBusy = false
    }
  }

  function afterBulk(made: AssetDetail[]) {
    toast.success(`${made.length} items created`)
    printItems = made.map((u) => ({ id: u.id, name: u.name, serial: u.serial_number ?? "" }))
    printOpen = true
  }

  /* --------------------------------------------------- step 6: people ---- */

  let format = $state<Format>("digits")
  let pattern = $state("")
  let formatBusy = $state(false)
  let formatSaved = $state(false)
  let rosterOpen = $state(false)

  $effect(() => {
    if (step !== 6) return
    api.getSettings().then(
      (s) => {
        format = s.student_number_format
        pattern = s.student_number_pattern
      },
      () => {},
    )
  })

  async function saveFormat() {
    formatBusy = true
    error = null
    try {
      await api.saveSettings({ student_number_format: format, student_number_pattern: pattern })
      formatSaved = true
    } catch (err) {
      error = err instanceof Error ? err.message.replace(/^invalid input: /, "") : String(err)
    } finally {
      formatBusy = false
    }
  }

  /* -------------------------------------------------- step 7: backups ---- */

  let backupDir = $state("")
  let backupDirConfirmed = $state(false)
  let backupBusy = $state(false)
  let backupResult = $state<BackupResult | null>(null)

  $effect(() => {
    if (step !== 7) return
    api.getSettings().then(
      (s) => (backupDir = s.backup_dir),
      () => {},
    )
  })

  async function backUpNow() {
    backupBusy = true
    error = null
    backupResult = null
    try {
      await api.saveSettings({ backup_dir: backupDir.trim() })
      backupResult = await api.backupNow()
    } catch (err) {
      error = err instanceof Error ? err.message.replace(/^invalid input: /, "") : String(err)
    } finally {
      backupBusy = false
    }
  }
</script>

<div class="mx-auto flex w-full max-w-2xl flex-col gap-6 py-4">
  <!-- The progress rail. -->
  <ol class="flex flex-wrap gap-x-4 gap-y-1 text-xs" aria-label="Setup steps">
    {#each STEPS as label, index (label)}
      {@const n = index + 1}
      <li
        class={cn(
          "flex items-center gap-1",
          n === step ? "font-semibold text-fg" : n < step ? "text-fg-muted" : "text-fg-faint",
        )}
        aria-current={n === step ? "step" : undefined}
      >
        {#if n < step}<CheckIcon class="size-3" aria-hidden="true" />{:else}<span>{n}.</span>{/if}
        {label}
      </li>
    {/each}
  </ol>

  {#if !setup}
    <p class="text-fg-muted">{error ?? "Loading…"}</p>
  {:else if step === 1}
    <section class="flex flex-col gap-3">
      <h1 class="text-xl font-semibold text-fg">Welcome to Stockroom.</h1>
      <p class="text-fg-muted">
        This sets up equipment checkout for your department. It takes about twenty minutes, and you
        can stop at any point and pick up where you left off.
      </p>
      <p class="text-fg-muted">
        Here is what we'll do: keep a spare way in, tell Stockroom what equipment you have, add the
        people who can borrow it, print barcode stickers, and turn on automatic backups.
      </p>
      <p class="text-fg-muted">
        Stockroom never sends your data anywhere you haven't set up yourself.
      </p>
      <div><Button onclick={() => go(3)}>Let's go</Button></div>
    </section>
  {:else if step === 3}
    <section class="flex flex-col gap-3">
      <h1 class="text-xl font-semibold text-fg">One thing to write down.</h1>
      <p class="text-fg-muted">
        This is a spare administrator account that Stockroom puts back every time it starts. If a
        screen ever breaks, or the last admin account is deleted by accident, or the records have to
        be restored onto a new computer, this is how you get back in.
      </p>

      {#if setup.failsafe_configured}
        <p class="text-fg">
          A spare account is already set up — it was chosen when Stockroom was installed. Make sure
          you know where its number and password are written down.
        </p>
        <label class="flex items-center gap-2 text-sm">
          <Checkbox checked={spareSaved} onCheckedChange={(v) => (spareSaved = v === true)} />
          I know where the spare account's details are kept
        </label>
        <div><Button disabled={!spareSaved} onclick={() => go(4)}>Continue</Button></div>
      {:else if !setup.failsafe_writable}
        <p class="text-status-overdue">
          Stockroom was started without a settings file, so it can't keep a spare account itself.
          Whoever installed it can add one; until then, don't delete your own account.
        </p>
        <div><Button variant="secondary" onclick={() => go(4)}>Continue anyway</Button></div>
      {:else}
        <div class="grid grid-cols-2 gap-3">
          <div class="flex flex-col gap-1.5">
            <Label for="spare-number">Spare ID number</Label>
            <Input id="spare-number" bind:value={spareNumber} class="font-mono" spellcheck={false} />
            <p class="text-xs text-fg-faint">Any number nobody at school uses.</p>
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="spare-password">Spare password</Label>
            <PasswordInput id="spare-password" bind:value={sparePassword} autocomplete="off" />
            <p class="text-xs text-fg-faint">Made up for you. Change it if you like.</p>
          </div>
        </div>
        <p class="text-fg">
          <strong>Save these somewhere that isn't this computer</strong> — a password manager, or a
          sealed envelope in a drawer. If the computer fails and Stockroom is set up again, this is
          what gets you back into your restored records.
        </p>
        <label class="flex items-center gap-2 text-sm">
          <Checkbox checked={spareSaved} onCheckedChange={(v) => (spareSaved = v === true)} />
          I have saved these somewhere safe
        </label>
        <div>
          <Button disabled={!spareSaved || spareBusy || sparePassword.length < 8} onclick={saveSpare}>
            {spareBusy ? "Saving…" : "Save the spare account"}
          </Button>
        </div>
      {/if}
    </section>
  {:else if step === 4}
    <section class="flex flex-col gap-3">
      <h1 class="text-xl font-semibold text-fg">How lending works.</h1>
      <ul class="flex list-disc flex-col gap-2 pl-5 text-fg-muted">
        <li>
          Students pick a return date when they check out, up to <strong class="text-fg">7 days</strong>
          away.
        </li>
        <li>
          Somebody with something overdue <strong class="text-fg">can't borrow anything else</strong>
          until it comes back. They're told when they sign in. An admin can override it for one
          checkout.
        </li>
        <li>Anybody signed in can scan an item back in, not only the person who borrowed it.</li>
      </ul>
      <p class="text-sm text-fg-faint">These are fixed in this version of Stockroom.</p>
      <div><Button onclick={() => go(5)}>Next</Button></div>
    </section>
  {:else if step === 5}
    <section class="flex flex-col gap-4">
      <h1 class="text-xl font-semibold text-fg">What's in your store cupboard?</h1>
      <p class="text-fg-muted">
        Every camera, lens, light and battery gets its own entry, so you always know which one came
        back. First a list of kinds of equipment, then the items themselves. Use whichever of these
        suits you, and add more any time from Admin → Assets.
      </p>

      <div class="flex flex-col gap-2 rounded-xl border border-line-strong bg-surface p-4">
        <p class="text-sm font-semibold text-fg">Just want to look around first?</p>
        <p class="text-sm text-fg-muted">
          Load a few obviously-fake example cameras and students, so you can try checking something
          out before entering your own. Remove them later with one button.
        </p>
        <div>
          <Button variant="secondary" disabled={examplesBusy || setup.examples_present} onclick={loadExamples}>
            {setup.examples_present ? "Examples loaded" : examplesBusy ? "Loading…" : "Load the examples"}
          </Button>
        </div>
        {#if examplesNote}<p class="text-sm text-fg-muted" role="status">{examplesNote}</p>{/if}
      </div>

      <div class="grid gap-3 sm:grid-cols-2">
        <button
          type="button"
          class="flex flex-col gap-1 rounded-xl border border-line-strong bg-surface p-4 text-left hover:bg-raised"
          onclick={() => (categoriesOpen = true)}
        >
          <span class="text-sm font-semibold text-fg">1. Upload your categories</span>
          <span class="text-xs text-fg-muted">
            Cameras → Bodies → Canon T7i, and so on. A short text file or a spreadsheet.
          </span>
        </button>
        <button
          type="button"
          class="flex flex-col gap-1 rounded-xl border border-line-strong bg-surface p-4 text-left hover:bg-raised"
          onclick={() => (assetsOpen = true)}
        >
          <span class="text-sm font-semibold text-fg">2. Upload a spreadsheet of items</span>
          <span class="text-xs text-fg-muted">If you already have a list. Save it as CSV first.</span>
        </button>
        <button
          type="button"
          class="flex flex-col gap-1 rounded-xl border border-line-strong bg-surface p-4 text-left hover:bg-raised"
          onclick={() => (bulkOpen = true)}
        >
          <span class="text-sm font-semibold text-fg">Add a batch</span>
          <span class="text-xs text-fg-muted">
            Twelve identical batteries, twenty SD cards — we'll number them and print the stickers.
          </span>
        </button>
        <button
          type="button"
          class="flex flex-col gap-1 rounded-xl border border-line-strong bg-surface p-4 text-left hover:bg-raised"
          onclick={() => router.go({ name: "admin", tab: "assets" })}
        >
          <span class="text-sm font-semibold text-fg">Add one by hand</span>
          <span class="text-xs text-fg-muted">Opens Admin → Assets. You'll come back here next time you sign in.</span>
        </button>
      </div>
      <p class="text-sm text-fg-muted">
        <strong class="text-fg">Tip:</strong> plenty of gear already has a unique barcode from the
        factory. Use that as the item's serial number and there's no sticker to print at all.
      </p>
      <div class="flex gap-2">
        <Button onclick={() => go(6)}>Next</Button>
        <Button variant="ghost" onclick={() => go(6)}>I'll do this later</Button>
      </div>
    </section>
  {:else if step === 6}
    <section class="flex flex-col gap-4">
      <h1 class="text-xl font-semibold text-fg">Who can borrow?</h1>
      <div class="flex flex-col gap-2 rounded-xl border border-line-strong bg-surface p-4">
        <p class="text-sm font-semibold text-fg">What do ID numbers look like?</p>
        <StudentNumberFormat idPrefix="setup-sn" bind:format bind:pattern />
        <div class="flex items-center gap-2">
          <Button variant="secondary" disabled={formatBusy} onclick={saveFormat}>
            {formatBusy ? "Saving…" : "Save"}
          </Button>
          {#if formatSaved}<span class="text-sm text-fg-muted" role="status">Saved.</span>{/if}
        </div>
      </div>
      <div class="flex flex-col gap-2 rounded-xl border border-line-strong bg-surface p-4">
        <p class="text-sm font-semibold text-fg">Upload your class list</p>
        <p class="text-sm text-fg-muted">
          A CSV with <code class="font-mono">first_name</code>, <code class="font-mono">last_name</code>
          and <code class="font-mono">student_number</code> columns. Nobody needs a password yet:
          each student picks one the first time they scan their card.
        </p>
        <div><Button variant="secondary" onclick={() => (rosterOpen = true)}>Upload a class list</Button></div>
      </div>
      <p class="text-sm text-fg-muted">
        If your ID cards have no barcode, Admin → Users can print cards with one on.
      </p>
      <div class="flex gap-2">
        <Button onclick={() => go(7)}>Next</Button>
        <Button variant="ghost" onclick={() => go(7)}>I'll do this later</Button>
      </div>
    </section>
  {:else if step === 7}
    <section class="flex flex-col gap-3">
      <h1 class="text-xl font-semibold text-fg">Protect your records.</h1>
      <p class="text-fg-muted">
        Stockroom backs itself up every night, automatically. Choose the folder on this computer
        where those backups go.
      </p>
      <div class="flex flex-col gap-1.5">
        <Label for="setup-backup-dir">Backup folder</Label>
        <Input
          id="setup-backup-dir"
          bind:value={backupDir}
          oninput={() => {
            backupDirConfirmed = false
            backupResult = null
          }}
          class="font-mono"
          spellcheck={false}
        />
        <p class="text-xs text-fg-faint">
          The full path, starting from the top of the disk. Open it on this computer afterwards and
          check the backup is really there.
        </p>
      </div>
      <label class="flex items-center gap-2 text-sm">
        <Checkbox checked={backupDirConfirmed} onCheckedChange={(v) => (backupDirConfirmed = v === true)} />
        That's where I want them, and I know how to find that folder
      </label>
      <div>
        <Button disabled={!backupDirConfirmed || backupBusy || !backupDir.trim()} onclick={backUpNow}>
          {backupBusy ? "Backing up…" : "Run a backup now and show me it worked"}
        </Button>
      </div>
      {#if backupResult}
        <div class="rounded-(--radius) border border-line p-3 text-sm" role="status">
          {#if backupResult.skipped}
            <p class="text-fg">Another backup was already running. Try again in a minute.</p>
          {:else}
            <p class="text-fg">It worked: {backupResult.rows} records saved to</p>
            <p class="font-mono text-xs break-all text-fg-muted">{backupResult.archive}</p>
          {/if}
        </div>
      {/if}
      <p class="text-sm text-fg-muted">
        A copy somewhere else as well — Google Drive or GitHub — means a failed hard drive doesn't
        take your records with it. Set that up in Admin → Settings whenever you're ready.
      </p>
      {#if !backupResult}
        <p class="text-sm text-fg-faint">
          If you skip this, Stockroom will remind everybody who signs in until it's set up.
        </p>
      {/if}
      <div class="flex gap-2">
        <Button onclick={() => go(8)}>{backupResult ? "Next" : "Skip for now"}</Button>
      </div>
    </section>
  {:else}
    <section class="flex flex-col gap-3">
      <h1 class="text-xl font-semibold text-fg">You're ready.</h1>
      <p class="text-fg-muted">
        Next: sign out, scan your own ID card at the sign-in screen and check something out, so
        you've seen what your students will see.
      </p>
      <p class="text-fg-muted">
        Stickers for everything are under Admin → Assets → Print labels. Print at 100%, not
        “fit to page”.
      </p>
      {#if setup.examples_present}
        <p class="text-fg-muted">
          The example items and people are still loaded. Admin → Assets has a button to remove them
          once your real equipment is in.
        </p>
      {/if}
      <div class="flex gap-2">
        <Button onclick={finish}>Open Stockroom</Button>
        <Button variant="ghost" onclick={() => router.go({ name: "admin", tab: "assets" })}>
          Print labels
        </Button>
      </div>
    </section>
  {/if}

  {#if error}
    <p class="text-sm text-status-overdue" role="alert">{error}</p>
  {/if}

  {#if setup && step < STEPS.length + 1}
    <p class="text-xs text-fg-faint">
      Signed in as {session.profile?.full_name ?? "the admin"}.
      <button type="button" class="underline" onclick={finish}>Stop showing this guide</button>
      — you can run it again from Admin → Settings.
    </p>
  {/if}
</div>

<ImportDialog
  bind:open={categoriesOpen}
  title="Upload your categories"
  accept=".md,.txt,.csv,text/markdown,text/plain,text/csv"
  upload={api.importCategories}
  onDone={() => catalog.loadTree().catch(() => {})}
>
  {#snippet description()}
    A text file with one kind of equipment per line, indented under the one it belongs to — or a
    spreadsheet saved as CSV with <code class="font-mono">type,category,model</code> columns.
    Running it twice is safe.
  {/snippet}
  {#snippet report(r: CategoryImportResult)}
    <p class="text-sm text-fg">{r.created} added, {r.existing} were already there.</p>
  {/snippet}
</ImportDialog>

<ImportDialog
  bind:open={assetsOpen}
  title="Upload a spreadsheet of items"
  accept=".csv,text/csv"
  upload={api.importAssets}
>
  {#snippet description()}
    Saved as CSV, with <code class="font-mono">serial_number</code> and
    <code class="font-mono">name</code> columns, and optionally <code class="font-mono">model</code>
    or <code class="font-mono">category</code> naming where each one files.
  {/snippet}
  {#snippet report(r: AssetImportResult)}
    <p class="text-sm text-fg">
      {r.created} added, {r.updated} updated{r.failed ? `, ${r.failed} couldn't be added` : ""}.
    </p>
    <ul class="mt-1 flex flex-col gap-1 text-xs">
      {#each r.rows.filter((row) => row.action === "failed") as row (row.line)}
        <li class="text-status-overdue">Line {row.line}: {row.error}</li>
      {/each}
    </ul>
  {/snippet}
</ImportDialog>

<ImportDialog
  bind:open={rosterOpen}
  title="Upload your class list"
  accept=".csv,text/csv"
  upload={(file) => api.importRoster(file)}
>
  {#snippet description()}
    A CSV with <code class="font-mono">first_name</code>, <code class="font-mono">last_name</code> and
    <code class="font-mono">student_number</code> columns.
  {/snippet}
  {#snippet report(r: RosterResult)}
    <p class="text-sm text-fg">
      {r.created} added, {r.updated} updated{r.failed ? `, ${r.failed} couldn't be added` : ""}.
    </p>
  {/snippet}
</ImportDialog>

<BulkAddDialog bind:open={bulkOpen} categories={categoryOptions} onCreated={afterBulk} />
<PrintLabelsDialog bind:open={printOpen} items={printItems} />
