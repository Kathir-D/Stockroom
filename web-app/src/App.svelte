<script lang="ts">
  /**
   * The web app, in full.
   *
   * Every screen lives in `@stockroom/ui`, so mirroring the desktop app here is
   * one import rather than a rewrite (design-system.md §14, step 10). If this
   * file ever grows a screen, something has leaked out of the package and the two
   * hosts have started to drift.
   *
   * The base URL is decided three ways, in order:
   *
   *  1. `VITE_API_BASE_URL`, an explicit override for anybody pointing a build
   *     at a server somewhere else.
   *  2. In a production build, `window.location.origin` — because the Go binary
   *     serves this bundle itself (web-app/embed.go), so the API is already
   *     wherever the page came from. Reading it off the location rather than
   *     hardcoding a port is what makes `SERVER_ADDR` stop being load-bearing,
   *     and it means the CORS allow-list never comes into it: a same-origin
   *     request is not subject to CORS at all.
   *  3. In the dev loop, the package default — Vite serves on :5173 and the Go
   *     server is a separate origin on :8080, which is exactly what the
   *     allow-list in `server/router.go` exists for.
   */
  import { StockroomApp } from '@stockroom/ui'

  const override = import.meta.env.VITE_API_BASE_URL as string | undefined
  const baseUrl =
    override ?? (import.meta.env.PROD ? window.location.origin : undefined)
</script>

<StockroomApp {...baseUrl ? { baseUrl } : {}} />
