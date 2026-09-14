# Bundled fonts

Both faces are self-hosted rather than fetched from a CDN, because the closet
PC has no internet for daily operation (`CLAUDE.md` §2) and a CDN link would
silently fall back to system sans — at which point the desktop app and the web
app stop matching.

| File | Family | Licence |
|---|---|---|
| `inter-v19-latin.woff2` | Inter, latin subset, variable weight 100–900 | SIL Open Font License 1.1 — see `OFL-Inter.txt` |
| `jetbrains-mono-latin.woff2` | JetBrains Mono, latin subset, variable weight 400–700 | SIL Open Font License 1.1 — <https://github.com/JetBrains/JetBrainsMono/blob/master/OFL.txt> |

JetBrains Mono came from the Fontsource build
(`@fontsource-variable/jetbrains-mono`, `jetbrains-mono-latin-wght-normal.woff2`).
The declarations that use these files live in `../tokens.css`; nothing else in
the codebase declares an `@font-face`.
