# taskd — UI test plan

The web app is a thin, live, optimistic client over the Connect API with a
lot of interaction surface (drag, keyboard, watch-driven live updates,
extensions). This plan gives it a full UI test suite.

## Approach

- **Primary: real-daemon end-to-end (Playwright).** Each test runs against a
  real `taskd` (webui build) on an ephemeral port + temp data dir, seeded via
  the Connect JSON API, driven in a browser. Highest fidelity: exercises the
  store, the watch/live-replica loop, sync, the embedding path, and the
  extensions — not React in isolation. (This is also the only layer that
  catches the classes of bug we actually hit: a stale embedded bundle, and
  the Escape-doesn't-close-overlay focus bug.)
- **Secondary: vitest units** for pure logic (parsing, ordering, command
  building, undo closures, saved-views, view filters, notify).

## Determinism (the hard part)

| Gotcha | Handling |
|---|---|
| Native HTML5 DnD ignores mouse simulation | `dragTaskTo()` helper dispatches dragstart/dragover/drop with one shared DataTransfer |
| Optimistic write + watch echo race | Only web-first, auto-retrying assertions; never sync DOM reads after an action |
| Toasts auto-dismiss (4–7s) | `?test=1` disables toast auto-dismiss |
| Time-dependent buckets / now-line | `page.clock.setFixedTime(FIXED_NOW)`; seed due dates relative to it |
| Animations | Playwright `reducedMotion: 'reduce'` (our pulse/caret are under `prefers-reduced-motion: no-preference`) |
| Native `<select>` value restore | Set via `selectOption`, assert behavior not the raw DOM value |
| Focus traps | Reset focus before global-key tests; dedicated Esc-closes-overlay test |
| Rail hidden below `md` | Default viewport 1280×800; a responsive test uses a narrow one |
| State bleed | Fresh daemon + temp dir per test (isolation); extensions data only in the "with-extensions" daemon |

## App test-hooks (inert in normal use)

- `?test=1` — disables toast auto-dismiss.
- `data-testid`s where text/role isn't stable: board columns, calendar rail +
  blocks, bulk bar, sidebar sections, saved-view items, connection dot.
- A hand-written **test-fixture extension** (`web/e2e/fixtures/testext/`):
  registers a presenter for `source:"test"` with a throwing `DetailSection`
  (error-boundary test), an "ok" and a "boom" command (command error test),
  and a quick-add token — no build step (plain `React.createElement`).

## Fixtures & helpers (`web/e2e/`)

- `daemon` fixture: spawns `taskd` (clean or with-extensions), temp dir,
  health wait, teardown.
- `api`/`seed`: `createTask`, `upsertExternal`, `getTask`, `listActive`.
- `dragTaskTo` / `dragCardToColumn` / `dropOnSidebar`; `openPalette` /
  `runCommand`; `expectToast` / `clickUndo`.
- Light page objects: Sidebar, Header, TaskList, Board, DetailPanel,
  CalendarRail, Overlay, Palette.

## e2e specs (all tests)

The 36 core specs live in `web/e2e/`; the 2 extension-owned specs are listed
under the test-kit section below.

- **smoke** — loads, zero console errors, live indicator, all fixed views navigable.
- **first-run** (empty) — welcome + add; empty-state affordances.
- **task-create** — open (q/c///+/palette); title-only; pills; token parse; overrides; add-more; view-aware prefill; cancel/esc/backdrop.
- **task-edit** — inline title (dblclick + `e`, save/cancel, disabled for synced); detail edits (title/notes/labels/due); revision conflict → info toast.
- **task-complete-delete** — complete (checkbox/`x`/palette/detail); reopen; delete (row/detail/palette); toasts.
- **context-menu** — right-click row menu (Open/Rename/Complete/Priority/Add-label/Plan-today); outside-click + Esc dismiss; synced rows omit inline edit and hide Schedule (due is source-owned).
- **detail-panel-extensions** — panel field-ownership on synced tasks (title/due disabled, labels/notes still editable, Complete disabled); local-task footer Complete → close + undo; extension DetailSections stay scoped to their source; row-swap clears stale title/notes + label draft; Esc closes from inside Notes; delete → undo toast, no confirm.
- **undo** — complete / delete / reschedule / bulk.
- **undo-edge-cases** — reschedule restores the *original* due (not null/new); complete-undo is a narrow patch (won't clobber a concurrent rename); synced delete offers no undo; bulk-delete undo recreates only the local tasks; stacked toasts each target their own task; global ⌘Z (checkbox complete, detail edit, "Nothing to undo"); delete restores fidelity (re-nests a child, restores a recurrence rule); bulk reschedule restores each task's own due.
- **command-palette** — open (⌘K + footer); fuzzy; groups; keyboard nav; run nav/action/task/extension commands; task search; throwing command caught.
- **command-palette-extra** — saved views/filters reachable as "Go to" commands; selected-task action commands (Open/Schedule/Complete/Delete, no confirm) hidden when nothing selected; Go to project/label/source navigation; ArrowUp/Down highlight + Enter runs the highlighted item; explicit "No matches"; backdrop-only close.
- **search** — header search narrows the view; esc clears; with sort/board.
- **keyboard** — all shortcuts; `?` cheat sheet; esc closes overlays even off-card; inert while typing.
- **sidebar-collapse** — collapse to an icon rail; expand restores; state persists across a reload.
- **selection-bulk** — ⌘/shift-click, `Space`; bulk bar complete/schedule/label/delete; clear/esc; resets on view change.
- **sort** — smart grouping; created/title order; manual flat; drag-reorder persists order + auto-switches to Manual; calendar drop does NOT switch sort.
- **board** — list⇄board; group by priority/project; drag card between columns; card open; de-duped chips; coexists with rail; empty state.
- **sources-model** — synced items are quarantined out of the built-in lists but live in their Source view; a filter promotes a chosen synced subset; syncer labels stay out of the sidebar (local-by-default + `synced=1` escape hatch); gcal events feed the rail, not the lists.
- **promotion** — a Today-promoting filter (`showIn`) surfaces its synced task into Today, counts it, and never duplicates it.
- **filter-builder** — labels any-of/all-of; text substring; due has/within-N-days; no "include completed" control; live match-count preview; edit pre-fills and updates in place; "Also show in" (`showIn`) persists through save; remove; cancel/backdrop; Save disabled on empty name + ⌘/Ctrl+Enter saves; compound source+label AND-combines; Esc closes only the open popover.
- **paused-source** — disabling an extension paints the sidebar pill + source banner live; re-enabling clears them.
- **saved-views** — save command → dialog → sidebar; apply; remove; persists across reload.
- **overdue-reschedule** — the Overdue section header batch-reschedules every overdue task in view.
- **calendar-timebox** — events (incl. completed via fetch); all-day strip; now-line; day nav; drag row → rail timeboxes; ✕ clears; block opens detail.
- **reassign** — drag task → sidebar project (swap) / label (add); drag-over highlight.
- **dock** — detail peek + calendar coexist (no overlap); esc closes detail; responsive (narrow hides rail).
- **recurring-subtasks** — recurring: quick-add `every 3 days` chip + repeat glyph, clear the chip, complete advances due + archives a frozen copy (undo reverses both), detail editor presets/custom/reject-garbage/clear, synced tasks get no recurrence editor; subtasks: add via detail + parent count chip, All-view nesting + collapse (hidden from j/k), a dated child nests under an undated parent, a child due today shows the parent breadcrumb, deleting the parent re-parents the child, complete a subtask from the parent panel.
- **settings** — open/close (sidebar/⌘K/Esc); About shows daemon version + address; theme picker live + light/dark toggle; startup-view picker; clean daemon lists no extensions; due-reminders (permission persists, fires on newly-due not already-overdue, closed-app catch-up summary); extensions list with capability badges + fed sources/counts + toggle.
- **theme** — toggle (button/palette); persists; icons theme-aware; opt-in visual snapshots.
- **live-sync** — external create/update/delete reflects; pulse on external only; reconnect converges.
- **completed** — server paging; reopen from completed.
- **completed-list-extra** — Load-more pagination (no drop/dup); `+N` label overflow; most-recently-completed-first ordering stays stable under edits; reopen one of several returns only it to the active set; a page-one reopen keeps the page-two cursor coherent; synced origin chip; not live (fetched once on mount).
- **extensions** (with-extensions) — github presenter/detail; gcal rows/detail; panel present; extension command; `noon` token; error boundary (fixture) survives.
- **icons** — no emoji in DOM; SVGs in palette/pills/rows; github octicon colors.
- **a11y** — dialog/status roles + labels; focus in/out of overlays; keyboard nav; accessible names.
- **coverage-extra** — cross-cutting gap-fills: reopen-from-completed, Space-to-select, detail due-date edit, row-hover Schedule/Priority menus, bulk Schedule, new-task Priority pill, keep-open, view-aware project prefill.

## Unit expansion (vitest)

18 pure-logic suites under `web/src/lib/`: `actions`, `admin`, `commands`,
`crosstab`, `extensions`, `filters`, `format`, `nest`, `notify`, `quickadd`,
`recur`, `reminders`, `reorder`, `savedviews`, `storage`, `store`, `undo`,
`views`. Plus the extension `web-src` math, run in the same vitest pass:
gcal `presenter` / `util`, github `gh` / `main`.

## Running

- **Unit** — `make test-web` (`cd web && npm run typecheck && npm test`).
  vitest, node env; covers `web/src/**` plus the extension `web-src` math
  (`extensions/*/web-src/**/*.test.ts`, wired in via `vite.config.ts`).
- **e2e** — `make test-web-e2e` (`cd web && npx playwright install chromium &&
  npx playwright test`). `web/e2e/global-setup.ts` builds the current bundle,
  the `-tags webui` daemon, and the extensions before the run, so tests never
  hit a stale artifact (set `E2E_SKIP_BUILD=1` to skip the build when
  iterating on specs only). Config: chromium, retries 2 on CI, trace on first
  retry, `reducedMotion: 'reduce'`, 1280×800.

Always run these from `web/` (Playwright resolves its config from the cwd).

## Status — implemented

All specs exist and pass (220 e2e across 38 spec files — 36 core + 2
extension-owned — and 303 unit across 22 files). The rig spawns a real
`taskd` per test on an ephemeral port + temp dir, freezes the browser clock
with `page.clock.setFixedTime`, pins the zone to UTC, and asserts **zero
console errors** on teardown (opt out per-test with
`expectNoConsoleErrors: false` for the deliberate error paths). Core's
`fixtures.ts` has two modes — `clean` and `extensions` (stages the gcal/github
mocks + the `testext` fixture) via `test.use({ mode })`.

The high-value gaps flagged in review are covered in `coverage-extra.spec.ts`:
reopen-from-completed, Space-to-select, detail due-date edit, row-hover
Schedule/Priority menus, bulk Schedule, new-task Priority pill, keep-open, and
view-aware project prefill. Calendar **events** (timed/all-day/now-line/day-nav)
and the github **detail** section are covered by the extension specs below.

## Reusable test kit — `web/testkit/`

The rig is a shared kit, not core-only: `daemon.ts` (spawn a taskd with any
extension set staged), `helpers.ts` (locators, DnD, palette, toasts, seed),
`e2e.ts` (`defineExtensionE2E` — the extension-author entry point), and
`unit.ts` (`mockApi` / `makeTask` for unit-testing presenters/commands). Core's
`web/e2e/fixtures.ts` is a thin consumer of it.

Extensions test their own surfaces with the same fidelity, and their
Playwright specs run in the same 220-test count:

- **gcal/e2e/calendar** — rail renders a timed event + opens its detail;
  all-day strip; day nav (now-line disappears/returns); single-day only (no view toggle);
  zoom taller; keyboard nudge/clear a timebox; drag the top handle to resize
  the start.
- **github/e2e/github** — issues render with a `repo#number` subtitle; the
  detail section; the palette count command.

Plus `extensions/{gcal,github}/web-src/*.test.ts` (vitest via `mockApi`). Full
usage and the ESM-scope requirement are in
[`web/testkit/README.md`](web/testkit/README.md).

Remaining unit-only cells (not e2e'd): revision-conflict toast; per-source
presenter variants beyond gcal/github/testext.

## Definition of done

A screens×actions matrix, every cell green: each screen (sidebar, list,
board, completed, calendar rail, detail, overlays, palette, cheat sheet,
first-run) × each action (create/edit/complete/delete/undo/select/bulk/
sort/reorder/timebox/reassign/search/navigate/save-view/theme/keyboard),
plus the invariants (zero console errors, live-sync convergence, extension
isolation, theme-aware icons).
