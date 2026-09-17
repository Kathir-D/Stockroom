/**
 * The report from the last restore, held outside the screen that started it.
 *
 * A restore replaces every row in the database, so the server drops **every**
 * live session the moment it commits — including the admin's, whose token now
 * points at a profile row that may not exist any more. That means the backup
 * screen is unmounted a beat after the restore succeeds, and a result rendered
 * inside it disappears with it: the admin presses Restore, the app returns to
 * the sign-in screen, and nothing ever says whether it worked.
 *
 * So the result lives here, and `app.svelte` renders it at the root, above the
 * sign-in screen as readily as above the shell. It is the one piece of state in
 * the app that deliberately outlives a sign-out.
 */

import type { RestoreResult } from "../api/types"

class RestoreReportStore {
  result = $state<RestoreResult | null>(null)

  show(result: RestoreResult) {
    this.result = result
  }

  dismiss() {
    this.result = null
  }
}

export const restoreReport = new RestoreReportStore()
