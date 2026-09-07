# design-sync notes (p-ui-frontend)

Repo-specific gotchas for future syncs. Read this before touching `config.json`.

## This is an app, not a component library

`frontend/` is a private Vite app with no library build and no `.d.ts` tree, so
three pieces of scaffolding exist only for design-sync. All are committed:

- **`frontend/ds-entry.tsx`** — the bundle entry (`cfg.entry`). Re-exports the 27
  storied components, `antd`, `react-hook-form`, the theme module, and
  `PanelProvider`. Nothing in the app imports it.
  Imports must use **relative** paths (`./src/...`), never the `@/` alias: the
  emitted `.d.ts` keeps import specifiers verbatim, and the converter's ts-morph
  project has no `paths` mapping, so `@/`-spelled re-exports resolve to nothing
  and every component is dropped as `[TITLE_UNMAPPED]`.
- **`frontend/ds-shim.ts`** — imported first by `ds-entry.tsx`. See below.
- **`frontend/tsconfig.ds-sync.json`** + `"types"` in `frontend/package.json` —
  emit the `.d.ts` tree the converter's component discovery reads.
  **Re-run `npx tsc -p tsconfig.ds-sync.json` from `frontend/` before every
  converter build**, or discovery works from stale declarations.
  Output `frontend/dist-types/` is gitignored.

## The module-scope side effects in useTheme.tsx

`src/hooks/useTheme.tsx` calls `readBool()` (reads `localStorage`) and
`applyDom()` (writes `document.body.classList`) at **import time**, lines 30-32.
The panel always loads it from a parsed document on a real origin, so this never
bites in production — but any embedder that evaluates the bundle before `<body>`
exists, or on an opaque origin, gets a `TypeError` that kills the whole IIFE and
leaves `window.Penhoon` undefined.

`ds-shim.ts` neutralizes both for the design bundle only (memory-backed
`localStorage` fallback, `document.body` created if missing). The app source is
deliberately untouched. **If someone hardens `useTheme.tsx` itself, delete
`ds-shim.ts` and its import.**

## Duplicate-copy failures (all fixed via cfg, all the same root cause)

A story helper bundled *from source* owns a **second copy** of a module whose React
context or module-level state the shipped component reads from the *bundle* copy.
The symptom is always a runtime error or a silently wrong render, never a build
failure. Five instances, all fixed:

| Module | Symptom | Fix |
|---|---|---|
| `.storybook/preview.tsx` decorators (bundle their own antd) | `Element type is invalid` in every cell | `cfg.provider = {component: "PanelProvider"}` — skips decorator bundling entirely. **Do not remove it.** |
| `@/hooks/useTheme` | `useTheme must be used inside <ThemeProvider>` | `cfg.storyImports.shim` |
| `react-hook-form` + `./useZodForm` | `Cannot read properties of null (reading '_names')` | `cfg.storyImports.shim` |
| `antd` (stories import `Form`, `Input`, … directly) | the story's `<Form layout="vertical">` never reached `FormField`'s `Form.Item` — labels rendered beside fields instead of above | `cfg.storyImports.shim` |
| `@/hooks/useDatepicker` | the Jalali story rendered gregorian, **and** a "Request failed with status 404" toast appeared in every cell (the story's `setDatepicker` never set the module's `fetched` flag, so it fetched panel settings) | `cfg.storyImports.shim` |

A shim only works because `ds-entry.tsx` also **exports** the symbols the stories
import (`ThemeProvider`, `FormProvider`, `useZodForm`, `setDatepicker`, all of
`antd`, …). Adding a shim pattern without the matching export yields `undefined`.

The antd pattern targets the runtime entry path exactly (`node_modules/antd/es/index.js`)
rather than the bare string: every `antd/es/...` import in this repo is `import type`
(erased at compile time), but a broad pattern would also shim real subpath imports.

## Verify-loop findings

- **[GENERAL] The storybook harness pins the light theme; the bundle must too.**
  `.storybook/preview-head.html` seeds `localStorage['dark-mode']='false'` before
  anything loads. `useTheme.tsx` reads that at module scope and **defaults to
  dark**, so without the same seed every theme-consuming component rendered dark
  against a light reference — caught on ClientTrafficCell's `Disabled Compact`
  (dark slate bar vs the reference's grey). `ds-shim.ts` now seeds both keys.
  Previews are therefore light-only; there is no dark card.
- **[GENERAL] `play` functions do not run in previews.** Storybook executes them,
  the preview harness does not, so any story whose visible state is produced by a
  `play` interaction renders its *initial* state instead. `TextModal.PlainText`
  is the case here: an owned `.design-sync/previews/TextModal.tsx` renders the
  opened modal directly. Check new stories for `play` before grading a diff.
- **Modals portal outside storybook's captured story root**, so the reference
  panel shows only the dimmed mask while the preview shows the real modal. That
  is the reference being the artifact — grade the component, not the reference.
  Overlay components get `cardMode: "single"` so the product card is not painted
  over by a sibling cell.
- `DateTimePicker` stories raise an antd `message` toast ("Request failed with
  status 404") that escapes its grid cell; `cardMode: "single"` with
  `primaryStory: "ClientExpiry"` is why that component is a single card.

## Type contracts are hand-written for 10 components

`cfg.dtsPropsFor` carries hand-written `<Name>Props` bodies for BansTable,
ClientSpeedTag, TextModal, EmailNotifications, TelegramNotifications,
NotificationGroup, Sparkline, FormField, DateTimePicker and InputAddon.

Why: the emitted `.d.ts` keeps import specifiers verbatim but ships no imports, so
prop types coming from another module (`BanView`, `AllSetting`,
`NotificationGroupConfig`, `Dayjs`, `CSSProperties`, react-hook-form's `Control`)
appeared as **unresolvable names** in the very contract the design agent codes
against. The hand-written bodies inline those shapes. `AllSetting` is a
~120-property class — deliberately left as `Record<string, unknown>` with a comment
rather than inlined.

If a component's real props change, its `dtsPropsFor` entry must be updated by
hand — nothing detects the drift.

## Known warnings (triaged — do not re-chase)

- `[TOKENS_MISSING]` — 12 `--ant-*` custom properties. Expected: antd 6 is CSS-in-JS
  and injects them at runtime under `PanelProvider`. Confirmed from rendered previews.

## Windows

`package-build.mjs` intermittently dies with `EPERM ... rm ds-bundle` when something
briefly holds a handle on the output dir. It is transient — **just re-run the build**;
it has succeeded on the retry every time. Do not go hunting for a culprit process;
the user's own Chrome windows are not it.

## Two Claude Design projects exist — do not mix them up

This repo syncs to **"Penhoon Panel Components"**
(`7a3bfc6f-febf-4c36-908a-ad2db06cc8d0`, pinned as `projectId` in config.json).

There is a **separate** project called **"Penhoon Admin UI"**
(`8f316a2e-8037-46dc-a6b2-751a0df663e0`) holding a different, larger design
system: ~130 shadcn/Radix-style components, four canvas templates
(dashboard, page-detail, page-queue, component-gallery), an `explorations/`
folder, JetBrains Mono + Vazirmatn fonts and a `HANDOFF-v3.md`. It is **not**
produced by this repo and nothing here should ever write to it — a sync there
would delete all of it. If a future run finds no `projectId` in config.json,
create a new project rather than adopting that one.

## Re-sync risks

- **Stale declarations.** `dist-types/` is generated and gitignored; a re-sync
  that skips the `tsc` step silently syncs the previous run's component set.
- **A new storied component** must be added to `ds-entry.tsx` by hand or it is
  dropped as `[TITLE_UNMAPPED]` — the barrel is not generated.
- **A new story that imports a context-owning helper** will hit the duplicate-copy
  class above. Add the module to `cfg.storyImports.shim` *and* export its symbols
  from `ds-entry.tsx`.
- `[TOKENS_MISSING]` reports 12 `--ant-*` custom properties. Expected: antd 6 is
  CSS-in-JS and injects them at runtime under `PanelProvider`. Confirm from a
  rendered preview, not from the stylesheet.
- Previews are pinned to the **light** theme (`PanelProvider` hardcodes it).
  The storybook toolbar's dark toggle has no equivalent in the synced output.
- Built against Node 26.5.1 / npm 11.17.0 on Windows; `.nvmrc` says 24.
- **All 27 components graded `match` against the repo's own storybook** (2026-09-07).
  Several stories run `play` functions, so the storybook reference shows a
  post-interaction state the preview cannot reproduce; those verdicts carry a note
  saying so. If a later sync surfaces one of them as a diff, read the note before
  "fixing" anything.
