/**
 * Who is signed in, and the two facts every screen keys off: whether the
 * session is *limited* (a scan login by an account with no password yet) and
 * whether the user has anything overdue.
 *
 * The server is the authority on both. This store exists so the UI can react
 * without refetching `/me` per component, not so it can make decisions the
 * server doesn't: the overdue block, for instance, is enforced here *and* by
 * `CheckOutAssets` regardless of what this file believes (CLAUDE.md §7).
 */

import * as api from "../api/index"
import type { BackupWarning, CustodyRecord, Profile } from "../api/types"
import { cart } from "./cart.svelte"

class SessionStore {
  profile = $state<Profile | null>(null)
  hasOverdue = $state(false)
  /** A limited session may only set its password, sign out, or ask who it is. */
  needsPassword = $state(false)
  /** The user's own overdue rows, for the sign-in notice and the cart dock. */
  overdueItems = $state<CustodyRecord[]>([])
  /** False until the first `/me` settles, so the shell can hold a blank frame. */
  ready = $state(false)

  /**
   * The backup-staleness sentence the server wants this person to see, or null.
   *
   * It rides on every `/me` and both login responses because sign-in is the one
   * moment everybody passes through (docs/design/backup.md §E.7). The wording is
   * the server's — an admin is told where to go, a student is told who to tell —
   * and this store never rewrites it.
   */
  backupWarning = $state<BackupWarning | null>(null)

  /**
   * Whether this person has dismissed the banner in this session.
   *
   * Kept beside the warning rather than inside the component, because `refresh()`
   * runs after every check-in and would otherwise reopen a banner somebody just
   * closed — which is the behaviour that teaches people to ignore banners. It
   * resets on `clear()`, so the next person at the shared machine sees it again.
   */
  backupWarningDismissed = $state(false)

  /** The warning to actually render: absent once dismissed. */
  get visibleBackupWarning() {
    return this.backupWarningDismissed ? null : this.backupWarning
  }

  dismissBackupWarning() {
    this.backupWarningDismissed = true
  }

  get signedIn() {
    return this.profile !== null
  }

  get isAdmin() {
    return this.profile?.is_admin === true
  }

  /** The name to put in the top-bar chip: first name, or whatever exists. */
  get displayName() {
    const p = this.profile
    if (!p) return ""
    return p.first_name ?? p.full_name ?? p.student_number ?? "Signed in"
  }

  /**
   * Whether the UI should refuse to build a cart. Belt to the server's
   * suspenders: the checkout button disables itself the instant an overdue user
   * signs in, and the server refuses anyway (design-system.md §8.4, §15 Q6).
   */
  get checkoutBlocked() {
    return this.hasOverdue
  }

  /**
   * Restore a session held in sessionStorage across a page reload. A reload is
   * explicitly *not* a sign-out: the cart survives it too (§15 Q5).
   */
  async restore() {
    if (!api.getToken()) {
      this.ready = true
      return
    }
    try {
      const result = await api.me()
      this.adoptProfile(result.profile, result.has_overdue, result.backup_warning)
      // `/me` answers for a limited session too, so the token being valid does
      // not by itself mean the session is full. A 403 from the first full-only
      // read is what reveals that; ask for the tree to find out now rather than
      // halfway through browse.
      try {
        await api.categoryTree()
        this.needsPassword = false
      } catch (error) {
        this.needsPassword = error instanceof api.ApiError && error.needsPassword
        if (!this.needsPassword) throw error
      }
      await this.refreshOverdue()
    } catch {
      // An expired or rejected token is not an error worth surfacing on a cold
      // start; it just means nobody is signed in.
      this.clear()
    } finally {
      this.ready = true
    }
  }

  /** Adopt a fresh login response. */
  async adopt(result: {
    profile: Profile
    has_overdue: boolean
    needs_password?: boolean
    backup_warning?: BackupWarning | null
  }) {
    this.adoptProfile(result.profile, result.has_overdue, result.backup_warning ?? null)
    this.needsPassword = result.needs_password === true
    if (!this.needsPassword) await this.refreshOverdue()
  }

  private adoptProfile(
    profile: Profile,
    hasOverdue: boolean,
    backupWarning: BackupWarning | null = null
  ) {
    this.profile = profile
    this.hasOverdue = hasOverdue
    this.backupWarning = backupWarning
  }

  /** Called after `setInitialPassword` upgrades a limited token in place. */
  async completePasswordSetup() {
    this.needsPassword = false
    const result = await api.me()
    this.adoptProfile(result.profile, result.has_overdue, result.backup_warning)
    await this.refreshOverdue()
  }

  /**
   * Load the user's own overdue rows.
   *
   * Not from `/custody/overdue`, which is the admin roster of who has what and
   * refuses a non-admin (CLAUDE.md §13, 2026-09-12). A user's own history is the
   * read they are allowed, and the overdue flag on it is the same definition —
   * both come from the `overdue_custody` view.
   */
  async refreshOverdue() {
    const id = this.profile?.id
    if (!id) {
      this.overdueItems = []
      return
    }
    try {
      const history = await api.userHistory(id)
      this.overdueItems = history.filter((row) => row.overdue)
      // `has_overdue` from the server stays authoritative; this only fills in
      // *which* items, so a disagreement resolves toward blocking.
      this.hasOverdue = this.hasOverdue || this.overdueItems.length > 0
    } catch {
      // A failed read must not silently unblock checkout, so leave hasOverdue
      // alone and let the server refuse if it comes to that.
      this.overdueItems = []
    }
  }

  /** Re-read `/me`, cheap enough to call after any custody change. */
  async refresh() {
    if (!this.signedIn) return
    const result = await api.me()
    this.adoptProfile(result.profile, result.has_overdue, result.backup_warning)
    await this.refreshOverdue()
  }

  async signOut() {
    await api.logout()
    this.clear()
  }

  /**
   * Drop everything local. Sign-out and the idle-timeout 401 are the only two
   * things that empty the cart (design-system.md §8.4, §15 Q5), and both land
   * here.
   */
  clear() {
    this.profile = null
    this.hasOverdue = false
    this.needsPassword = false
    this.overdueItems = []
    this.backupWarning = null
    this.backupWarningDismissed = false
    api.setToken(null)
    cart.clear()
  }
}

export const session = new SessionStore()
