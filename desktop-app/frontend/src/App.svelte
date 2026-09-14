<script lang="ts">
  /**
   * The Wails desktop app, in full.
   *
   * Wails' Go side is a window host and does not touch the database (CLAUDE.md
   * §3); this frontend talks to the same localhost Go server the web app does,
   * through the same components. The only deliberate differences between the two
   * hosts are the ones §12 lists.
   *
   * The webview is a separate origin from 127.0.0.1:8080, which is why the API
   * client sends a bearer token rather than relying on the session cookie.
   */
  import { StockroomApp } from '@stockroom/ui'

  const baseUrl = import.meta.env.VITE_API_BASE_URL as string | undefined
</script>

<StockroomApp {...baseUrl ? { baseUrl } : {}} />
