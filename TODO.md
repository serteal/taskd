# taskd — Roadmap

*Rewritten 2026-07-05. Supersedes the flat 2026-07-04 UI/UX backlog. See
[DESIGN.md](DESIGN.md) for the architecture this builds on and
[ARCHITECTURE.md](ARCHITECTURE.md) for the current wiring.*

## Where this is going

`taskd` is a **personal** task backend you run on your own machine. One Go
daemon owns SQLite and serves one Connect API; every frontend is a client of
that API. The primary frontend is the **web app, installed as a localhost
Chrome/PWA app** — that is how a user lives in it day to day. The CLI, a
future TUI, and the MCP server are siblings. Integrations (calendar, GitHub,
doc-comments, …) are **out-of-tree extensions** a user drops into
`~/.taskd/extensions/`.

Two audiences drive the work:

- **Users** deploy the daemon locally, install the web UI as an app, connect
  a few sources, and want it to feel as calm and fast as Todoist/Linear.
- **Extension authors** write a source (a syncer) and optionally ship UI (a
  web bundle) against a small, stable API — without forking the core.

Everything below is organized around getting both of those right.

---

## The idea this roadmap turns on: sources ≠ tasks

Today every synced item **is** a task, mixed straight into your lists: a
GitHub issue lands in Inbox (no `project:` label), a calendar event shows up
in Today. That's noise. The target model:

- **Local tasks are your task list.** `Inbox`, `All`, `Today`, `Upcoming`
  show tasks you created (`source == ""`) by default.
- **Sources are quarantined feeds.** Every synced item (`source != ""`)
  lives under its own **Sources** area in the sidebar and does **not** leak
  into Inbox/All/Today/Upcoming.
- **Filters promote what you want.** A user-defined filter (a saved
  `TaskFilter` bound to a sidebar section) can pull a *subset* of a source
  into a surface — "GitHub PRs assigned to me → a **Reviews** section,"
  "calendar events labelled `focus` → Today." Promotion is opt-in, per
  criterion.
- **The calendar is a consumer, not a list.** Because calendar sources are
  excluded from lists by default, their events simply feed the calendar
  rail (for timeboxing) instead of cluttering Today. A source can be *either*
  "just feed the calendar" *or* "add real tasks" — the difference is entirely
  which filters/sections subscribe to it, not a source type.

**Why this is cheap:** the daemon already exposes `source` as a filter
dimension, and the web replica already watches *all* active tasks, so the web
app can slice them into sections in-memory with no schema change. We
deliberately do **not** add a `kind`/`type` field to `Task` (see Open
decisions). The only server-side additions the model needs are: a way to
**disable a source** (stop supervising its syncer) and, later, somewhere to
**persist filters/settings** so the CLI/TUI share them.

---

## M1 — Daily driver on localhost

*Make it the thing you actually open every day: installable, and free of the
rough edges. Mostly web-only; no proto changes.*

### Install & deploy
- [ ] **PWA / installable app.** Add `manifest.webmanifest` (name, icons,
      `display: standalone`, `theme_color`, `start_url`) + app icons to the
      Vite build so they land in `dist/` and get embedded/served; add
      `<link rel="manifest">` and `theme-color` to `web/index.html`. localhost
      is a secure context, so Chrome "Install app" works with just this.
- [ ] **First-run & launch DX.** `taskd` prints the URL and optionally opens
      the browser; a clean empty state that explains "install me as an app"
      and "connect a source." (First-run empty state exists; extend it.)
- [ ] **Release story.** Prebuilt `taskd`/`task`/`task-mcp` binaries (web UI
      embedded) + a one-line install; document the "run daemon, install web
      app" flow in the README.

### UI polish (the list you flagged)
- [ ] **Remove the header search bar.** Delete the in-header `Search…` input,
      its `search` state, and the client-side title/notes filter; `⌘K`
      already searches tasks. Follow-ups: drop `search` from `SavedView`, and
      update `a11y.spec.ts` / `search.spec.ts` which assert the old input.
- [ ] **Calm hover state.** Stop mutating a row's appearance on hover. Keep
      the label chips visible; show only a **drag-handle (dots) + grab
      cursor**. Remove the trash/bin-on-hover entirely.
- [ ] **Row actions via context menu.** Move delete/schedule/priority/label
      off hover into a **right-click context menu** (Todoist/Linear style),
      plus the detail panel and multi-select bulk bar. Deleting a task should
      never be a stray hover target.
- [ ] **Nicer view controls.** Replace the raw mono `sort`/`group` `<select>`
      and the `list`/`board` text toggle with a single tidy **"View" menu**
      (segmented list/board toggle with icons; sort + group-by inside a
      popover). Match the app's typographic weight instead of debug-mono.
- [ ] **Multi-line quick add.** The quick-add title should **auto-grow** to
      multiple lines for long titles instead of staying a 1-row textarea; keep
      Enter = submit, Shift+Enter = newline. (Stretch: pasting N lines offers
      "create N tasks.")
- [ ] **Todoist-style overdue.** A distinct **Overdue** treatment at the top
      of Today with one-click **Reschedule** (Today / Tomorrow / this weekend /
      pick), a per-task inline reschedule, and a **"reschedule all overdue →
      today"** action. (Today: only a red "Overdue" group header + generic
      schedule popover.)
- [ ] **Sticky view state.** Persist sort/board/group-by (per view) so a
      reload doesn't reset them — today they're session-local React state.

### Settings surface
- [ ] **Settings shell.** A real Settings screen (Todoist-like): sections for
      General, Appearance, Extensions, Keyboard, About. Greenfield — no
      settings UI exists today.
- [ ] **Theme catalog.** Promote theming from a light/dark boolean to a
      **named-theme picker**: ship several curated light+dark themes —
      Catppuccin (Latte/Frappé/Macchiato/Mocha), Gruvbox (light/dark), Nord,
      Rosé Pine, Solarized, plus the existing paper/dusk. A theme is just a set
      of CSS-var values (`--bg/--surface/--ink/--muted/--faint/--line/--accent/
      --warn`); add a registry + preview + persistence. Respect
      `prefers-color-scheme` for the default.
- [ ] **Enable/disable extensions** (needs the daemon — see M2).

---

## M2 — Sources, not noise

*The source/task separation above. Web-side view model + a small daemon lever
for disabling sources.*

- [ ] **Redefine built-in views to "local by default."** `Inbox`, `All`,
      `Today`, `Upcoming` filter to `source == ""`. Synced items stop leaking
      in. (Client-side predicate change over the replica; no proto change.)
- [ ] **Sidebar "Sources" area.** One entry per connected source (gcal:*,
      github, …), showing that source's items in its own view. Distinguish a
      *connected source* from an incidental `source` string — connected =
      installed+enabled extension, not merely "≥1 task exists."
- [ ] **Filters → sections.** A user-buildable filter (labels, source, due,
      text — the `TaskFilter` dimensions, as UI, no query language) that can be
      **pinned as a sidebar section**. This is the promotion mechanism: pull a
      slice of a source into a named surface. Generalizes today's localStorage
      "saved views" into first-class filters. Decide OR-across-sources support
      (see Open decisions).
- [ ] **Enable/disable a source (daemon).** A settings-writable list of
      disabled extensions the daemon reads to decide whether to supervise the
      syncer **and** serve its bundle; ideally hot (no restart). This is the
      one piece that can't be client-only — a running syncer keeps upserting
      regardless of any browser flag.
- [ ] **Per-source configuration.** Let a source scope what it ingests (the
      doc-comments "only files under `src/`" case). Mechanism: the extension
      declares a config schema → Settings renders a form → writes the
      extension's own `config.yaml` → the syncer reads it. (Overlaps the M5
      extension-settings item; do it once.)
- [ ] **Calendar as a pure consumer.** With calendar sources excluded from
      lists, the calendar rail becomes the *only* place their events surface —
      confirm nothing else renders `gcal:*` as rows, and that timeboxing still
      targets your local tasks against those events.
- [ ] **Update DESIGN.md.** Document the source/task/filter model as an
      evolution of §5/§5a (sources are feeds; list membership is opt-in).

---

## M3 — Timeboxing that feels right

*The calendar rail is real but minimal: one day, fixed zoom, drop-to-create
only. Make timeboxing a first-class planning surface. Extension-side (gcal
web bundle) + a bit of core (timebox visibility).*

- [ ] **Move a placed timebox.** Drag an existing timebox block to a new time
      (rewrites `user_data.timebox`). Today you can only create (drop) or clear.
- [ ] **Resize duration.** Edge-drag a timebox to change its end; today
      duration is hard-coded to 60 min with a 30-min drop snap.
- [ ] **Zoom in/out.** Make px-per-minute adjustable (buttons and/or
      ctrl-scroll); today it's a compile-time constant with a fixed 07:00–21:00
      band.
- [ ] **Better overlap handling.** Move from greedy equal-width lanes to
      Google-Calendar-style packing (min widths, hover-to-front, clearer
      separation of events vs timeboxes).
- [ ] **Multi-day / week view + navigation.** Bring back a week/multi-day grid
      (the code was extracted from one) with cross-week nav, so you can plan
      more than today.
- [ ] **Keyboard reachability.** A non-drag path to timebox/reschedule
      (drag-only actions have no keyboard equivalent today) — pick a slot from
      a menu, nudge with arrows.
- [ ] **Timeboxes visible outside the rail.** A timebox is invisible unless
      the gcal panel is open. Show a "planned HH:MM" indicator on the task row
      and/or a "Planned today" surface, so timeboxing isn't gcal-only.
- [ ] **Timebox any local task, from anywhere.** "Add to calendar" from the
      row/detail/context-menu, not only by dragging onto the rail.

---

## M4 — Real integrations

*Every source ships mocked data. Swap the single `mock.go` per extension; the
web halves are already production-shaped and won't change.*

- [ ] **gcal → real.** Google Calendar API (OAuth) and/or a private ICS URL;
      one `gcal:<account>` source per account. Keep it read-only into tasks.
- [ ] **github → real.** GitHub REST API; optional **close-on-complete**
      write-back via `syncer.WatchChanges` (the write-back primitive exists but
      is unused).
- [ ] **doc-comments source (new, reference example).** A syncer that scans a
      repo for `TODO`/`FIXME`/authored comments and mirrors them as source
      items, **scoped by config** (folders/globs). This is the canonical proof
      of "a configurable source that is quarantined by default and promoted via
      a filter" — build it to validate M2.
- [ ] **Secrets/config handling.** A documented, safe place for tokens
      (extension folder, `0600`, never served over HTTP — already true; make it
      a documented convention + surface auth status in Settings).

---

## M5 — Extension author experience

*Lower the cost of writing a source + UI. `web/testkit` and the `?ext-dev=`
live-load hook already exist; build the rest of the loop.*

- [ ] **Scaffold.** `taskd ext new <name>` → manifest + Go syncer stub +
      `web-src` + build wiring. Today you hand-copy an existing extension.
- [ ] **Extension settings schema.** An extension declares a config schema;
      the host renders a Settings form and writes the extension's config
      (shared with M2 per-source config).
- [ ] **Hot reload / one documented dev loop.** `build-web --watch` exists
      (rebuild, not HMR) and `?ext-dev=<url>` can live-load from a dev server —
      wire these into a single documented "edit → see it" loop.
- [ ] **Test harness.** Formalize running a bundle against a mock `api` in
      vitest (the `web/testkit` `mockApi` + `defineExtensionE2E` are the
      foundation).
- [ ] **API stability contract.** Document the extension API surface (5
      register hooks + `api.hooks/getTasks/store/client/ui/dnd/notify/icon/
      format`) and its pre-freeze → frozen guarantees.
- [ ] **Panel docking beyond "right."** `Panel.side` only supports `"right"`;
      add left/bottom if a second concrete consumer needs it (rule of three).
- [ ] **Distribution.** `taskd ext install <url>` + a trust prompt; a
      capability/permission model (installing an extension = running code
      today); optionally a registry. Design carefully — this is the one place
      the "no sandbox, all first-party" assumption gets tested.

---

## M6 — Core product features

*Real features that each need an **additive** proto change (new field or RPC —
nothing renamed/removed, per the freeze plan in DESIGN.md §3). Sequence after
the app feels good, so the schema settles around real usage.*

- [ ] **Sub-tasks / hierarchy** — a `parent_id` field; roll-up counts, nested
      rendering, drag-to-nest.
- [ ] **Recurring tasks** — a recurrence-rule field + expansion (on complete,
      spawn the next); natural-language "every weekday" in quick-add.
- [ ] **Reminders / scheduled notifications** — a scheduler + delivery. For an
      installed PWA this likely means a service worker + local scheduling; the
      browser-notification stub (`api.notify.browser`) is the delivery seam.
- [ ] **Comments / activity log** — per-task notes/history (additive; consider
      whether this is a repeated field or a small side table).
- [ ] **Richer natural-language dates** in quick-add — "next monday", "in 2
      weeks", "fri 3pm" (today: today/tomorrow/Nd/weekday/ISO).

---

## Cross-cutting

- [ ] **Detail panel: resize + full-page peek** (Linear-style) — today it's a
      fixed-width, non-resizable column.
- [ ] **Keyboard-first parity** — audit that every mouse action (schedule,
      timebox, reorder, context-menu items) has a keyboard path.
- [ ] **Persistence decision for settings/views** — see Open decisions;
      unblocks CLI/TUI sharing the same views.
- [ ] **Accessibility** — keep the zero-console-error + a11y e2e invariants
      green as the UI is reworked (search-bar removal, hover changes, settings,
      themes all touch tested surfaces).
- [ ] **Docs** — README/ARCHITECTURE/DESIGN kept in step with the source model
      and the install-as-app flow; an extension-author guide.

---

## Open design decisions

1. **Where do settings & saved filters live?** Client `localStorage` (fast,
   fine for a single installed app) vs a small **server-side settings/prefs
   surface** (new additive RPCs) so the CLI/TUI/web share one set of views and
   settings survive a cache clear. Recommendation: **localStorage for M1;
   server-side prefs when a second client needs them** — except
   extension enable/disable, which must be daemon-side from the start.
2. **`kind` field vs `source` discriminator.** Keep using `source != ""` to
   mean "not a plain local task" (no schema change, respects DESIGN.md's "no
   kinds"), vs adding an explicit `Task.kind`. Recommendation: **stick with
   the source discriminator**; revisit only if a source needs items that are
   genuinely non-task and non-event.
3. **Filter expressiveness.** Current `TaskFilter` is a single AND of
   dimensions — it can't express "local OR (github AND label:review)" in one
   query. For the web app, sections can be arbitrary in-memory predicates over
   the replica (fully flexible, no proto change); only server-side/CLI queries
   are AND-limited. Recommendation: **rich predicates client-side; leave the
   proto filter AND-only** unless the CLI needs unions.
4. **Notification delivery.** Service worker + local scheduling vs a daemon
   background notifier vs native OS notifications. Tie to M1 PWA and M6
   reminders.
5. **Multi-line quick-add scope.** Auto-growing single task (simple) vs
   multi-line-to-multiple-tasks (Todoist paste behavior). Recommendation:
   **auto-grow first; multi-task paste as a later opt-in.**

---

## Done

Backend & architecture:
- [x] Slim backend — one unversioned Connect API, integrations as clients
- [x] Extension architecture — dumb host, `pkg/syncer`, `web/extension-api`,
      in-tree ics/gcal/github
- [x] `user_data` + timeboxing persisted (survives source re-sync)
- [x] Default port 8888

Web app:
- [x] Watch-replica store, embedded via `go:embed`, SPA fallback
- [x] Layout: sidebar · center · right dock · bottom bar · overlays
- [x] Detail-peek dock that coexists with the calendar panel
- [x] Quick-add overlay — token parsing + overridable pills
- [x] Draggable rows, day-rail calendar, drop-to-timebox, manual reorder
- [x] Command palette (⌘K), notifications + undo (incl. bulk), browser notifs
- [x] Inline title edit + inline reschedule; multi-select + bulk actions
- [x] Drag task → sidebar project/label to reassign
- [x] Board (kanban) view; saved/named views (localStorage); shortcuts sheet
- [x] Hand-authored theme-aware SVG icon set (no deps); `api.icon`

Extension API surface:
- [x] `registerPresenter/View/Panel/Command/QuickAddToken`
- [x] `api.hooks/getTasks/store/client/ui/dnd/notify/icon/format`; error
      boundary around extension surfaces

Testing (see [TESTING.md](TESTING.md)):
- [x] Playwright e2e against a real per-test `taskd`, both daemon modes,
      zero-console-error invariant, self-building harness
- [x] vitest units for pure web logic + extension `web-src` math
- [x] Reusable `web/testkit` — `defineExtensionE2E` + `mockApi`; gcal/github
      test their own surfaces with the same rig
