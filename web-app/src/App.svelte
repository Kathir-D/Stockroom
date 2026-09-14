<script lang="ts">
  /**
   * The web app, in full.
   *
   * Every screen lives in `@stockroom/ui`, so mirroring the desktop app here is
   * one import rather than a rewrite (design-system.md §14, step 10). If this
   * file ever grows a screen, something has leaked out of the package and the two
   * hosts have started to drift.
   *
   * `VITE_API_BASE_URL` exists only for the dev loop, where Vite serves on :5173
   * and the Go server is on :8080. In the deployed localhost-only setup the
   * default is already right (CLAUDE.md §3).
   */
  import { StockroomApp } from '@stockroom/ui'

  const baseUrl = import.meta.env.VITE_API_BASE_URL as string | undefined
</script>

<StockroomApp {...baseUrl ? { baseUrl } : {}} />
