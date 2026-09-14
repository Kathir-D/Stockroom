<script lang="ts">
  /**
   * The three-level `Type → Category → Model` filter (design-system.md §5.2).
   *
   * Recursive rather than three nested loops, because branches legitimately stop
   * short of depth 3: `Primes` is seeded with no models at all, since
   * `Catagories.md` records none in inventory (CLAUDE.md §6.2). A hand-unrolled
   * three-level render has to special-case that; this doesn't.
   *
   * Sibling order is the server's, which is `sort_order` — `Catagories.md`'s
   * document order, not alphabetical (CLAUDE.md §13, 2026-09-13). Nothing here
   * sorts anything.
   */
  import ChevronRightIcon from "@lucide/svelte/icons/chevron-right"
  import { cn } from "@stockroom/ui/utils"
  import type { CategoryNode } from "../../api/types"
  import Self from "./category-tree.svelte"

  let {
    nodes,
    selected,
    onSelect,
    /** Ids on the path to the selection, so the tree opens to it after a reload. */
    openPath = [],
    depth = 0,
  }: {
    nodes: CategoryNode[]
    selected?: string
    onSelect: (id: string | undefined) => void
    openPath?: string[]
    depth?: number
  } = $props()

  // Expansion is local per level. Seeded from openPath so arriving at
  // #/browse/<model-id> by URL reveals where that model lives.
  let expanded = $state<Record<string, boolean>>(
    Object.fromEntries(openPath.map((id) => [id, true]))
  )

  function toggle(id: string) {
    expanded = { ...expanded, [id]: !expanded[id] }
  }
</script>

<ul class={cn("flex flex-col", depth === 0 && "gap-px")} role={depth === 0 ? "tree" : "group"}>
  {#each nodes as node (node.id)}
    {@const hasChildren = node.children.length > 0}
    {@const isOpen = expanded[node.id] ?? openPath.includes(node.id)}
    <li role="none">
      <div class="flex items-stretch">
        {#if hasChildren}
          <button
            type="button"
            onclick={() => toggle(node.id)}
            aria-label={`${isOpen ? "Collapse" : "Expand"} ${node.name}`}
            class="flex w-5 shrink-0 items-center justify-center rounded-sm text-fg-faint hover:text-fg"
          >
            <ChevronRightIcon
              class={cn(
                "size-3.5 transition-transform duration-(--dur-fast) ease-(--ease-brand)",
                isOpen && "rotate-90"
              )}
              aria-hidden="true"
            />
          </button>
        {:else}
          <span class="w-5 shrink-0" aria-hidden="true"></span>
        {/if}

        <button
          type="button"
          role="treeitem"
          aria-selected={selected === node.id}
          aria-expanded={hasChildren ? isOpen : undefined}
          onclick={() => onSelect(node.id)}
          class={cn(
            "flex min-h-(--tap) flex-1 items-center rounded-sm px-2 text-left",
            "transition-colors duration-(--dur-fast) ease-(--ease-brand) hover:bg-raised",
            depth === 0 ? "font-medium text-fg" : "text-fg-muted hover:text-fg",
            selected === node.id && "bg-raised text-fg"
          )}
        >
          <span class="truncate">{node.name}</span>
        </button>
      </div>

      {#if hasChildren && isOpen}
        <div class="ml-3 border-l border-line pl-1">
          <Self
            nodes={node.children}
            {selected}
            {onSelect}
            {openPath}
            depth={depth + 1}
          />
        </div>
      {/if}
    </li>
  {/each}
</ul>
