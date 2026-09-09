# Stockroom design system

The single reference for how Stockroom looks and behaves on screen, in both the Wails desktop app and
the web app. `CLAUDE.md` is the architecture reference and `TODO.md` is the backend work list; this file
is the UI counterpart to both. Where the two disagree about behaviour, `CLAUDE.md` wins and this file
should be corrected.

Written 2026-09-09. Direction chosen from `docs/design/mood-board.html` (open it with
`python3 -m http.server 8899` inside `docs/design/`, then visit `/mood-board.html`).

**Settled:** ground `A2` "Studio Ops" (warm charcoal dark) · component library **shadcn-svelte v1** over
Bits UI · shared **npm workspace package** · cart as a **dock that expands into a drawer** · status as
**dot + label** · **Inter + mono for identifiers**.

**Not settled:** seven user-flow questions in §15. Everything in §1 to §14 holds regardless of how they
land; where a section depends on one, it says so.

---

## 1. Principles

These are ordered. When two conflict, the earlier one wins.

1. **Legibility beats beauty.** The machine lives in a camera closet under fluorescent light and gets used
   in ten-second bursts by someone holding two lenses. If a choice makes something prettier and slower to
   read, it loses.
2. **Hue means state, nothing else.** Green, blue, amber, red and grey are reserved for
   `available` / `checked_out` / due-soon / overdue / `unavailable`. Nothing decorative is ever coloured.
   Primary buttons are achromatic, near-white on the dark ground. This is the single rule that makes the
   app read as a tool rather than a template, and it is why the existing `#038940` brand green is being
   demoted (§3.2).
3. **Colour is never the only signal.** Every status carries its word. Every destructive action carries an
   icon and a verb. A colour-blind user and a greyscale printout must both work.
4. **One component set, two hosts.** Anything that renders differently in Wails than in the browser is a
   bug unless §12 lists it as a deliberate difference.
5. **Density is a setting, not a redesign.** The same components serve a 36px admin table row and a 72px
   kiosk touch target, driven by CSS variables (§4).
6. **Nothing moves that doesn't have to.** Motion exists to explain a state change, never to decorate.

---

## 2. Repository setup

### 2.1 Current state

| | `desktop-app/frontend` | `web-app` |
|---|---|---|
| Vite | `^7.0.0` | `^8.2.2` |
| `@sveltejs/vite-plugin-svelte` | `^6.0.0` | `^7.3.0` |
| Svelte | `^5.55.7` | `^5.56.10` |
| Tailwind | `4.3.3` via `@tailwindcss/vite` | `4.3.3` via `@tailwindcss/vite` |
| TS config | `tsconfig.json` | `tsconfig.app.json` + `tsconfig.node.json` |
| Tests | Vitest + Testing Library | none |
| Tokens today | full "Nocturne" `@theme` block | two variables (`--color-brand`) |

Two different Vite majors is the main integration hazard. **Align `desktop-app/frontend` up to Vite `^8`
and `@sveltejs/vite-plugin-svelte` `^7` before creating the workspace.** Sharing Svelte source across two
Vite majors works until it doesn't, and the failure mode (duplicate Svelte runtime, components that render
but whose state never updates) is miserable to debug.

### 2.2 Workspace layout

```
stockroom/
├── package.json                 # NEW: { "private": true, "workspaces": ["packages/*", "web-app", "desktop-app/frontend"] }
├── packages/ui/                 # NEW: @stockroom/ui, the design system
│   ├── package.json
│   ├── components.json          # shadcn-svelte config, lives here not in the apps
│   └── src/lib/
│       ├── styles/tokens.css    # §3, the single source of truth for every token
│       ├── components/ui/       # shadcn-svelte generated components
│       ├── components/app/      # Stockroom-specific components (§5.2)
│       ├── hooks/
│       └── utils.ts             # cn() helper
├── web-app/
└── desktop-app/frontend/
```

`packages/ui/package.json`:

```json
{
  "name": "@stockroom/ui",
  "version": "0.0.0",
  "private": true,
  "type": "module",
  "svelte": "./src/lib/index.ts",
  "exports": {
    ".": { "svelte": "./src/lib/index.ts", "default": "./src/lib/index.ts" },
    "./components/*": { "svelte": "./src/lib/components/*", "default": "./src/lib/components/*" },
    "./styles/*": "./src/lib/styles/*",
    "./utils": { "svelte": "./src/lib/utils.ts", "default": "./src/lib/utils.ts" }
  },
  "dependencies": {
    "bits-ui": "^2",
    "tailwind-variants": "^3",
    "tailwind-merge": "^3",
    "clsx": "^2",
    "@lucide/svelte": "^0.5"
  },
  "peerDependencies": { "svelte": "^5", "tailwindcss": "^4" }
}
```

**No build step.** The package ships raw `.svelte` and `.ts`; each app's Vite compiles it. That keeps
hot-reload working across the boundary and avoids a watch-build in the dev loop.

Both apps then need, in `vite.config.ts`:

```ts
export default defineConfig({
  plugins: [tailwindcss(), svelte()],
  resolve: {
    alias: { $lib: path.resolve('./src/lib') },
    dedupe: ['svelte'],                    // MANDATORY, two Svelte copies breaks reactivity silently
  },
  optimizeDeps: { exclude: ['@stockroom/ui'] },  // it's source, not a prebuilt dep
})
```

and in `tsconfig` (`tsconfig.app.json` for web-app, `tsconfig.json` for desktop):

```json
{ "compilerOptions": { "baseUrl": ".", "paths": { "$lib": ["./src/lib"], "$lib/*": ["./src/lib/*"] } } }
```

### 2.3 Installing shadcn-svelte

Both apps already have Tailwind v4 wired through `@tailwindcss/vite`, so **skip the `sv add tailwindcss`
step** in the official guide. Run the CLI **inside `packages/ui`**, not inside either app:

```
cd packages/ui
npx shadcn-svelte@latest init
npx shadcn-svelte@latest add button dialog table input label select checkbox \
    dropdown-menu tooltip sheet sonner badge separator scroll-area \
    alert-dialog popover calendar tabs skeleton avatar command
```

The critical part is `components.json`. Set the aliases to **package-absolute paths**, so generated
components import each other via `@stockroom/ui/...` and resolve identically from both apps. If you leave
the default `$lib` aliases, generated code will resolve to whichever *app* is importing it and break:

```json
{
  "$schema": "https://shadcn-svelte.com/schema.json",
  "tailwind": { "css": "src/lib/styles/tokens.css", "baseColor": "zinc" },
  "aliases": {
    "components": "@stockroom/ui/components",
    "ui": "@stockroom/ui/components/ui",
    "utils": "@stockroom/ui/utils",
    "hooks": "@stockroom/ui/hooks",
    "lib": "@stockroom/ui"
  },
  "typescript": true,
  "registry": "https://shadcn-svelte.com/registry"
}
```

After `add`, **read every generated component before shipping it.** They are source in your repo, not a
dependency; the whole point is that you edit them. Expect to change the `Button` variants (§5.1) and the
`Table` row height (§4) immediately.

### 2.4 Each app's `app.css`

Both apps' `src/app.css` collapse to four lines. Everything else moves into the package.

```css
@import "tailwindcss";
@import "tw-animate-css";
@import "@stockroom/ui/styles/tokens.css";
@source "../../packages/ui/src";   /* Tailwind v4 must scan the package for class names */
```

The `@source` line is easy to forget and the failure is confusing: components render with no styling at
all, because Tailwind never saw their class names during the content scan. Adjust the relative path per
app (`web-app/src/app.css` → `../../packages/ui/src`; `desktop-app/frontend/src/app.css` →
`../../../packages/ui/src`).

`desktop-app/frontend/src/app.css` also keeps its `@font-face` blocks, since the font files live
under `desktop-app/frontend/src/assets/fonts/`. **Move those font files into
`packages/ui/src/lib/styles/fonts/` and declare the faces once in `tokens.css`.** Otherwise the web app
either refetches Inter from a CDN (breaking the offline requirement in `CLAUDE.md` §2) or silently falls
back to system sans and the two apps stop matching.

---

## 3. Tokens

`packages/ui/src/lib/styles/tokens.css` is the only file allowed to define a colour, radius, shadow or
duration. Nothing else in the codebase writes a raw hex value.

### 3.1 Why hex and not `oklch()`

shadcn-svelte's default theme ships `oklch()` values. Both are valid CSS and Tailwind v4 accepts either.
This project uses **hex**, because the existing `desktop-app` theme is already hex, the palette was picked
by eye against real screenshots rather than derived from a lightness scale, and hand-maintaining oklch
values you can't read is a bug source. If you later want a programmatic ramp, convert then.

### 3.2 The colour model

Two independent systems that never mix:

- **Neutrals.** The ground, surfaces, text and borders. Warm charcoal, slightly green-shifted to match
  the existing `#1d1f1e`.
- **Status.** Five hues, used *only* to express asset and custody state.

There is deliberately **no brand accent colour**. Primary actions are near-white on the dark ground.

> **Change from the current desktop theme.** `app.css` today makes `#038940` green the brand accent and
> uses it for primary buttons, focus rings and selection. That collides head-on with `available` being
> green: a primary button and an "in stock" chip become the same colour, and the eye stops trusting green
> as a signal. The green survives, but only as `--status-available`.
>
> Also note the current theme's `--color-surface: #232532` is blue-shifted while `--color-bg: #1d1f1e` is
> green-shifted; side by side the surface looks faintly purple. Both are re-grounded below.

### 3.3 The token file

```css
/* packages/ui/src/lib/styles/fonts/..., Inter and JetBrains Mono, latin subset, self-hosted.
   The app must work with no internet (CLAUDE.md §2), so no CDN font links, ever. */
@font-face {
  font-family: "Inter"; font-style: normal; font-weight: 100 900; font-display: swap;
  src: url("./fonts/inter-v19-latin.woff2") format("woff2");
}
@font-face {
  font-family: "JetBrains Mono"; font-style: normal; font-weight: 400 700; font-display: swap;
  src: url("./fonts/jetbrains-mono-latin.woff2") format("woff2");
}

:root {
  /* ---------- neutrals: Studio Ops (A2) ---------- */
  --ground:          #17191A;   /* the window background */
  --surface:         #1E2122;   /* cards, sidebar, table container, top bar */
  --raised:          #262A2B;   /* hover, selected row, input fill, thumbnails */
  --overlay:         #2B2F30;   /* dialogs and popovers, floats above surface */

  --fg:              #E9E9E7;   /* primary text */
  --fg-muted:        #9A9F9E;   /* secondary text, column headers, metadata */
  --fg-faint:        #7C8281;   /* placeholder, disabled label, timestamps */

  --line:            #2F3435;   /* decorative dividers, table rules */
  --line-control:    #646B6A;   /* borders that define an interactive control (§10) */
  --line-strong:     #414748;   /* card and panel edges */

  /* ---------- action ---------- */
  --primary:         #E9E9E7;
  --primary-fg:      #17191A;
  --primary-hover:   #FFFFFF;
  --destructive:     #FF7A70;
  --destructive-fg:  #17191A;

  /* ---------- status: the ONLY saturated colour in the app ---------- */
  --status-available:      #4ADE80;  --status-available-bg:  #0E2B1C;
  --status-out:            #7DB3FF;  --status-out-bg:        #12233D;
  --status-due-soon:       #FBBF24;  --status-due-soon-bg:   #33260A;
  --status-overdue:        #FF7A70;  --status-overdue-bg:    #3A1614;
  --status-unavailable:    #9A9F9E;  --status-unavailable-bg:#232727;

  /* ---------- shape ---------- */
  --radius-sm: 4px;   /* chips, thumbnails, checkboxes */
  --radius:    6px;   /* buttons, inputs, rows, the default */
  --radius-lg: 10px;  /* cards, dialogs, the cart drawer */
  /* Nothing in this app is more rounded than 10px. Large radii read as consumer/toy. */

  /* ---------- elevation: hairline first, shadow second ---------- */
  --elev-1: 0 0 0 1px var(--line-strong);
  --elev-2: 0 0 0 1px var(--line-strong), 0 4px 12px rgb(0 0 0 / .45);
  --elev-3: 0 0 0 1px var(--line-control), 0 16px 40px rgb(0 0 0 / .60);
  /* No glow. No coloured shadow. On a dark ground, a hairline reads as elevation better than a blur. */

  /* ---------- type ---------- */
  --font-sans: "Inter", system-ui, sans-serif;
  --font-mono: "JetBrains Mono", ui-monospace, "SF Mono", Menlo, monospace;

  /* ---------- motion ---------- */
  --ease:       cubic-bezier(.2, 0, 0, 1);
  --dur-fast:   120ms;   /* hover, focus, checkbox */
  --dur-normal: 200ms;   /* drawer, dialog, popover */
  --dur-slow:   320ms;   /* scan confirmation */

  /* ---------- density (see §4) ---------- */
  --row-h:      36px;
  --control-h:  32px;
  --text-body:  14px;
  --gutter:     16px;
  --tap:        32px;
}
```

### 3.4 Mapping to shadcn's variable names

shadcn-svelte components are written against `--background`, `--foreground`, `--primary`, `--border`,
`--ring` and so on. Rather than rename them inside twenty generated components, alias them once. Append to
`tokens.css`:

```css
:root {
  --background: var(--ground);        --foreground: var(--fg);
  --card: var(--surface);             --card-foreground: var(--fg);
  --popover: var(--overlay);          --popover-foreground: var(--fg);
  --primary-foreground: var(--primary-fg);
  --secondary: var(--raised);         --secondary-foreground: var(--fg);
  --muted: var(--raised);             --muted-foreground: var(--fg-muted);
  --accent: var(--raised);            --accent-foreground: var(--fg);
  --destructive-foreground: var(--destructive-fg);
  --border: var(--line);              --input: var(--line-control);
  --ring: var(--fg);
  --sidebar: var(--surface);          --sidebar-foreground: var(--fg);
  --sidebar-border: var(--line);      --sidebar-accent: var(--raised);
}

@theme inline {
  --color-ground: var(--ground);
  --color-surface: var(--surface);
  --color-raised: var(--raised);
  --color-fg: var(--fg);
  --color-fg-muted: var(--fg-muted);
  --color-fg-faint: var(--fg-faint);
  --color-line: var(--line);
  --color-line-control: var(--line-control);
  --color-status-available: var(--status-available);
  --color-status-out: var(--status-out);
  --color-status-due-soon: var(--status-due-soon);
  --color-status-overdue: var(--status-overdue);
  --color-status-unavailable: var(--status-unavailable);
  --font-sans: var(--font-sans);
  --font-mono: var(--font-mono);
  --radius-sm: var(--radius-sm);
  --radius-lg: var(--radius-lg);
}
```

`@theme inline` is what turns them into utilities: `bg-surface`, `text-fg-muted`,
`text-status-overdue`, `border-line`. Note `--accent` is deliberately mapped to a *neutral*
(`--raised`), not to a hue: in shadcn's vocabulary "accent" means hover-fill, and giving it a colour is
exactly how the app would start looking generic.

### 3.5 Adding light mode later

The app is dark-only for v1. Should you want `A1` Field Manual later, it is a mechanical change: move the
`:root` neutrals above into a `.dark { }` block and put these in `:root`. The status hues need their
darker light-mode variants; the values are already in `mood-board.html` under `.ground-manual`.

```
ground #FAFAF9 · surface #FFFFFF · raised #F5F5F4 · overlay #FFFFFF
fg #1C1917 · fg-muted #78716C · fg-faint #A8A29E
line #E7E5E4 · line-control #D6D3D1 · line-strong #D6D3D1
primary #1C1917 · primary-fg #FAFAF9
available #047857 · out #1D4ED8 · due-soon #B45309 · overdue #B91C1C · unavailable #57534E
```

Do not add a user-facing theme toggle unless someone asks. It doubles what you have to test on a machine that
lives in one room.

---

## 4. Density

Three densities, set with a `data-density` attribute on any container. Everything inside inherits.
This is how one component set serves both the admin table and the across-the-room kiosk.

```css
[data-density="compact"]     { --row-h: 32px; --control-h: 28px; --text-body: 13px; --gutter: 12px; --tap: 28px; }
[data-density="comfortable"] { --row-h: 36px; --control-h: 32px; --text-body: 14px; --gutter: 16px; --tap: 32px; }
[data-density="kiosk"]       { --row-h: 60px; --control-h: 52px; --text-body: 17px; --gutter: 24px; --tap: 48px; }
```

- **`compact`.** Admin tables where an operator is scanning many rows: users list, overdue list, asset
  history.
- **`comfortable`.** The default. Browse, detail, cart, every form.
- **`kiosk`.** The sign-in screen and the scan-result screen. `--tap: 48px` is the floor for
  anything a student touches in a hurry; it is also the WCAG 2.2 target-size minimum (24px) with margin.

Components read `h-[--control-h]`, `min-h-[--row-h]`, `text-[length:--text-body]`, `p-[--gutter]`. A
component that hardcodes `h-9` instead is a bug. It will not scale into kiosk mode.

---

## 5. Components

### 5.1 From shadcn-svelte, with modifications

| Component | Used for | Modify |
|---|---|---|
| `button` | everything | Replace variants with: `primary` (near-white fill), `secondary` (outline on `--line-control`), `ghost` (hover fill only), `destructive`. **Delete the `link` variant**. A button that looks like a link has no place here. Heights bind to `--control-h`. |
| `table` | asset list, users, overdue, history | Row height → `--row-h`. Sticky header. `tabular-nums` on every numeric column. |
| `dialog` | asset detail, confirmations | Max width 560px. `--elev-3`. Escape and click-outside both close. |
| `sheet` | the cart drawer (§8.4) | Right side, width `min(420px, 90vw)`. |
| `input`, `label`, `checkbox`, `select` | forms, filters | Border `--line-control`, not `--line`: they need 3:1 (§10). |
| `command` | global search / jump-to-asset | Bound to `/` and `Cmd/Ctrl+K`. |
| `sonner` | toasts | Bottom-centre. Never for errors that need a decision. Those are dialogs. |
| `alert-dialog` | delete asset, delete user, override overdue | Never let a destructive default get the focus. |
| `badge` | counts only | **Not for status.** Status uses the custom component below. |
| `popover` + `calendar` | due-date picker | Must hard-cap at now + 7 days (`CLAUDE.md` §7); disable everything beyond it rather than validating after the fact. |
| `tooltip` | icon-only buttons | Every icon-only button needs one *and* an `aria-label`. |
| `tabs`, `separator`, `scroll-area`, `skeleton`, `avatar`, `dropdown-menu` | as named | none |

### 5.2 Stockroom-specific, hand-written

These live in `packages/ui/src/lib/components/app/`.

- **`<StatusDot status={...} />`.** The §6 status expression. One component, used everywhere, so status
  can never drift between two screens.
- **`<Serial value="T7iBat-001" />`.** Mono, `select-all` on click, copy-on-click with a toast. Every
  serial and student number in the app goes through this.
- **`<AssetRow />`.** Thumbnail, name, category path, `<Serial>`, `<StatusDot>`, due date, cart affordance.
- **`<CategoryTree />`.** The three-level `Type → Category → Model` filter. Must handle branches that stop
  short of depth 3 (`Primes` is seeded with no models, per `CLAUDE.md` §6.2).
- **`<CartDock />`** and **`<CartDrawer />`.** §8.4.
- **`<ScanListener />`.** Headless. Wraps `lib/scanner.ts`, emits `scan` and `typed` events. Exactly one
  instance mounts at the app root; screens subscribe. §9.
- **`<ScanResult />`.** The screen shown after a scan. §8.6.
- **`<DueDatePicker />`.** `popover` + `calendar` with the 7-day cap and a "max" hint.
- **`<EmptyState />`.** Icon, one sentence, one action. Every list needs one; a blank panel reads as broken.
- **`<PhotoFrame />`.** Asset/profile photo with a typed fallback glyph. Must look deliberate when
  `photo_path` is null, which will be most assets for a long time.

---

## 6. The status system

Five states. `CLAUDE.md` §6.2 puts three on the asset (`available`, `checked_out`, `unavailable`); the
other two are *derived* from `custody_events.due_at` and exist only in the UI and in `overdue_custody`.

| State | Colour | Label | Derivation |
|---|---|---|---|
| Available | `--status-available` | `Available` | `assets.status = 'available'` |
| Checked out | `--status-out` | `Checked out · due Sep 12` | `assets.status = 'checked_out'`, `due_at > now + 24h` |
| Due soon | `--status-due-soon` | `Due tomorrow` | `checked_out` and `due_at` within 24h |
| Overdue | `--status-overdue` | `Overdue 3 days` | row present in `overdue_custody` |
| Unavailable | `--status-unavailable` | `Unavailable` | `assets.status = 'unavailable'` |

**Expression is `C1`, dot plus label.** A 7px filled dot in the status colour, followed by the word in the
same colour at 11.5px/650. The word is never omitted; the dot is never used alone.

Two exceptions where the filled chip (`C2`) is correct instead: the asset **detail dialog**, where there is
exactly one status on screen and it should be unmissable, and the **scan confirmation** surface, where it
is the entire message.

The 24-hour "due soon" threshold is a UI-only convention and needs no backend support. The due date is
already in the `GetAsset` and `ListActiveCustody` payloads. Say so in the component, not in a comment
buried in a screen.

---

## 7. Layout

### 7.1 App shell

```
┌──────────────────────────────────────────────────────────┐
│ Top bar   breadcrumb ······················ search · user│  56px, --surface, 1px bottom --line
├───────────┬──────────────────────────────────────────────┤
│ Sidebar   │ Content                                      │
│ 236px     │ scrolls independently                        │
│ --surface │ max-width 1440px, centred                    │
│           │                                              │
├───────────┴──────────────────────────────────────────────┤
│ Cart dock                                                │  48px, only when cart is non-empty
└──────────────────────────────────────────────────────────┘
```

- **Sidebar.** 236px fixed. Category tree first, then an `Admin` group visible only when
  `is_admin`. Collapses to a 56px icon rail below 900px, and to a `sheet` below 720px.
- **Top bar.** Breadcrumb of the active category path on the left; search and the signed-in user on the
  right. The user chip shows first name, student number in mono, and an overdue indicator when
  `Me().has_overdue` is true.
- **Content.** Capped at 1440px so a wide monitor doesn't produce 2000px-wide table rows that are
  impossible to track across.
- **Cart dock.** Absent at zero items, so it costs nothing until it matters.

### 7.2 Breakpoints

Tailwind defaults, with only three that matter here:

| Width | Behaviour |
|---|---|
| ≥1280px | Full shell. The closet PC and any dev machine. |
| 900 to 1279px | Sidebar → icon rail. Table sheds the *Category* column. |
| <900px | Sidebar → sheet. Table → stacked cards. |

Below 900px is a courtesy, not a supported target. `CLAUDE.md` §2 rules out mobile and LAN access. Don't
spend time there. Do make sure it isn't *broken*, because someone will resize the Wails window.

---

## 8. Screen specifications

> Screens marked **⚠** depend on an open question in §15 and are specified under a stated assumption.

### 8.1 Sign-in

Full-bleed, `data-density="kiosk"`, no sidebar, no top bar. Centred: the wordmark, one large focused
input, and the line **"Scan your student ID."** The input is always focused and refocuses on blur. A
keyboard-wedge scanner types into whatever has focus (`CLAUDE.md` §10), so losing focus breaks scanning
entirely.

- **Fast burst + Enter** → `LoginByScan`. No password.
- **Typed slowly + Enter** → reveal a password field in place, `LoginByPassword`.
- **`needs_password: true`** → a set-password step before anything else. Two fields, a visible rule
  ("at least 8 characters"), and no way to skip.
- **Errors** render inline beneath the input, never as a toast. A toast can vanish before someone reads it.
- **On success**, if `has_overdue`, a blocking overdue notice appears before the browse screen, listing the
  overdue items and their days-late count, with a single **Continue**.

### 8.2 Browse

The primary screen. Sidebar category tree, top-bar search, content table.

**List shape is `B1 + B3`** (grouped by model, expandable to units), pending §15 Q-B. Default rows are one
per **model**: thumbnail, model name, category path in `--fg-muted`, `9 of 12 available`, `Add`. Expanding a
row reveals its individual units with their serials, so an admin can act on a specific one.

> **This needs backend work that doesn't exist yet.** `ListAssets` returns units; grouping needs a
> per-model available-count, and `CheckOutAssets` needs to accept "any unit of model X" or the frontend must
> pick a free unit itself and send its ID. The second option needs no server change and is the cheaper path
> It races when two people browse at once, which on a single shared PC cannot happen. Recommend the
> frontend picks. Add it to `TODO.md` Phase 3.

Other rules:

- Filtering is instant and client-side once the category's assets are loaded. Search hits the server
  (the `idx_assets_search` GIN index exists).
- Clicking a row opens the detail dialog. Clicking `Add` skips the dialog.
- `unavailable` units render at 55% opacity with `Add` disabled and the reason in a tooltip.
- The empty state distinguishes "this category has no models yet" (true for `Primes`) from "your filters
  matched nothing". They need different actions.

### 8.3 Asset detail

`dialog`, 560px. Photo left (or `<PhotoFrame>` fallback), facts right: name, full category path, `<Serial>`,
status as a filled chip, condition, and, when checked out, the checked-out date and due date. Custodian
name and student number are **admin-only**, per the rule below. Under that, the custody history from
`GetAssetHistory` as a compact list, newest first.

Footer: **Add to cart** (primary) or **Check in** if checked out and the viewer may do it. Admins also get
**Edit** and **Mark unavailable**.

**Custodian identity is admin-only until peer disclosure is approved.** Approved audience: admins
(`profiles.is_admin`) and the custodian themselves. A non-admin viewing someone else's item sees only that
it is checked out and when it is due, never who holds it. Letting a student find who has the lens they want
is worth having, but student custody records are personal data and nobody has yet been named who can
approve the wider audience, so the narrow rule is the default. ⚠ §15 Q7.

**Enforce it in the Go API, not the UI.** The asset-detail, scan and history responses must omit the
custodian fields for a non-admin actor rather than returning them for the frontend to hide; a hidden field
is still sent over the wire and the web app is a `fetch` call away. `TODO.md` Phase 3 owns that response
shape. If Q7 later approves peer disclosure, widening it is a change in that one place.

### 8.4 Cart: dock + expanding drawer

Chosen shape: a persistent dock that expands into a drawer, the way an e-commerce cart does.

**Collapsed dock** (48px, bottom, only when non-empty): up to five overlapping item thumbnails, `3 items`,
the chosen due date, and **Check out 3** as the primary action. Clicking anywhere on the dock *except* the
primary button expands the drawer.

**Expanded drawer** (`sheet`, right, `min(420px, 90vw)`): header `Cart · 3` with a close control; a scrolling
list of items, each with thumbnail, name, `<Serial>` and a remove button; then the due-date picker
(`<DueDatePicker>`, capped at 7 days); then, **for admins only**, a custodian picker defaulting to the signed-in
user; then **Check out**. A `Clear cart` ghost action sits at the bottom, behind an `alert-dialog`.

Rules:

- Adding an item while the drawer is closed **pulses the dock** (`--dur-fast` background flash) and
  increments the count. It does not auto-open the drawer, which would interrupt someone mid-scan.
- Adding while the drawer is open scrolls the new item into view and highlights it for `--dur-slow`.
- The drawer is **not modal**: you can browse with it open. Wails windows are small; make sure the content
  area still works at 420px narrower.
- **When the user has an overdue item**, the dock renders in the overdue colour with
  `Return BM6K-002 to check out` and the primary button is disabled. Admins see an **Override** control
  that opens an `alert-dialog` naming the overdue items before it will proceed. ⚠ §15 Q6.
- Cart state clears on sign-out and on idle-timeout 401. ⚠ §15 Q5.

### 8.5 Checkout

Not a screen, a transition. Pressing **Check out** disables the button, shows an inline spinner, and calls
`POST /checkout` once. `CheckOutAssets` is a single transaction and the whole cart fails together, so the
UI must never show partial success.

- **Success** → `<ScanResult>` in confirm mode: `3 items checked out`, the list, the due date,
  and two buttons, **Done** and **Sign out**. The sign-out prompt is required by `CLAUDE.md` §7 because
  the machine is shared.
- **`ErrConflict`** (an item stopped being available) → dialog naming exactly which items failed, with
  **Remove them and retry**. Never a bare "conflict".
- **`ErrOverdueBlocked`** → shouldn't be reachable if 8.4 disabled the button, but handle it: the same
  overdue notice, with the admin override if applicable.

### 8.6 Scan result ⚠

**Assumption:** scanning an *available* item shows it and requires a press to add, which is your stated preference.
Scanning a *checked-out* item also requires a press to confirm the return, which **contradicts `CLAUDE.md`
§1.5** ("checked in immediately"). Confirm which wins (§15 Q2); if `CLAUDE.md` wins, drop the confirm step
from the checked-out branch below and keep everything else.

`data-density="kiosk"`, drawn over the current screen so context isn't lost, dismissing on confirm,
cancel, Escape, or a fresh scan.

| Scan result | Surface |
|---|---|
| `available` | Large photo, name, `<Serial>`, `Available` chip. Buttons: **Add to cart** (primary, autofocused) · **Cancel**. |
| `checked_out` | Name, `<Serial>`, days out, and the custodian **for admins only** (§8.3). Buttons: **Check in** (primary) · **Cancel**. An optional damage-note field is collapsed under **Add a note**, expanded inline. |
| `unavailable` | Name, `<Serial>`, `Unavailable` chip, reason. Single **Close**. No path to the cart. |
| unknown serial (`ErrNotFound`) | The scanned string in mono, "Not a Stockroom item." Single **Close**. |

After a successful check-in, `<ScanResult>` switches to a green confirmation for `--dur-slow` and appends the
item to a **session scan log**, the last five scans with time, name, serial and in/out, which persists
under the surface so someone returning a six-item kit can verify all six landed. That's the `D3` behaviour
from the mood board, adapted to your confirm-first requirement.

Any **new scan while `<ScanResult>` is open** replaces its contents immediately. Someone with an armful of gear
will scan continuously and should never have to press Cancel between items.

### 8.7 Admin panel

Reached from the sidebar `Admin` group, visible only when `is_admin`. Not a separate app, not a separate
theme. It is the same shell, `data-density="compact"`.

- **Assets.** Table with create/edit/delete, photo upload, `available ⇄ unavailable` toggle. Delete is an
  `alert-dialog` and is blocked server-side when custody is open; surface that as a specific message, not a
  generic failure.
- **Categories.** The three-level tree with inline rename, add-child, and delete. Depth is capped at 3
  server-side; the UI hides **Add child** on depth-3 nodes rather than letting the server reject it. Delete
  is blocked when the node has children or assets. Say which.
- **Users.** Table of student number, name, admin flag, overdue count. Row actions: edit, set password,
  view history, delete. **Roster CSV import** gets its own dialog: file picker, a preview table of the parsed
  rows, then a per-row result list after `ImportRoster` returns. Never fire-and-forget an import.
- **Overdue.** The highest-value admin screen. `ListOverdueCustody` sorted by days-late descending:
  custodian, student number, item, serial, due date, days late. Row action: **Check in**.
- **Backup.** One **Backup Now** button, the destination path, and the result of the last run.

---

## 9. Scanner integration

`lib/scanner.ts` in each app implements the keystroke buffer from `CLAUDE.md` §10. The UI contract around it:

1. **Exactly one listener**, mounted at the app root as `<ScanListener>`. Two listeners means every scan
   fires twice, and the second one will be a duplicate check-in.
2. **Which endpoint a scan hits is decided by the active screen**, never by the payload. Sign-in screen →
   `POST /auth/scan`. Anywhere else → `POST /scan`. The backend never guesses.
3. **Never swallow keystrokes from a real input.** If focus is inside an `<input>`, `<textarea>` or a
   `contenteditable`, the global listener must ignore the burst. Otherwise typing a search query or a
   damage note fires phantom scans. The sign-in screen is the deliberate exception, since its input *is*
   the scan target.
4. **The 50ms threshold is a guess** until hardware arrives (Week 7). Put it in one exported constant with
   a comment, and add a hidden diagnostic (`Ctrl+Shift+D`) that prints the last burst's inter-key timings.
   You will need it on the day the scanner shows up, and reverse-engineering it later is miserable.
5. **Visible affordance.** A small scanner-ready indicator in the top bar. When someone scans and nothing
   happens, the first question is always "is it even listening?"

---

## 10. Accessibility

Non-negotiable, and cheap if done from the start.

**Contrast.** Measured against `--ground: #17191A`:

| Pair | Ratio | Verdict |
|---|---|---|
| `--fg` `#E9E9E7` | 14.51 | AA / AAA |
| `--fg-muted` `#9A9F9E` | 6.58 | AA |
| `--fg-faint` `#7C8281` | 4.51 | AA (this is why it is not `#6E7473`, which measures 3.70 and fails) |
| `--status-available` `#4ADE80` | 10.12 | AA |
| `--status-out` `#7DB3FF` | 8.20 | AA |
| `--status-due-soon` `#FBBF24` | 10.57 | AA |
| `--status-overdue` `#FF7A70` | 6.95 | AA |
| `--primary-fg` on `--primary` | 14.51 | AA |
| `--line-control` `#646B6A` | 3.24 | meets the 3:1 non-text minimum |
| `--line` `#2F3435` | 1.40 | **decorative only**. Never use it to define a control's boundary |

The `--line` / `--line-control` split exists precisely because of that last row. A table rule may be
invisible; an input border may not.

**Everything else:**

- Focus is visible on every interactive element: `outline: 2px solid var(--ring); outline-offset: 2px`.
  Never `outline: none` without a replacement.
- Full keyboard operation: `Tab` order follows visual order; `/` focuses search; `Escape` closes the
  topmost layer only; `Enter` activates the focused row.
- Status is never colour-alone (§6).
- Every icon-only button has an `aria-label`.
- Dialogs trap focus and restore it to the trigger on close. Bits UI does this; don't fight it.
- Scan results announce via `aria-live="assertive"`; toasts via `aria-live="polite"`.
- Respect `prefers-reduced-motion`: replace every transform/opacity transition with an instant state change.
- Photos need real `alt` text (the asset name), not `alt="photo"`.

---

## 11. Motion

| What | Duration | Property |
|---|---|---|
| Hover, focus, checkbox | `--dur-fast` | `background-color`, `border-color` |
| Dialog, drawer, popover | `--dur-normal` | `opacity` + `translate` |
| Scan confirmation | `--dur-slow` | `opacity` |
| Cart dock pulse on add | `--dur-fast` | `background-color` |

Animate `opacity` and `transform` only. Never animate `height`, `width` or `box-shadow`. They force
layout and the Wails webview on a school PC is not a fast machine. All easing is `--ease`. No spring
physics, no bounce, no stagger, no page transitions.

---

## 12. Wails vs web

Deliberate differences. Anything not listed is a bug.

| | Wails desktop | Web app |
|---|---|---|
| API base | `http://127.0.0.1:8080` | same, needs CORS for the Vite dev origin |
| Window chrome | native title bar | browser chrome |
| Minimum size | set `MinWidth: 1024, MinHeight: 700` in `main.go` | user's problem |
| Right-click | disable the default context menu in production, keep it in dev | leave alone |
| Text selection | `user-select: none` on chrome, `text` on content and every `<Serial>` | leave alone |
| Fonts | bundled `woff2` | same bundled `woff2`, never a CDN |
| Zoom | ignore | respect browser zoom to 200% |

**One real constraint worth knowing before you start.** Tailwind v4 requires Chrome 111+ / Safari 16.4+
because it depends on `@property` and `color-mix()`. On Windows, Wails uses WebView2, which is evergreen
Chromium, so that is fine. On macOS, Wails uses the **system WKWebView**, whose engine is tied to the OS version, so
the dev machine needs **macOS 13.3 or newer**. Below that, Tailwind v4 output silently misrenders rather
than erroring. The closet PC is Windows, so production is safe; this only bites macOS development.

---

## 13. What not to do

The brief said avoid the AI look. Concretely, none of these appear anywhere:

- Purple, violet or indigo as a brand colour. No `#6366F1`, no `#8B5CF6`.
- Gradient fills of any kind, on buttons, cards, headers, backgrounds or text.
- Glassmorphism, `backdrop-filter: blur`, translucent floating panels.
- Glow: coloured box-shadows, neon borders, pulsing rings.
- Border radius above 10px. No pill-shaped cards.
- Emoji as UI iconography. Use `@lucide/svelte`, one weight, one size per context.
- Decorative colour. A coloured icon, heading or divider that isn't communicating status.
- Marketing voice in product copy. "Check out 3" not "Let's get your gear! 🎬".
- Centred hero layouts inside the app. Content is left-aligned and scannable.
- More than one primary button in a view.
- Animated blobs, mesh gradients, aurora backgrounds, particles.

If a screen looks like it could be any SaaS product, something on this list has crept in.

---

## 14. Implementation order

Maps onto `TODO.md` Phase 6 (Week 8), which is where UI work is scheduled. Each step is shippable.

1. **Workspace + tokens.** Align `desktop-app/frontend` to Vite 8. Create `packages/ui`, move the fonts,
   write `tokens.css`, wire both apps' `app.css`. Verify a `bg-surface text-fg` div renders identically in
   both. *Nothing else is worth starting until this is true.*
2. **shadcn-svelte init and base components.** Run the CLI in `packages/ui`, fix `components.json` aliases, add
   the §5.1 list, rework `Button` variants and `Table` density.
3. **Stockroom components** (§5.2), built against fixtures, so no server is needed. `<StatusDot>`, `<Serial>`,
   `<AssetRow>`, `<PhotoFrame>`, `<EmptyState>` first; they unblock everything.
4. **App shell** (§7) in the desktop app, with a stub sidebar and top bar.
5. **Sign-in** (8.1) + `<ScanListener>` (§9) against `POST /auth/scan`. The first path that runs end to end, from scanner to database.
6. **Browse + detail** (8.2, 8.3).
7. **Cart dock + drawer + checkout** (8.4, 8.5).
8. **Scan result** (8.6). The riskiest screen; do it once the loop below it is solid.
9. **Admin panel** (8.7), `data-density="compact"`.
10. **Mirror into `web-app`.** If steps 1 to 9 were done right this is import statements and routing, because
    every component already lives in `packages/ui`. If it turns into a rewrite, something leaked into
    `desktop-app/frontend/src/lib/`.
11. **Delete the legacy path.** `desktop-app/frontend/src/lib/supabase.ts`, `db.ts`,
    `@supabase/supabase-js`, and the current `AssetBrowser.svelte` with its hand-rolled `btnBase` /
    `btnGhost` / `inputClass` strings, which this system replaces.

Accessibility (§10) is checked at each step, not at the end. Retrofitting focus order into a finished app
costs several times what building it in does.

---

## 15. Open decisions

These block specific sections, not the whole document. Recorded from the grilling round in progress.

- **Q1. Who operates the machine?** Unattended student self-service, or a staffed desk with an officer
  signed in? Changes whether "check out on behalf of" is an edge case or the main path. *Blocks:* 8.1, 8.4.
- **Q2. Does check-in need a confirm press?** `CLAUDE.md` §1.5 says a scanned checked-out item checks in
  immediately; you've asked for a confirm. One of the two must be corrected. *Blocks:* 8.6.
- **Q3. Does the cart mix borrowing and returning?** *Blocks:* 8.4, 8.6.
- **Q4. What happens when an item is scanned with nobody signed in?** *Blocks:* 8.1.
- **Q5. How does the cart die?** Sign-out, idle timeout, reload. *Blocks:* 8.4.
- **Q6. Where does the overdue block bite?** At sign-in with the cart disabled throughout, or at the
  checkout press. *Blocks:* 8.4, 8.5.
- **Q7. Who may see who holds an item?** Admins and the custodian only, or any signed-in student? Needs
  whoever owns student privacy at the school to decide. Until then §8.3 ships the admin-only default and
  the API withholds the field. *Blocks:* 8.3, 8.6.
- **Q-B. `B1` vs `B1 + B3`.** Unit-level or model-level browsing. If `B1 + B3`, `TODO.md` Phase 3 needs a
  per-model availability count. *Blocks:* 8.2.

Also still open from `CLAUDE.md` §13 and relevant here: the scan-vs-typed keystroke threshold (§9) and
`SESSION_IDLE_MINUTES`, which determines how aggressively the cart is discarded (8.4).
