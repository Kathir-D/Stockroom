<script lang="ts">
  /**
   * The backup-staleness banner (docs/design/backup.md §E.7, surface 1 and 2).
   *
   * A backup system fails silently by default, so the fact that it has stopped
   * has to arrive somewhere everybody already looks. Sign-in is the one moment
   * every person at this machine passes through, which is why the warning rides
   * on `LoginResult`/`MeResult` beside `has_overdue` rather than living on the
   * backup screen alone — that screen is admin-only, and the admin is the person
   * least likely to be standing at the closet PC.
   *
   * Two audiences, one fact. The *server* writes both sentences: an admin is
   * told where to go, a student is told which admin to mention it to, by name.
   * Nothing here rewrites them, and nothing here decides who sees which — the
   * warning that arrived is the warning that renders. The only thing this
   * component adds for an admin is the button, because the destination is a
   * route the student cannot reach anyway.
   *
   * Not a toast. A toast disappears on its own, and the whole failure mode being
   * guarded against is something going unnoticed. Not a modal either: a stale
   * backup is not a reason to stop a student borrowing a camera, and a dialog in
   * front of the browse screen would train everybody to dismiss it unread.
   */
  import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert"
  import XIcon from "@lucide/svelte/icons/x"
  import { Button } from "@stockroom/ui/components/ui/button"
  import type { BackupWarning } from "../../api/types"

  let {
    warning,
    isAdmin = false,
    actionLabel = "Open Backup",
    onOpenBackup,
    onDismiss,
  }: {
    warning: BackupWarning
    isAdmin?: boolean
    /** The admin's button. The closet camera's notice reuses this banner. */
    actionLabel?: string
    onOpenBackup?: () => void
    onDismiss: () => void
  } = $props()
</script>

<!-- `role="status"`, not `alert`: a screen reader should hear this when it gets
     to it, not have the current sentence interrupted. Nothing here is urgent to
     the second, and an assertive live region on a screen somebody scans into
     would talk over the scan result.

     Hue sits on the icon alone, never as a fill: §1.2 reserves the five status
     hues for asset state, and a panel washed amber would read as an asset that
     is due soon. The icon plus the sentence is the same pattern <OverdueNotice>
     uses. -->
<div
  role="status"
  class="mb-(--gutter) flex items-start gap-3 rounded-xl border border-line-strong bg-surface p-3"
>
  <AlertTriangleIcon class="mt-0.5 size-4 shrink-0 text-status-due-soon" aria-hidden="true" />

  <!-- The whole sentence, and only the sentence. `warning.admins` is
       deliberately not rendered beside it: the server has already folded those
       names into the message a student sees ("Please tell Admin Admin or Test
       User."), and printing them again underneath is the same fact twice in two
       different wordings. The field stays on the wire because it is the
       structured form of what the sentence says, and because it is names only —
       never a student number, which is a working scan login (CLAUDE.md §7). -->
  <p class="min-w-0 flex-1 text-sm text-fg">{warning.message}</p>

  {#if isAdmin && onOpenBackup}
    <Button variant="secondary" size="sm" onclick={onOpenBackup}>{actionLabel}</Button>
  {/if}

  <Button variant="ghost" size="icon-sm" aria-label="Dismiss this notice" onclick={onDismiss}>
    <XIcon aria-hidden="true" />
  </Button>
</div>
