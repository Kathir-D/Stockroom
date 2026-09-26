<script lang="ts">
  /**
   * Choosing a folder by clicking through folders, rather than typing a path
   * or pasting an id.
   *
   * Two sources, one component:
   *
   * - **drive** — Google Drive, through the one Google connection. Starts in
   *   My Drive, and in Shared with me too when `shared` is set (the photo
   *   wall's folder is usually somebody else's). Folders are named by
   *   short-lived handles, never Drive ids (google_admin.go). The pick carries
   *   the handle and the path from My Drive, which is what the backup stores.
   * - **local** — this computer's disks, for the backup folders, which must be
   *   full paths (2026-09-21: a pasted path without its leading slash put a
   *   whole term of backups inside the repository).
   */
  import ArrowLeftIcon from "@lucide/svelte/icons/arrow-left"
  import ChevronRightIcon from "@lucide/svelte/icons/chevron-right"
  import FolderIcon from "@lucide/svelte/icons/folder"
  import FolderPlusIcon from "@lucide/svelte/icons/folder-plus"
  import HardDriveIcon from "@lucide/svelte/icons/hard-drive"
  import UsersIcon from "@lucide/svelte/icons/users"
  import LoaderIcon from "@lucide/svelte/icons/loader-circle"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import { Input } from "@stockroom/ui/components/ui/input"
  import * as api from "../../api/index"
  import type { LocalFolder } from "../../api/types"

  export interface FolderChoice {
    name: string
    /** Local: the full path. Drive: the path from My Drive ("" outside it). */
    path: string
    /** Drive only. */
    handle?: string
  }

  let {
    open = $bindable(false),
    source,
    title,
    description,
    shared = false,
    allowCreate = true,
    pickLabel = "Use this folder",
    onpick,
  }: {
    open?: boolean
    source: "drive" | "local"
    title: string
    description: string
    /** Drive: offer Shared with me beside My Drive. */
    shared?: boolean
    allowCreate?: boolean
    pickLabel?: string
    /** Resolves when the pick is saved; a throw is shown in the dialog. */
    onpick: (choice: FolderChoice) => Promise<void> | void
  } = $props()

  /** Where the picker is: a crumb per folder, the first being a root. */
  type Crumb = { key: string; name: string }
  let trail = $state<Crumb[]>([])
  let folders = $state<{ key: string; name: string }[]>([])
  let places = $state<LocalFolder[]>([])
  let parent = $state("")
  let loading = $state(false)
  let error = $state<string | null>(null)
  let picking = $state(false)
  let newName = $state("")
  let creating = $state(false)
  let showCreate = $state(false)

  const roots = $derived<Crumb[]>(
    source === "drive"
      ? [
          { key: "my-drive", name: "My Drive" },
          ...(shared ? [{ key: "shared", name: "Shared with me" }] : []),
        ]
      : []
  )
  const here = $derived(trail.at(-1))
  /** Inside a real folder, not at a Drive root or the list of roots. */
  const canPick = $derived(source === "local" ? !!here : trail.length >= 2)
  const canCreate = $derived(
    allowCreate && !!here && !(source === "drive" && trail.length === 1 && here.key === "shared")
  )

  $effect(() => {
    if (!open) return
    error = null
    showCreate = false
    if (source === "local") go([], "")
    else if (roots.length === 1) go([roots[0]], roots[0].key)
    else {
      trail = []
      folders = roots.map((r) => ({ key: r.key, name: r.name }))
    }
  })

  async function go(nextTrail: Crumb[], key: string) {
    loading = true
    error = null
    showCreate = false
    try {
      if (source === "local") {
        const list = await api.localFolders(key)
        places = list.places
        parent = list.parent
        folders = list.folders.map((f) => ({ key: f.path, name: f.name }))
        trail = [{ key: list.path, name: list.path }]
      } else {
        const list = await api.googleFolders(key)
        folders = list.map((f) => ({ key: f.handle, name: f.name }))
        trail = nextTrail
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      loading = false
    }
  }

  function enter(folder: { key: string; name: string }) {
    if (source === "local") go([], folder.key)
    else go([...trail, folder], folder.key)
  }

  function back() {
    if (source === "local") {
      if (parent) go([], parent)
      return
    }
    if (trail.length <= 1) {
      if (roots.length > 1) {
        trail = []
        folders = roots.map((r) => ({ key: r.key, name: r.name }))
      }
      return
    }
    const up = trail.slice(0, -1)
    go(up, up.at(-1)!.key)
  }

  const canGoBack = $derived(
    source === "local" ? !!parent : trail.length > 1 || (trail.length === 1 && roots.length > 1)
  )

  function choice(): FolderChoice {
    if (source === "local") return { name: here!.name, path: here!.key }
    const inMyDrive = trail[0]?.key === "my-drive"
    return {
      name: here!.name,
      handle: here!.key,
      path: inMyDrive ? trail.slice(1).map((c) => c.name).join("/") : "",
    }
  }

  async function pick() {
    picking = true
    error = null
    try {
      await onpick(choice())
      open = false
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      picking = false
    }
  }

  async function create() {
    if (!here || !newName.trim()) return
    creating = true
    error = null
    try {
      if (source === "local") {
        const made = await api.createLocalFolder(here.key, newName.trim())
        newName = ""
        await go([], made.path)
      } else {
        const made = await api.createGoogleFolder(here.key, newName.trim())
        newName = ""
        await go([...trail, { key: made.handle, name: made.name }], made.handle)
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      creating = false
    }
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Content class="sm:max-w-xl">
    <Dialog.Header>
      <Dialog.Title>{title}</Dialog.Title>
      <Dialog.Description>{description}</Dialog.Description>
    </Dialog.Header>

    <div class="flex flex-col gap-2">
      {#if source === "local" && places.length > 0}
        <div class="flex flex-wrap gap-1.5">
          {#each places as place (place.path)}
            <Button
              variant={here?.key === place.path ? "secondary" : "ghost"}
              size="xs"
              onclick={() => go([], place.path)}
            >
              {place.name}
            </Button>
          {/each}
        </div>
      {/if}

      <div class="flex min-h-8 items-center gap-1 text-sm text-fg-muted">
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Up one folder"
          disabled={!canGoBack || loading}
          onclick={back}
        >
          <ArrowLeftIcon aria-hidden="true" />
        </Button>
        <p class="truncate" title={source === "local" ? here?.key : undefined}>
          {#if source === "local"}
            {here?.key ?? ""}
          {:else if trail.length === 0}
            Google Drive
          {:else}
            {trail.map((c) => c.name).join(" › ")}
          {/if}
        </p>
      </div>

      <ul
        class="flex h-72 flex-col overflow-y-auto rounded-(--radius-sm) border border-line"
        aria-busy={loading}
      >
        {#if loading}
          <li class="flex items-center gap-2 p-3 text-sm text-fg-muted">
            <LoaderIcon class="size-4 animate-spin" aria-hidden="true" />
            Loading folders…
          </li>
        {:else if folders.length === 0}
          <li class="p-3 text-sm text-fg-faint">
            No folders in here.{canCreate ? " Make one below, or use this one." : ""}
          </li>
        {:else}
          {#each folders as folder (folder.key)}
            <li>
              <button
                type="button"
                class="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-fg hover:bg-raised"
                onclick={() => enter(folder)}
              >
                {#if trail.length === 0 && folder.key === "shared"}
                  <UsersIcon class="size-4 shrink-0 text-fg-muted" aria-hidden="true" />
                {:else if trail.length === 0}
                  <HardDriveIcon class="size-4 shrink-0 text-fg-muted" aria-hidden="true" />
                {:else}
                  <FolderIcon class="size-4 shrink-0 text-fg-muted" aria-hidden="true" />
                {/if}
                <span class="flex-1 truncate">{folder.name}</span>
                <ChevronRightIcon class="size-4 shrink-0 text-fg-faint" aria-hidden="true" />
              </button>
            </li>
          {/each}
        {/if}
      </ul>

      {#if canCreate}
        {#if showCreate}
          <form
            class="flex gap-2"
            onsubmit={(e) => {
              e.preventDefault()
              create()
            }}
          >
            <Input bind:value={newName} placeholder="New folder name" autocomplete="off" />
            <Button type="submit" variant="secondary" disabled={creating || !newName.trim()}>
              {creating ? "Making…" : "Make folder"}
            </Button>
          </form>
        {:else}
          <div>
            <Button variant="ghost" size="sm" onclick={() => (showCreate = true)}>
              <FolderPlusIcon aria-hidden="true" />
              New folder here
            </Button>
          </div>
        {/if}
      {/if}

      {#if error}
        <p class="text-sm text-status-overdue" role="alert">{error}</p>
      {/if}
    </div>

    <Dialog.Footer>
      <Button variant="ghost" onclick={() => (open = false)}>Cancel</Button>
      <Button disabled={!canPick || picking || loading} onclick={pick}>
        {#if picking}
          Saving…
        {:else if canPick}
          {pickLabel}: {here?.name.split(/[\\/]/).filter(Boolean).at(-1) ?? here?.name}
        {:else}
          Open a folder to choose it
        {/if}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
