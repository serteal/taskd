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

## Status — 2026-07-05: M1–M3 shipped

M1–M3 landed in 8 commits (`ea32a77`→`5fc114e`). Verified: full web e2e
**106 passing** (zero-console-error invariant held), Go suite green, and a
live embedded-binary smoke (health, app HTML, PWA manifest/icon, admin +
task RPCs). **Remaining in these milestones:** M1 launch-DX auto-open +
release/CI, and M2 per-source configuration (needs extension config schema —
pairs with M5). Everything else below is done.

---

## M1 — Daily driver on localhost

*Make it the thing you actually open every day: installable, and free of the
rough edges. Mostly web-only; no proto changes.*

### Install & deploy
- [x] **PWA / installable app.** `manifest.webmanifest` + SVG app icon in the
      Vite build, `<link rel="manifest">` + theme-color in `index.html`, and a
      `.webmanifest` MIME registration in the daemon. Chrome "Install app" works
      on localhost. (`86a0c7e`)
- [ ] **First-run & launch DX.** *(remaining)* First-run empty state exists;
      still to do: `taskd` optionally auto-opens the browser + richer
      "install-as-app / connect a source" onboarding.
- [ ] **Release story.** *(remaining)* Prebuilt binaries + one-line install +
      CI — packaging infra, not yet built.

### UI polish (the list you flagged)
- [x] **Remove the header search bar.** Gone; `⌘K` is the search path; `search`
      dropped from `SavedView` (legacy entries still load). (`1c2aed1`)
- [x] **Calm hover state.** Rows no longer mutate on hover — chips stay, only the
      drag handle + grab cursor appear. No bin. (`d58bef6`)
- [x] **Row actions via context menu.** Right-click menu (schedule / priority /
      label / complete / delete / plan-today), reusing the shared pickers;
      synced tasks get the subset minus Rename. (`d58bef6`)
- [x] **Nicer view controls.** A single "View" popover (segmented List/Board,
      Sort by, Group by) replaces the debug-mono selects. (`1c2aed1`)
- [x] **Multi-line quick add.** Title + description textareas auto-grow;
      Enter submits, Shift+Enter newline. (`1c2aed1`)
- [x] **Todoist-style overdue.** The Overdue group header has a batch Reschedule
      (Today / Tomorrow / weekend / pick); per-task via the context menu + date
      cell. (`d58bef6`)
- [x] **Sticky view state.** Sort/board/group-by persist per view across
      reloads. (`1c2aed1`)

### Settings surface
- [x] **Settings shell.** Focus-trapped Settings overlay (Appearance /
      Extensions / About), opened from the sidebar gear + ⌘K. (`931f7bd`)
- [x] **Theme catalog.** 13 themes (paper/dusk, Catppuccin, Gruvbox, Nord, Rosé
      Pine, Solarized) with a live-swatch picker; applied via CSS vars + `.dark`
      + `data-theme`, persisted, `prefers-color-scheme` default. (`86a0c7e`)
- [x] **Enable/disable extensions.** `admin.AdminService` (hot toggle,
      config-persisted) + the Settings → Extensions toggles. (`e3bb44a` + `931f7bd`)

---

## M2 — Sources, not noise

*The source/task separation above. Web-side view model + a small daemon lever
for disabling sources.*

- [x] **Redefine built-in views to "local by default."** inbox/today/upcoming/
      all filter to `source == ""`; synced items stop leaking in. (`5fc114e`)
- [x] **Sidebar "Sources" area.** Synced feeds grouped under a "Sources"
      heading ("synced feeds"), each a per-source view. (`5fc114e`) *(Derived
      from live source strings; a connected-but-empty source registry is a later
      refinement.)*
- [x] **Filters → sections.** `SavedFilter` (labels/source/due/text predicate)
      pinned as a sidebar section via a FilterBuilder overlay; the predicate
      runs over the full replica so it can promote synced items onto a named
      surface. (`5fc114e`)
- [x] **Enable/disable a source (daemon).** Hot toggle via the admin API — stops
      the syncer + unserves the bundle, config-persisted. (`e3bb44a`)
- [ ] **Per-source configuration.** *(remaining)* Let a source scope what it
      ingests (the doc-comments "only files under `src/`" case). Needs the
      extension to declare a config schema + the admin API to read/write the
      extension's own `config.yaml`. Pairs with the M5 extension-settings work;
      deferred.
- [x] **Calendar as a pure consumer.** With calendar sources excluded from the
      lists (local-by-default), gcal events surface only in the calendar rail;
      timeboxing still targets your local tasks against them. (`5fc114e`)
- [x] **Update DESIGN.md.** New §5b documents the source/task/filter model as an
      evolution of §5/§5a (sources are feeds; list membership is opt-in). (`5fc114e`)

---

## M3 — Timeboxing that feels right

*The calendar rail is real but minimal: one day, fixed zoom, drop-to-create
only. Make timeboxing a first-class planning surface. Extension-side (gcal
web bundle) + a bit of core (timebox visibility).*

- [x] **Move a placed timebox.** Drag an existing block to a new time
      (preserves duration). (`089beab`)
- [x] **Resize duration.** Bottom-edge drag; min 15 min. (`089beab`)
- [x] **Zoom in/out.** −/+ buttons and ⌘/ctrl-wheel; persisted; all geometry
      scales with px-per-minute. (`089beab`)
- [x] **Better overlap handling.** Calendar-style packing (blocks widen into
      free columns, hover-to-front) replacing equal-width lanes. (`089beab`)
- [x] **Multi-day / week view + navigation.** Day / 3-day / Week toggle with
      span-stepped nav; sticky gutter + per-day headers. (`089beab`)
- [x] **Keyboard reachability.** Focus a block; ↑/↓ nudge start, shift+↑/↓
      duration, delete clears, enter opens. (`089beab`)
- [x] **Timeboxes visible outside the rail.** A '◷ HH:MM' chip on rows with a
      timebox (core `timebox.ts` helper, not the gcal extension). (`d58bef6`)
- [x] **Timebox any local task, from anywhere.** "Plan today" context action
      timeboxes without dragging. (`d58bef6`)

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
