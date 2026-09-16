# Stockroom design system

The single reference for how Stockroom looks and behaves on screen, in both the Wails desktop app and
the web app. `CLAUDE.md` is the architecture reference and `TODO.md` is the backend work list; this file
is the UI counterpart to both. Where the two disagree about behaviour, `CLAUDE.md` wins and this file
should be corrected.

Written 2026-09-09. Direction chosen from `docs/design/mood-board.html` (open it with
`python3 -m http.server 8899` inside `docs/design/`, then visit `/mood-board.html`).

**Settled:** ground `A2` "Studio Ops" (warm charcoal dark) · component library **shadcn-svelte v1** over
Bits UI · shared **npm workspace package** · asset list **grouped by model, each row an accordion onto its
units** (`B3 + B1`) · cart as a **bottom dock that opens a full cart page** · status as **dot + label** ·
**Inter + mono for identifiers**.

Revised 2026-09-10: the list shape and the cart shape both changed. §8.2 and §8.4 carry the new versions and
§16 records why.

**Settled 2026-09-12:** all seven user-flow questions from §15 were resolved in a grilling session; see §16
for the resolutions. Nothing in this document is blocked on an open question anymore.

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
    alert-dialog popover calendar tabs skeleton avatar command collapsible
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
  /* Re-anchored 2026-09-15: the window background was set to #2E3033 and every
     other neutral was lifted by the step it already held above the old ground,
     so the elevation order survives. The two lower text tiers moved too -- see
     the contrast table in §11. */
  --ground:          #2E3033;   /* the window background */
  --surface:         #35383B;   /* cards, sidebar, table container, top bar */
  --raised:          #3D4144;   /* hover, selected row, input fill, thumbnails */
  --overlay:         #424649;   /* dialogs and popovers, floats above surface */

  --fg:              #E9E9E7;   /* primary text */
  --fg-muted:        #A8ADAC;   /* secondary text, column headers, metadata */
  --fg-faint:        #949998;   /* placeholder, disabled label, timestamps */

  --line:            #464B4E;   /* decorative dividers, table rules */
  --line-control:    #7B8283;   /* borders that define an interactive control (§10) */
  --line-strong:     #585E61;   /* card and panel edges */

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
  --radius-lg: 10px;  /* cards, dialogs, the cart summary panel */
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
  --dur-normal: 200ms;   /* dialog, popover, sheet */
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
| `sheet` | the sidebar below 720px (§7.2) | Left side, width `min(300px, 85vw)`. The cart no longer uses it. |
| `collapsible` | the unit accordion on a model row (§8.2) | Trigger is the whole row. No height animation; see §11. |
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
- **`<Serial value="T7IBAT-001" />`.** Mono, `select-all` on click, copy-on-click with a toast. Every
  serial and student number in the app goes through this. It shortens two ways, both display-only:
  a generated `prefix-NNNN` serial shows just its number (`T7IBAT-001` → `1`), and anything past 14
  characters truncates in the middle (`3QZB…8842`). Full value on hover and in the copy; §8.2 has both
  rules and why the ellipsis is never trailing.
- **`<ModelRow />`.** A model with its thumbnail, name, category path, `n of m available`, out-count and
  **Add**. Wraps a `collapsible` whose content is the unit list. The whole row is the trigger; **Add** stops
  propagation so pressing it does not also toggle the row. §8.2.
- **`<UnitRow />`.** One physical asset inside an expanded `<ModelRow>`: `<Serial>`, `<StatusDot>`, due date
  or custodian, and its own **Add**. Also used standalone by the admin asset table.
- **`<CategoryTree />`.** The three-level `Type → Category → Model` filter. Must handle branches that stop
  short of depth 3 (`Primes` is seeded with no models, per `CLAUDE.md` §6.2).
- **`<CartDock />`.** The 48px bottom bar. Count, thumbnails, due date, and one action that navigates to
  the cart page. §8.4.
- **`<CartPage />`.** Line items plus the commit panel. §8.4.
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

The cart page (§8.4) is a route inside this same shell. It replaces the content area and hides the dock.

- **Sidebar.** 236px fixed. Category tree first, then an `Admin` group visible only when
  `is_admin`. Collapses to a 56px icon rail below 900px, and to a `sheet` below 720px.
- **Top bar.** Breadcrumb of the active category path on the left; search and the signed-in user on the
  right. The user chip shows first name, student number in mono, and an overdue indicator when
  `Me().has_overdue` is true.
- **Content.** Capped at 1440px so a wide monitor doesn't produce 2000px-wide table rows that are
  impossible to track across.
- **Cart dock.** Spans the full width below the sidebar. Absent at zero items, so it costs nothing until it
  matters, and absent again on the cart page itself.

### 7.2 Breakpoints

Tailwind defaults, with only three that matter here:

| Width | Behaviour |
|---|---|
| ≥1280px | Full shell. The closet PC and any dev machine. |
| 900 to 1279px | Sidebar → icon rail. Model rows shed the category path; unit rows shed the due column. |
| <900px | Sidebar → sheet. Rows → stacked cards. The cart page drops to one column. |

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

- **The field accepts digits only.** Letters are stripped as they arrive — typed, pasted or autofilled —
  and the value is capped at `MaxStudentNumberLength` (32). `NormalizeStudentNumber` refuses non-digits
  server-side regardless; filtering here just means the rule shows while someone types instead of being
  reported after they press Enter. The scanner is untouched by this: it reads keystrokes directly, so an
  *item* barcode scanned at sign-in still produces the explicit "that looks like an item barcode" message
  rather than a silently stripped number failing as a bad login (§15 Q4).
- **Fast burst + Enter** → `LoginByScan`. No password.
- **Typed slowly + Enter** → reveal a password field in place, `LoginByPassword`.
- **`needs_password: true`** → a set-password step before anything else. Two fields, a visible rule
  ("at least 8 characters"), and no way to skip.
- **Errors** render inline beneath the input, never as a toast. A toast can vanish before someone reads it.
- **On success**, if `has_overdue`, a blocking overdue notice appears before the browse screen, listing the
  overdue items and their days-late count, with a single **Continue**.

### 8.2 Browse

The primary screen. Sidebar category tree, top-bar search, content table.

**List shape is `B3 + B1`: one row per model, and the row is an accordion onto its units.** Settled
2026-09-10, closing the former list-shape question. `B3` is the base because the closet holds 200-odd units across
roughly thirty models, and a flat unit list is thirty screens of near-identical rows. `B1` survives inside
the expansion, so nothing the flat list could tell you is lost.

**The model row** carries thumbnail, model name, category path in `--fg-muted`, `4 of 6 available` as a
`<StatusDot>` and an out-count. Its availability dot reads `--status-available` when the count is above
zero and `--status-unavailable` at `0 of n`.

**It has no Add** (2026-09-14, reversing the 2026-09-10 rule below). The row is a summary and a way in;
adding is a unit-row action. See §16.

**Expanding it** reveals one `<UnitRow>` per physical asset. The row is, left to right:

```
[photo] name · serial · status · Add
```

The **name repeats on every unit row**, which looks redundant next to the model headline and is not:
the same component is the admin table's row (§8.7), where there is no model row above it, and a row that
reads differently in the two places is §1.4's bug. **The serial is its own column**, because it is the
only thing that tells two units of one model apart — and it is the scan key, so it is what someone
matches against the sticker in their hand.

Two rules on those columns, both settled 2026-09-14 against real data:

- **A name never carries its own unit number.** `Canon T7i`, three times, distinguished by serial — not
  `Canon T7i #1`, `#2`, `#3`. The list groups by name, so a numbered name splits one model into N groups
  of one and `2 of 3 available` never appears at all. `supabase/seed.sql` and a pgTAP assertion both
  guard this.
- **A model-prefixed serial displays as its unit number alone.** `T7IBAT-001` reads `1`. Batteries, bags
  and tripods have no manufacturer serial, so their stickers carry one we generate — and its prefix is
  identical for every unit of the model, with the model name already sitting beside it in the row.
  `<Serial>` treats `prefix-NNNN` as generated and anything unbroken as a manufacturer serial, which is
  what keeps the two apart without a column in the database declaring it.
- **A serial longer than 14 characters truncates in the middle**, `3QZB…8842`. Never at the end:
  manufacturer serials share a long prefix across a production run, so a trailing ellipsis would render
  every unit of a model identical, which is the one thing the column exists to prevent.

Both rules are **display only**. The stored serial is the scan key, so the tooltip, the `title` and what
a click copies are always the whole thing.

Rules that make the two levels behave predictably:

- **Only a unit row adds.** What goes in the cart is a specific serial, and a model-level press had to
  guess which — invisibly, in both directions: you pressed Add on `Canon T7i`, one of three bodies landed
  in the cart, and nothing said which one. Expanding first costs one press and removes the guess.
- **Add opens the detail dialog, which is where the cart press actually happens** (`CLAUDE.md` §1 step 3:
  "click an item → detail popup → Add to Cart"). The popup is the only place the photo, the condition note
  and the custody history are visible, and it is where someone confirms they are taking *this* body rather
  than one that merely shares its model name. **Units with nothing extra to show skip it** and go straight
  into the cart — a popup repeating a row the user is already looking at is a press for nothing (§1.1).
  `lib/add-flow.ts` is the whole of this: one `ADD_OPENS_DETAIL` flag and one `hasAddDetail` predicate,
  deliberately isolated because the behaviour is provisional. A linear item counts as having nothing extra
  (no photo, no condition, a serial we generated rather than a manufacturer stamped).
- **The whole model row is the accordion trigger**, and with no button beside it nothing has to stop
  propagation.
- **The Add button is a toggle.** Pressed on a row already in the cart it reads `In cart` and removes the
  item; the icon swaps from a check to an X on hover so the press is predictable before it happens. Undo
  belongs where the action was — the previous disabled `In cart` label said what had happened and gave no
  way to change your mind without navigating to the cart page. The detail dialog (§8.3) and the scan
  surface (§8.6) carry the same toggle, for the same reason.
- **Rows are collapsed by default.** Two exceptions open one: a search result opens the group that matched,
  and a scanned serial opens its group and highlights the unit for `--dur-slow`.
- **The expansion is not paginated but it is capped**, at fifty units with a `n more units` footer row.
  `SD Card 128GB` with twenty units is fine inline. A future model with three hundred would not be.
- **`unavailable` units** render their thumbnail and row fill at 55% opacity, while their serial and status
  label remain fully opaque. `Add` is disabled and the reason appears in a tooltip. They still count in the
  total (`4 of 6`), because someone looking at the shelf will count six bodies.
- **Custodian identity is visible to any signed-in viewer, on hover over the status** (§8.3, resolved
  2026-09-12; moved behind the hover 2026-09-14). A unit the viewer holds themselves reads `You · Sep 12`;
  every other checked-out unit names the current custodian and the due date. It is still sourced from
  `ListAssets`, so the row carries it without opening detail — but it is no longer a column of its own. It
  was the widest thing in the row and the least often needed, and the status is exactly the thing a person
  is already looking at when they want to know who has it.
- **Sort order.** Categories (the model-row grouping's parent) follow `Catagories.md`'s document order
  (Cameras/Bodies, Lenses, Lights, Audio Stuff, Physical Bags, Tripods/Monopods, Batteries, Misc), not
  alphabetical. Within any list, available units sort before checked-out ones.

> **Backend, as built (2026-09-13).** `ListAssets` returns units, each with its status and current holder,
> in this order. The model row's `4 of 6 available` count and the free unit `Add` points at are both
> derived by the **frontend** from the group it already loaded, so `CheckOutAssets` stays unchanged. That
> races when two people browse at once, which on a single shared PC cannot happen.

Other rules:

- Filtering is instant and client-side once the category's assets are loaded. Search hits the server
  (the `idx_assets_search` GIN index exists).
- Clicking a model row expands it. Clicking a unit row opens the detail dialog.
- The empty state distinguishes "this category has no models yet" (true for `Primes`) from "your filters
  matched nothing". They need different actions.
- **The admin asset table (§8.7) stays flat `B1`.** An operator editing assets works unit by unit and the
  grouping only gets in the way. Same `<UnitRow>` component, no `<ModelRow>` around it.

### 8.3 Asset detail

`dialog`, 560px. Photo left (or `<PhotoFrame>` fallback), facts right: name, full category path, `<Serial>`,
status as a filled chip, condition, and, when checked out, custodian name, checked-out date and due date.
Under that, the custody history from `GetAssetHistory` as a compact list, newest first — history stays
admin-only (below).

Footer: **Add to cart** (primary), **Remove from cart** when it is already in there (§8.2's toggle), or
**Check in** if checked out and the viewer may do it. Admins also get **Edit** and **Mark unavailable**.

**Current custodian is visible to any signed-in viewer; historical custodians are not.** Resolved
2026-09-12 (§15 Q7), reversing the admin-only default from `b6fe111`: any signed-in user can see who
currently holds a checked-out item — the original motivation, letting a student find who has the lens they
want, outweighs withholding it, and this project doesn't have a separate school privacy officer to seek
sign-off from. This applies only to the **current** holder. `GetAssetHistory`'s past-custodian trail stays
admin-only; a non-admin's own history is available only via `GetUserHistory`.

**Enforce it in the Go API, not the UI.** `GetAsset`, `ListAssets`, and `ScanItem` responses include the
current custodian for every actor; `GetAssetHistory` omits all custodian identities unless the actor is an
admin. `TODO.md` Phase 3/4 own that response shape.

**The custodian's name, not their number** (2026-09-13). `custody.student_number` is null for a non-admin
viewer. A student number signs its owner in by scan with no password, so a browse list that carried one per
checked-out unit would be a list of usable credentials. Nothing in §8.2 or §8.3 displays it to a student
anyway: the unit row shows `You · Sep 12` or `Jordan S. · Sep 15`.

### 8.4 Cart: bottom dock, full cart page

Settled 2026-09-10, replacing the expanding drawer. The cart sits on the bottom edge and its button
**navigates to a cart page**, the way an online store does. The drawer is gone: it ate 420px of a 1024px
Wails window, and everything it held fits on the page with room left over.

**The dock** (48px, full width below the sidebar, only when the cart is non-empty): up to five overlapping
thumbnails, `3 items`, the chosen due date if one has been picked, and **Review cart · 3**. The whole bar is
the link, not just the button.

**The cart page** (a route in the same shell, `data-density="comfortable"`) is two columns:

- **Left, the line items.** Thumbnail, name, `<Serial>`, live `<StatusDot>`, and a remove control per row.
  The status is live so an item that someone else took while the cart sat idle turns red here instead of
  failing inside the transaction.
- **Right, the commit panel** (244px, `--radius-lg`): the item count, the `<DueDatePicker>` capped at 7 days
  with the cap named in the hint, a custodian picker **for admins only** defaulting to the signed-in user,
  then **Check out n** full-width. `Clear cart` sits below it as a ghost action behind an `alert-dialog`.

Rules:

- **The dock is hidden on the cart page.** Two `Check out` buttons in one view is one too many, and the
  §13 rule allows one primary button per view.
- **`Back to browse` returns to the category the user left**, not to the root of the tree. Someone adding
  six items from one shelf should not re-navigate the tree after every trip to the cart.
- Adding an item while browsing **pulses the dock** (`--dur-fast` background flash) and increments the
  count. It never navigates on its own, which would interrupt someone mid-scan.
- **Scanning still works on the cart page.** `<ScanListener>` is mounted at the app root (§9), so a scan
  there opens `<ScanResult>` over the cart exactly as it would over browse.
- **Removing the last item returns to browse**, because an empty cart page is a dead end.
- **When the user has an overdue item**, the dock renders in the overdue colour with
  `Return BM6K-002 to check out` and the button is disabled, so the block reads before the page rather than
  at the commit. Admins get an **Override** control that opens an `alert-dialog` naming the overdue items
  first. The same block repeats on the cart page if someone arrives by URL. Resolved 2026-09-12 (§15 Q6):
  this UI-level block is **in addition to**, not instead of, `CheckOutAssets` refusing server-side — both
  layers enforce it so there's no path that only relies on the frontend disabling a button.
- Cart state clears on sign-out or idle-timeout 401, and **only** then — a page reload does not clear it.
  Resolved 2026-09-12 (§15 Q5).

### 8.5 Checkout

Not a screen, a transition on the cart page (§8.4). Pressing **Check out** disables the button, shows an
inline spinner, and calls `POST /checkout` once. `CheckOutAssets` is a single transaction and the whole
cart fails together, so the UI must never show partial success.

- **Success** → `<ScanResult>` in confirm mode: `3 items checked out`, the list, the due date,
  and two buttons, **Done** and **Sign out**. The sign-out prompt is required by `CLAUDE.md` §7 because
  the machine is shared.
- **`ErrConflict`** (an item stopped being available) → dialog naming exactly which items failed, with
  **Remove them and retry**. Never a bare "conflict". The failed lines also flip to their real status on the
  page behind the dialog, so the two agree.
- **`ErrOverdueBlocked`** → shouldn't be reachable if the dock already blocked it, but handle it: the same
  overdue notice, with the admin override if applicable.

### 8.6 Scan result

Resolved 2026-09-12 (§15 Q2): `CLAUDE.md` §1.5 wins. Scanning a *checked-out* item checks it in
**immediately**, no confirm press — the dialog below appears already in its post-check-in state for that
branch. Scanning an *available* item still requires a press to add, since that path opens the same
detail/add-to-cart flow as clicking the item, not an irreversible action.

If nobody is signed in when an item barcode is scanned (§15 Q4), the sign-in screen shows an explicit
"Sign in first" message instead of trying to interpret the code as a student number.

`data-density="kiosk"`, drawn over the current screen so context isn't lost, dismissing on confirm,
cancel, Escape, or a fresh scan.

| Scan result | Surface |
|---|---|
| `available` | Large photo, name, `<Serial>`, `Available` chip. Buttons: **Add to cart** (primary, autofocused) · **Cancel**. Adding pulses the dock; it does not navigate to the cart page. |
| `checked_out` | Already checked in by the time this renders. Name, `<Serial>`, the custodian who just returned it, and a green confirmation for `--dur-slow` (§8.5's session scan log picks it up). An optional damage-note field is collapsed under **Add a note**, expanded inline. Single **Close**. |
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

- **Assets.** Flat unit table (§8.2, no model grouping) with create/edit/delete, photo upload, and the
  `available ⇄ unavailable` toggle. The create/edit form asks for a **name and a serial number**, and no
  asset tag: the tag is generated by the database and is an internal key (`CLAUDE.md` §6.2). The detail
  dialog shows it to admins only, for the rare case of matching a row against a CSV export. Delete is an `alert-dialog` and is blocked server-side when custody is
  open; surface that as a specific message, not a generic failure.
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

**Contrast.** Re-measured 2026-09-15 against `--ground: #2E3033`. A lighter
ground costs every foreground some ratio — the old values against `#17191A` are
kept in the last column, because the *reason* a token has the value it does is
usually the ratio it was chosen to clear:

| Pair | Ratio | Verdict | Was (on `#17191A`) |
|---|---|---|---|
| `--fg` `#E9E9E7` | 10.89 | AA / AAA | 14.51 |
| `--fg-muted` `#A8ADAC` | 5.82 | AA | 6.58 (as `#9A9F9E`) |
| `--fg-faint` `#949998` | 4.58 | AA. It had to move: the old `#7C8281` measures **3.38** here and fails | 4.51 (as `#7C8281`) |
| `--status-available` `#4ADE80` | 7.59 | AA | 10.12 |
| `--status-out` `#7DB3FF` | 6.15 | AA | 8.20 |
| `--status-due-soon` `#FBBF24` | 7.93 | AA | 10.57 |
| `--status-overdue` `#FF7A70` | 5.21 | AA | 6.95 |
| `--primary-fg` on `--primary` | 14.51 | AA | 14.51 (unaffected; neither token moved) |
| `--line-control` `#7B8283` | 3.38 | meets the 3:1 non-text minimum | 3.24 |
| `--line` `#464B4E` | 1.50 | **decorative only**. Never use it to define a control's boundary | 1.40 |

The status hues are the tightest of these: `--status-overdue` has 5.21 to give
before it stops clearing 4.5:1, so a further lift of the ground is not free.

The `--line` / `--line-control` split exists precisely because of that last row. A table rule may be
invisible; an input border may not.

**Everything else:**

- Focus is visible on every interactive element: `outline: 2px solid var(--ring); outline-offset: 2px`.
  Never `outline: none` without a replacement.
- Full keyboard operation: `Tab` order follows visual order; `/` focuses search; `Escape` closes the
  topmost layer only; `Enter` activates the focused row.
- A model row is a real trigger, not a `<div>` with an `onclick`. It needs `aria-expanded`, `aria-controls`
  pointing at its unit list, and `Enter` / `Space` to toggle. Bits UI's `collapsible` gives all three; a
  hand-rolled row gives none of them.
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
| Dialog, sheet, popover | `--dur-normal` | `opacity` + `translate` |
| Model row caret on expand | `--dur-fast` | `transform: rotate(90deg)` |
| Scan confirmation | `--dur-slow` | `opacity` |
| Cart dock pulse on add | `--dur-fast` | `background-color` |

Animate `opacity` and `transform` only. Never animate `height`, `width` or `box-shadow`. They force
layout and the Wails webview on a school PC is not a fast machine.

**The unit accordion therefore does not slide open.** Units appear at once; only the caret rotates. A
twenty-row expansion animating its height is exactly the layout thrash this rule exists to prevent, and the
row is a filter step, not a reveal worth decorating. All easing is `--ease`. No spring
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
   `<UnitRow>`, `<PhotoFrame>`, `<EmptyState>` first; they unblock everything. `<ModelRow>` after
   `<UnitRow>`, since it wraps it.
4. **App shell** (§7) in the desktop app, with a stub sidebar and top bar.
5. **Sign-in** (8.1) + `<ScanListener>` (§9) against `POST /auth/scan`. The first path that runs end to end, from scanner to database.
6. **Browse + detail** (8.2, 8.3). Build the flat unit list first, then wrap it in the model accordion, so
   the admin table (8.7) and the browse list share one component from the start.
7. **Cart dock + cart page + checkout** (8.4, 8.5).
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

All seven resolved 2026-09-12 in a grilling session; see §16 for the resolutions and which sections each
one unblocked. Nothing below is open anymore — kept as a record of what was asked.

- ~~**Q1. Who operates the machine?**~~ Unattended student self-service, or a staffed desk with an officer
  signed in? Changed whether "check out on behalf of" is an edge case or the main path. *Blocked:* 8.1, 8.4.
- ~~**Q2. Does check-in need a confirm press?**~~ `CLAUDE.md` §1.5 says a scanned checked-out item checks in
  immediately; an earlier session asked for a confirm. *Blocked:* 8.6.
- ~~**Q3. Does the cart mix borrowing and returning?**~~ *Blocked:* 8.4, 8.6.
- ~~**Q4. What happens when an item is scanned with nobody signed in?**~~ *Blocked:* 8.1.
- ~~**Q5. How does the cart die?**~~ Sign-out, idle timeout. *Blocked:* 8.4.
- ~~**Q6. Where does the overdue block bite?**~~ At sign-in with the cart disabled throughout, or at the
  checkout press. *Blocked:* 8.4, 8.5.
- ~~**Q7. Who may see who holds an item?**~~ Admins and the custodian only, or any signed-in student?
  *Blocked:* 8.3, 8.6.

Also resolved alongside these, from `CLAUDE.md` §13: `SESSION_IDLE_MINUTES` is **10 minutes**, measured from the last interaction rather than from sign-in (it was 5 when first decided; raised on 2026-09-14). The
scan-vs-typed keystroke threshold (§9) stays genuinely open — it needs real scanner hardware, arriving
Week 7 — but ships as a named constant defaulted to 50ms so tuning it later is a one-line change.

---

## 16. Decisions log

**2026-09-14, Add goes through the detail popup.** `CLAUDE.md` §1 has always described the flow as "click
an item → detail popup → Add to Cart", and the browse list was adding directly instead. Pressing **Add**
now opens the dialog and the dialog's own button commits — which matters most for exactly the case §8.2's
grouping created, three `Canon T7i` bodies whose rows differ only by serial. Items with nothing extra to
show skip the popup, because ceremony that tells you nothing is the thing §1.1 says to cut, and "nothing
extra" is defined once in `lib/add-flow.ts` beside the flag that disables the whole behaviour.

**2026-09-14, adding is a unit-row action, and Add toggles.** Two changes to §8.2 from using the list
against real grouped data. The model row's **Add** is gone: it took "any free unit", which reads fine in a
spec and badly on screen, because the press silently picked one of three identical-looking bodies and the
row never reported which serial you now had. The cost of removing it is one press — expand, then add the
unit you want — and what you get back is that every cart line was chosen, not assigned. And **the same
button now removes**: an added row shows `In cart` and pressing it takes the item out, where before it was
a disabled label and the only undo was the cart page. Both changes apply to the detail dialog and the scan
surface too, so the three places that can add an item all behave alike.

**2026-09-10, list shape: `B3 + B1`, one accordion instead of two views.** Closes the former list-shape question.
The earlier note left `B1` and `B3` as coexisting modes behind a toggle. A toggle is a setting somebody
has to find, and the two audiences are not two people: the same student wants a count on Tuesday and a
specific serial on Thursday. Folding `B1` into the expansion of a `B3` row serves both without a mode. Cost: a per-model
availability count that `ListAssets` does not return yet (`TODO.md` Phase 3), and one more component pair,
`<ModelRow>` over `<UnitRow>`. §8.2 has the rules.

**2026-09-10, cart: bottom dock, full page.** Replaces the dock-expands-into-a-drawer shape. The dock stays
because it is the part that stops someone walking off with an uncommitted cart. The drawer goes because
420px of a 1024px Wails window is too much to spend on a panel you visit once per checkout, and because a
cart page is the interaction every student already knows. The commit panel gets more room for the due-date
picker and the admin custodian picker, and the dock stops carrying a commit button it was too small for.
§8.4 has the rules.

**2026-09-12, §15 Q1 to Q7 resolved in a grilling session.**

- **Q1 → unattended self-service.** Matches `CLAUDE.md`'s described flow. "Check out on behalf of" stays
  the admin-only edge case it was already specced as; no change to 8.1/8.4 beyond confirming the assumption.
- **Q2 → `CLAUDE.md` wins: immediate check-in, no confirm.** §8.6 rewritten; the confirm-press branch for
  `checked_out` scans is gone.
- **Q3 → the cart never mixes borrow and return.** Scanning a checked-out item checks it in immediately and
  never touches the cart; the cart exists only to accumulate items being borrowed. Confirmed by the
  post-check-in green flash + "put it back" pattern §8.6 already described.
- **Q4 → explicit "sign in first" message.** Scanning an item barcode with no session active shows a
  dedicated prompt rather than trying to interpret the code as a student number and failing generically.
- **Q5 → the cart survives a reload.** It clears only on sign-out or the idle timeout (10 minutes since 2026-09-14), not on a
  page refresh — safer for someone who accidentally reloads mid-shopping than for the machine sitting
  unattended, and idle-timeout already covers the unattended case.
- **Q6 → both layers enforce the overdue block.** The cart/checkout UI disables itself the instant an
  overdue user signs in (as §8.4 already specced), *and* `CheckOutAssets` refuses server-side regardless of
  what the client sends. Belt and suspenders, not either/or.
- **Q7 → current custodian visible to any signed-in viewer, reversing `b6fe111`.** The original motivation
  (a student can find who has the lens they want) outweighs withholding it, and this project has no separate
  school privacy officer to seek sign-off from before shipping the open version. This applies only to who
  currently holds an item — `GetAssetHistory`'s past-custodian trail stays admin-only, and a non-admin's own
  history is available only via `GetUserHistory`. §8.2, §8.3, and §8.6 updated; `TODO.md` Phase 3/4 own the
  API shape (`ListAssets`/`GetAsset`/`ScanItem` include the current custodian for every actor,
  `GetAssetHistory` doesn't unless the actor is an admin).

Also settled in the same session: `SESSION_IDLE_MINUTES`, decided as 5 minutes and raised to **10** on 2026-09-14, measured from the last interaction. Browse-list sort order (not
previously specified anywhere): categories in `Catagories.md`'s document order, available units before
checked-out ones within any list. Backup (`CLAUDE.md` §11) moves from "local CSV, Drive client syncs it"
to "local CSV, then `rclone copy` pushes it directly" — a one-time human `rclone config` OAuth step replaces
writing custom Google API/OAuth code.
