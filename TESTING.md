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

- **smoke** — loads, zero console errors, live indicator, all fixed views navigable.
- **task-create** — open (q/c///+/palette); title-only; pills; token parse; overrides; add-more; view-aware prefill; cancel/esc/backdrop.
- **task-edit** — inline title (dblclick + `e`, save/cancel, disabled for synced); detail edits (title/notes/labels/due); revision conflict → info toast.
- **task-complete-delete** — complete (checkbox/`x`/palette/detail); reopen; delete (row/detail/palette); toasts.
- **undo** — complete / delete / reschedule / bulk.
- **command-palette** — open (⌘K + footer); fuzzy; groups; keyboard nav; run nav/action/task/extension commands; task search; throwing command caught.
- **search** — header search narrows the view; esc clears; with sort/board.
- **selection-bulk** — ⌘/shift-click, `Space`; bulk bar complete/schedule/label/delete; clear/esc; resets on view change.
- **sort** — smart grouping; created/title order; manual flat; drag-reorder persists order + auto-switches to Manual; calendar drop does NOT switch sort.
- **board** — list⇄board; group by priority/project; drag card between columns; card open; de-duped chips; coexists with rail; empty state.
- **calendar-timebox** — events (incl. completed via fetch); all-day strip; now-line; day nav; drag row → rail timeboxes; ✕ clears; block opens detail.
- **reassign** — drag task → sidebar project (swap) / label (add); drag-over highlight.
- **saved-views** — save command → dialog → sidebar; apply; remove; persists across reload.
- **dock** — detail peek + calendar coexist (no overlap); esc closes detail; responsive (narrow hides rail).
- **keyboard** — all shortcuts; `?` cheat sheet; esc closes overlays even off-card; inert while typing.
- **theme** — toggle (button/palette); persists; icons theme-aware; opt-in visual snapshots.
- **live-sync** — external create/update/delete reflects; pulse on external only; reconnect converges.
- **extensions** (with-extensions) — github presenter/detail; gcal rows/detail; panel present; extension command; `noon` token; error boundary (fixture) survives.
- **icons** — no emoji in DOM; SVGs in palette/pills/rows; github octicon colors.
- **completed** — server paging; reopen from completed.
- **first-run** (empty) — welcome + add.
- **a11y** — dialog/status roles + labels; focus in/out of overlays; keyboard nav; accessible names.

## Unit expansion (vitest)

`reorder`, `commands`, `actions`, `savedviews`, `notify`, `views`, `format`
(+ existing `store`, `quickadd`); extension `gh.ts` / gcal `util.ts` math.

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

All specs below exist and pass (81 e2e + 70 unit). The rig spawns a real
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

Extensions test their own surfaces with the same fidelity — see
`extensions/gcal/e2e`, `extensions/github/e2e` (Playwright) and
`extensions/{gcal,github}/web-src/*.test.ts` (vitest via `mockApi`). Full usage
and the ESM-scope requirement are in [`web/testkit/README.md`](web/testkit/README.md).

Remaining unit-only cells (not e2e'd): revision-conflict toast; per-source
presenter variants beyond gcal/github/testext.

## Definition of done

A screens×actions matrix, every cell green: each screen (sidebar, list,
board, completed, calendar rail, detail, overlays, palette, cheat sheet,
first-run) × each action (create/edit/complete/delete/undo/select/bulk/
sort/reorder/timebox/reassign/search/navigate/save-view/theme/keyboard),
plus the invariants (zero console errors, live-sync convergence, extension
isolation, theme-aware icons).
