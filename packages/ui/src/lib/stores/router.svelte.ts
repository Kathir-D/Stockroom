/**
 * A hash router, about forty lines of it.
 *
 * The cart is "a route inside this same shell" and someone can "arrive by URL"
 * (design-system.md §8.4), so the app needs real routes — but it has eight
 * screens on one shared machine, and pulling in a routing library to serve them
 * would be more configuration than code. The hash rather than the History API
 * because the Wails webview loads the app from a file-ish origin where pushState
 * paths have nowhere to resolve to.
 */

export type Route =
  | { name: "browse"; category?: string }
  | { name: "cart" }
  | { name: "history" }
  | { name: "admin"; tab: AdminTab }

export type AdminTab = "assets" | "categories" | "users" | "overdue" | "backup" | "settings"

const ADMIN_TABS: AdminTab[] = [
  "assets",
  "categories",
  "users",
  "overdue",
  "backup",
  "settings",
]

function parse(hash: string): Route {
  const path = hash.replace(/^#\/?/, "")
  const [head, ...rest] = path.split("/")
  switch (head) {
    case "cart":
      return { name: "cart" }
    case "history":
      return { name: "history" }
    case "admin": {
      const tab = rest[0]
      return { name: "admin", tab: ADMIN_TABS.includes(tab as AdminTab) ? (tab as AdminTab) : "assets" }
    }
    case "browse":
      return { name: "browse", category: rest[0] ? decodeURIComponent(rest[0]) : undefined }
    default:
      return { name: "browse" }
  }
}

function serialise(route: Route): string {
  switch (route.name) {
    case "cart":
      return "#/cart"
    case "history":
      return "#/history"
    case "admin":
      return `#/admin/${route.tab}`
    case "browse":
      return route.category ? `#/browse/${encodeURIComponent(route.category)}` : "#/browse"
  }
}

class Router {
  current = $state<Route>(
    typeof location === "undefined" ? { name: "browse" } : parse(location.hash)
  )

  /**
   * The category the user was last browsing. "Back to browse" returns to the
   * category they left, not to the root of the tree: someone adding six items
   * from one shelf should not re-navigate the tree after every trip to the cart
   * (design-system.md §8.4).
   */
  lastBrowseCategory = $state<string | undefined>(undefined)

  start(): () => void {
    if (typeof window === "undefined") return () => {}
    const onHashChange = () => {
      this.current = parse(location.hash)
      if (this.current.name === "browse") this.lastBrowseCategory = this.current.category
    }
    window.addEventListener("hashchange", onHashChange)
    onHashChange()
    return () => window.removeEventListener("hashchange", onHashChange)
  }

  go(route: Route) {
    if (route.name === "browse") this.lastBrowseCategory = route.category
    const next = serialise(route)
    if (typeof location === "undefined") {
      this.current = route
      return
    }
    if (location.hash === next) this.current = route
    else location.hash = next
  }

  /** Back to browse, at the category the user left. */
  backToBrowse() {
    this.go({ name: "browse", category: this.lastBrowseCategory })
  }

  href(route: Route) {
    return serialise(route)
  }
}

export const router = new Router()
