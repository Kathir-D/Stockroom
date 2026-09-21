<script lang="ts">
  /**
   * The 236px left sidebar (design-system.md §7.1): the category tree first,
   * then an `Admin` group visible only when `is_admin`.
   *
   * Admin is a group inside this same sidebar rather than a separate shell,
   * because the admin panel "is not a separate app, not a separate theme" (§8.7).
   * The only thing that changes over there is the density.
   *
   * Below 720px the shell renders this inside a `sheet` instead (§7.2). That is
   * the shell's decision, not this component's: everything here is the same
   * markup either way, so the two can't drift.
   */
  import BoxesIcon from "@lucide/svelte/icons/boxes"
  import PackageIcon from "@lucide/svelte/icons/package"
  import FolderTreeIcon from "@lucide/svelte/icons/folder-tree"
  import UsersIcon from "@lucide/svelte/icons/users"
  import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert"
  import DatabaseBackupIcon from "@lucide/svelte/icons/database-backup"
  import SettingsIcon from "@lucide/svelte/icons/settings"
  import ImagesIcon from "@lucide/svelte/icons/images"
  import { Separator } from "@stockroom/ui/components/ui/separator"
  import { cn } from "@stockroom/ui/utils"
  import type { CategoryNode } from "../../api/types"
  import type { AdminTab, Route } from "../../stores/router.svelte"
  import CategoryTree from "./category-tree.svelte"

  let {
    tree,
    selectedCategory,
    route,
    isAdmin = false,
    /** Ids on the path to the selection, so the tree opens to it after a reload. */
    openPath = [],
    onSelectCategory,
    onNavigate,
  }: {
    tree: CategoryNode[]
    selectedCategory?: string
    route: Route
    isAdmin?: boolean
    openPath?: string[]
    onSelectCategory: (id: string | undefined) => void
    onNavigate: (route: Route) => void
  } = $props()

  const ADMIN_ITEMS: { tab: AdminTab; label: string; icon: typeof BoxesIcon }[] = [
    { tab: "assets", label: "Assets", icon: BoxesIcon },
    { tab: "categories", label: "Categories", icon: FolderTreeIcon },
    { tab: "users", label: "Users", icon: UsersIcon },
    { tab: "overdue", label: "Overdue", icon: AlertTriangleIcon },
    { tab: "backup", label: "Backup", icon: DatabaseBackupIcon },
    { tab: "settings", label: "Settings", icon: SettingsIcon },
    // Last rather than directly under Backup, which is where
    // docs/design/signin-photo-wall.html §7 put it: Backup and Settings are one
    // subject read top to bottom, and the photo wall is a different one. It is
    // also the only item here that is decoration rather than inventory, so it
    // belongs at the bottom of the list on those grounds too.
    { tab: "photo-wall", label: "Photo wall", icon: ImagesIcon },
  ]

  const browsing = $derived(route.name === "browse")
</script>

<nav
  class="flex h-full w-full flex-col gap-2 overflow-y-auto bg-surface p-3"
  aria-label="Categories and admin"
>
  <button
    type="button"
    onclick={() => onSelectCategory(undefined)}
    class={cn(
      "flex min-h-(--tap) items-center rounded-(--radius) px-2 text-left text-sm font-medium",
      "transition-colors duration-(--dur-fast) hover:bg-raised",
      browsing && selectedCategory === undefined ? "bg-raised text-fg" : "text-fg-muted"
    )}
  >
    All equipment
  </button>

  <CategoryTree
    nodes={tree}
    selected={browsing ? selectedCategory : undefined}
    {openPath}
    onSelect={onSelectCategory}
  />

  <!-- Kits sit under the tree rather than inside it: a kit is a bundle of units
       that may come from four different Types, so it has no place in a tree
       whose whole meaning is where a unit files (CLAUDE.md §6.2). Visible to
       everyone, because taking a kit out is a student action. -->
  <button
    type="button"
    onclick={() => onNavigate({ name: "kits" })}
    aria-current={route.name === "kits" ? "page" : undefined}
    class={cn(
      "flex min-h-(--tap) items-center gap-2 rounded-(--radius) px-2 text-left text-sm",
      "transition-colors duration-(--dur-fast) hover:bg-raised",
      route.name === "kits" ? "bg-raised text-fg" : "text-fg-muted hover:text-fg"
    )}
  >
    <PackageIcon class="size-4 shrink-0" aria-hidden="true" />
    Kits
  </button>

  {#if isAdmin}
    <Separator class="my-1" />
    <h2 class="px-2 text-[11px] font-semibold tracking-wide text-fg-faint uppercase">
      Admin
    </h2>
    <ul class="flex flex-col gap-px">
      {#each ADMIN_ITEMS as item (item.tab)}
        {@const Icon = item.icon}
        {@const active = route.name === "admin" && route.tab === item.tab}
        <li>
          <button
            type="button"
            onclick={() => onNavigate({ name: "admin", tab: item.tab })}
            aria-current={active ? "page" : undefined}
            class={cn(
              "flex min-h-(--tap) w-full items-center gap-2 rounded-(--radius) px-2 text-left text-sm",
              "transition-colors duration-(--dur-fast) hover:bg-raised",
              active ? "bg-raised text-fg" : "text-fg-muted hover:text-fg"
            )}
          >
            <Icon class="size-4 shrink-0" aria-hidden="true" />
            {item.label}
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</nav>
