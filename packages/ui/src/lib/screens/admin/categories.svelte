<script lang="ts">
  /**
   * Admin → Categories (design-system.md §8.7).
   *
   * The three-level `Type → Category → Model` tree with inline rename, add-child
   * and delete. Two server rules shape the screen:
   *
   *  - **Depth is capped at 3.** The UI hides **Add child** on a depth-3 node
   *    rather than letting the server reject the press. The cap is still enforced
   *    in `internal/stockroom`; hiding the control is a courtesy, not the rule.
   *  - **Delete is refused when the node has children or assets.** Say which —
   *    the server's message does, so it is shown as written.
   *
   * Sibling order is `sort_order`, which is `examples/categories.media-department.md`'s document order and
   * is what the browse screen and the filter tree sort on (CLAUDE.md §13). A new
   * node lands after its siblings; the arrows here swap two siblings' values,
   * which is the whole of "a level can be renumbered by hand".
   */
  import ChevronRightIcon from "@lucide/svelte/icons/chevron-right"
  import PlusIcon from "@lucide/svelte/icons/plus"
  import PencilIcon from "@lucide/svelte/icons/pencil"
  import TrashIcon from "@lucide/svelte/icons/trash-2"
  import ArrowUpIcon from "@lucide/svelte/icons/arrow-up"
  import ArrowDownIcon from "@lucide/svelte/icons/arrow-down"
  import { toast } from "svelte-sonner"
  import * as AlertDialog from "@stockroom/ui/components/ui/alert-dialog"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import EmptyState from "@stockroom/ui/components/app/empty-state.svelte"
  import { cn } from "@stockroom/ui/utils"
  import * as api from "../../api/index"
  import type { CategoryNode } from "../../api/types"
  import { catalog } from "../../stores/catalog.svelte"

  /** The tree is three deep by definition; a depth-3 node takes no children. */
  const MAX_DEPTH = 3

  let error = $state<string | null>(null)
  let busy = $state(false)
  let expanded = $state<Record<string, boolean>>({})

  let formOpen = $state(false)
  /** Set when renaming; null when creating a child of `formParent`. */
  let editing = $state<CategoryNode | null>(null)
  let formParent = $state<CategoryNode | null>(null)
  let formName = $state("")
  let formError = $state<string | null>(null)

  let deleteTarget = $state<CategoryNode | null>(null)
  let deleteError = $state<string | null>(null)

  async function reload() {
    error = null
    try {
      await catalog.loadTree()
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    }
  }

  function toggle(id: string) {
    expanded = { ...expanded, [id]: !expanded[id] }
  }

  function openCreate(parent: CategoryNode | null) {
    editing = null
    formParent = parent
    formName = ""
    formError = null
    formOpen = true
    if (parent) expanded = { ...expanded, [parent.id]: true }
  }

  function openRename(node: CategoryNode) {
    editing = node
    formParent = null
    formName = node.name
    formError = null
    formOpen = true
  }

  async function save(event: SubmitEvent) {
    event.preventDefault()
    const name = formName.trim()
    if (!name) {
      formError = "A name is required."
      return
    }
    busy = true
    formError = null
    try {
      if (editing) {
        // parent_id is sent unchanged: an update that omitted it would be a move
        // to the root, and nothing on this screen asks for a move.
        await api.updateCategory(editing.id, { name, parent_id: editing.parent_id })
        toast.success("Category renamed")
      } else {
        await api.createCategory({ name, parent_id: formParent?.id ?? null })
        toast.success("Category created")
      }
      formOpen = false
      await reload()
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err)
    } finally {
      busy = false
    }
  }

  async function confirmDelete() {
    if (!deleteTarget) return
    deleteError = null
    try {
      await api.deleteCategory(deleteTarget.id)
      toast.success("Category deleted")
      deleteTarget = null
      await reload()
    } catch (err) {
      // "has children" / "has assets" — the server names which. Keep the dialog
      // open and print it rather than collapsing both into "couldn't delete".
      deleteError = err instanceof Error ? err.message : String(err)
    }
  }

  /**
   * Swap a node with the sibling above or below it.
   *
   * Two updates rather than one, because `sort_order` is a position among
   * siblings and swapping is the only reorder this screen offers. Duplicates and
   * gaps are harmless server-side, so a half-applied swap degrades to "these two
   * tie and the name breaks it", not to a broken tree.
   */
  async function move(siblings: CategoryNode[], index: number, delta: -1 | 1) {
    const other = siblings[index + delta]
    const node = siblings[index]
    if (!other) return
    busy = true
    try {
      await api.updateCategory(node.id, {
        name: node.name,
        parent_id: node.parent_id,
        sort_order: other.sort_order,
      })
      await api.updateCategory(other.id, {
        name: other.name,
        parent_id: other.parent_id,
        sort_order: node.sort_order,
      })
      await reload()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      busy = false
    }
  }
</script>

{#snippet level(nodes: CategoryNode[], depth: number)}
  <ul class="flex flex-col gap-px">
    {#each nodes as node, index (node.id)}
      {@const hasChildren = node.children.length > 0}
      {@const isOpen = expanded[node.id] ?? false}
      <li>
        <div
          class="group flex min-h-(--row-h) items-center gap-1 rounded-(--radius) pr-1 transition-colors duration-(--dur-fast) hover:bg-raised"
        >
          {#if hasChildren}
            <button
              type="button"
              onclick={() => toggle(node.id)}
              aria-expanded={isOpen}
              aria-label={`${isOpen ? "Collapse" : "Expand"} ${node.name}`}
              class="flex w-6 shrink-0 items-center justify-center text-fg-faint hover:text-fg"
            >
              <ChevronRightIcon
                class={cn("size-3.5 transition-transform duration-(--dur-fast)", isOpen && "rotate-90")}
                aria-hidden="true"
              />
            </button>
          {:else}
            <span class="w-6 shrink-0" aria-hidden="true"></span>
          {/if}

          <span class={cn("flex-1 truncate text-sm", depth === 0 ? "font-medium text-fg" : "text-fg-muted")}>
            {node.name}
          </span>

          <span class="font-mono text-[11px] text-fg-faint tabular-nums">{node.sort_order}</span>

          <div class="flex items-center gap-0.5 opacity-0 transition-opacity duration-(--dur-fast) group-hover:opacity-100 focus-within:opacity-100">
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={`Move ${node.name} up`}
              disabled={busy || index === 0}
              onclick={() => move(nodes, index, -1)}
            >
              <ArrowUpIcon aria-hidden="true" />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={`Move ${node.name} down`}
              disabled={busy || index === nodes.length - 1}
              onclick={() => move(nodes, index, 1)}
            >
              <ArrowDownIcon aria-hidden="true" />
            </Button>
            <!-- Hidden, not disabled-and-rejected, at depth 3 (§8.7). -->
            {#if depth + 1 < MAX_DEPTH}
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`Add a child of ${node.name}`}
                onclick={() => openCreate(node)}
              >
                <PlusIcon aria-hidden="true" />
              </Button>
            {/if}
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={`Rename ${node.name}`}
              onclick={() => openRename(node)}
            >
              <PencilIcon aria-hidden="true" />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={`Delete ${node.name}`}
              onclick={() => {
                deleteTarget = node
                deleteError = null
              }}
            >
              <TrashIcon class="text-destructive" aria-hidden="true" />
            </Button>
          </div>
        </div>

        {#if hasChildren && isOpen}
          <div class="ml-3 border-l border-line pl-1">
            {@render level(node.children, depth + 1)}
          </div>
        {/if}
      </li>
    {/each}
  </ul>
{/snippet}

<div data-density="compact" class="flex flex-col gap-3">
  <div class="flex items-center gap-3">
    <div class="flex flex-col">
      <h1 class="text-base font-semibold text-fg">Categories</h1>
      <p class="text-xs text-fg-muted">
        Type → Category → Model, three levels. Order here is the order the browse list uses.
      </p>
    </div>
    <span class="flex-1"></span>
    <Button onclick={() => openCreate(null)}>
      <PlusIcon aria-hidden="true" />
      New type
    </Button>
  </div>

  {#if error}
    <EmptyState title="Couldn't load the category tree" description={error}>
      {#snippet action()}
        <Button variant="secondary" onclick={reload}>Try again</Button>
      {/snippet}
    </EmptyState>
  {:else if catalog.tree.length === 0}
    <EmptyState
      title="No categories yet"
      description="Create a Type first; Categories and Models hang off it."
    >
      {#snippet action()}
        <Button variant="secondary" onclick={() => openCreate(null)}>New type</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <div class="rounded-xl border border-line-strong p-2">
      {@render level(catalog.tree, 0)}
    </div>
  {/if}
</div>

<Dialog.Root bind:open={formOpen}>
  <Dialog.Content>
    <form onsubmit={save} class="flex flex-col gap-3">
      <Dialog.Header>
        <Dialog.Title>
          {editing ? `Rename ${editing.name}` : formParent ? `New child of ${formParent.name}` : "New type"}
        </Dialog.Title>
        <Dialog.Description>
          Names are unique across the whole tree, not just among siblings, so generic names are worth
          avoiding.
        </Dialog.Description>
      </Dialog.Header>

      <div class="flex flex-col gap-1.5">
        <Label for="category-name">Name</Label>
        <Input id="category-name" bind:value={formName} required />
      </div>

      {#if formError}
        <p class="text-status-overdue" role="alert">{formError}</p>
      {/if}

      <Dialog.Footer>
        <Button type="button" variant="ghost" onclick={() => (formOpen = false)}>Cancel</Button>
        <Button type="submit" disabled={busy}>{busy ? "Saving…" : "Save"}</Button>
      </Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>

<AlertDialog.Root open={deleteTarget !== null} onOpenChange={(open) => !open && (deleteTarget = null)}>
  <AlertDialog.Content>
    <AlertDialog.Header>
      <AlertDialog.Title>Delete {deleteTarget?.name}?</AlertDialog.Title>
      <AlertDialog.Description>
        A category with children or with assets filed under it is refused by the server. Move or
        delete those first.
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
