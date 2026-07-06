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

- **Local tasks are your task list.** `Inbox`, `All`, `Today`, `Upcoming`
  show tasks you created (`source == ""`) by default.
- **Sources are quarantined feeds.** Every synced item (`source != ""`)
  lives under its own **Sources** area in the sidebar and does **not** leak
  into Inbox/All/Today/Upcoming.
- **Filters promote what you want.** A user-defined filter (a saved
  predicate bound to a sidebar section) can pull a *subset* of a source
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

## Status

M1–M3 shipped 2026-07-05 (commits `ea32a77`→`b40c00b`; see git log for the
per-item breakdown). All of M3 (timeboxing) is done and removed from this
list — which is why the milestones below jump from M2 to M4. Everything from
M4 on is future work.

2026-07-06 release-readiness batch shipped the remaining M1 launch DX and
more, all removed below per this file's convention: auto-open + first-run
onboarding + explicit disconnected state; version stamping end-to-end
(`-version` flags, `GET /version`, shown in Settings → About); versioned
SQLite migrations (`PRAGMA user_version`); **promotion into built-in lists**
(`SavedFilter.showIn` → named sections in Today/Inbox/Upcoming), making the
"calendar events labelled `focus` → Today" promise real; Upcoming = strictly
future; sidebar/label quarantine (labels count local tasks only, `synced=1`
escape hatch); synced tasks can no longer be completed from the UI (the next
sync would revert it — write-back is still M4); global ⌘Z undo; versioned
localStorage envelopes; richer quick-add/CLI dates (`next monday`,
`in 2 weeks`, `fri 3pm`, time-of-day); reminders catch-up ("N tasks became
due while you were away"); Settings reorder + real daemon version + paused
sources; detail panel + calendar rail coexist ≥1440px.

A release pipeline (goreleaser, tag-triggered GitHub release workflow, a
checksum-verifying `install.sh`, CI) was built and verified in the same
batch, then removed the same day by decision — the repo carries no CI for
now. The **Release story** item below is the surviving roadmap entry.

---

## Release story (deliberately not built yet)

- [ ] **Prebuilt binaries + one-line install + CI.** Prebuilt
      `taskd`/`task`/`task-mcp` (web UI embedded) + bundled extension
      artifacts, an install one-liner, and CI to produce them. A working
      goreleaser + Actions + `install.sh` setup existed briefly on
      2026-07-06 (never committed); rebuild it when releasing becomes real.
      Version stamping (`internal/version`, `-ldflags` in the Makefile) is
      already in place and stays.

---

## M2 — Sources, not noise

*The source/task separation is shipped; this is the one lever left.*

- [ ] **Per-source configuration.** Let a source scope what it ingests (the
      doc-comments "only files under `src/`" case). Needs the extension to
      declare a config schema + the admin API to read/write the extension's own
      `config.yaml`. Pairs with the M5 extension-settings work; do it once.

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
- [ ] **API stability contract.** Document the extension API surface (6
      register hooks + `api.hooks/getTasks/store/client/ui/dnd/notify/icon/
      format`) and its pre-freeze → frozen guarantees.
- [ ] **Panel docking beyond "right."** `Panel.side` only supports `"right"`;
      add left/bottom if a second concrete consumer needs it (rule of three).
- [ ] **Expose source/extension status to panels.** The calendar rail shows a
      paused source's events with no indication they're stale — a panel can't
      currently tell that its own source is paused. Surface source/extension
      status through `api` (e.g. a status field/hook) so a panel can badge or
      dim a paused feed. Deferred pending API design.
- [ ] **Distribution.** `taskd ext install <url>` + a trust prompt; a
      capability/permission model (installing an extension = running code
      today); optionally a registry. Design carefully — this is the one place
      the "no sandbox, all first-party" assumption gets tested.

---

## M6 — Core product features

*Real features that each need an **additive** proto change (new field or RPC —
nothing renamed/removed, per the freeze plan in DESIGN.md §3). Sequence after
the app feels good, so the schema settles around real usage.*

*Recurring tasks and one-level sub-tasks shipped 2026-07-06 (additive
`recurrence`/`parent_id` fields; server-side roll-forward on complete;
re-parent-never-cascade deletes). Left for later in that area:
drag-to-nest, deeper nesting (rule of three), roll-up counts beyond the
open-subtask chip, recurrence anchored on completion date rather than
due date, and a per-rule TZID — recurrence rules currently follow the
daemon's local wall clock (documented in the proto; roll-forward preserves
each occurrence's local time-of-day across DST shifts), so a per-rule
timezone is the eventual fix only if the daemon ever serves clients in
other timezones. Deferred.*

- [ ] **Reminders while the app is closed** — a scheduler + delivery. In-app
      due reminders + the reopen catch-up summary shipped 2026-07-06; what
      remains is firing with no page open. For an installed PWA this likely
      means a service worker + local scheduling; the browser-notification
      stub (`api.notify.browser`) is the delivery seam.
- [ ] **Comments / activity log** — per-task notes/history (additive; consider
      whether this is a repeated field or a small side table).

---

## Cross-cutting

- [ ] **Detail panel: resize + full-page peek** (Linear-style) — today it's a
      fixed-width, non-resizable column.
- [ ] **Keyboard-first parity** — audit that every mouse action (schedule,
      timebox, reorder, context-menu items) has a keyboard path. (Calendar
      timebox nudging landed in M3; the rest of the app still needs a sweep.)
- [ ] **Persistence decision for settings/views** — see Open decisions;
      unblocks CLI/TUI sharing the same views.
- [ ] **Accessibility** — keep the zero-console-error + a11y e2e invariants
      green as the UI keeps changing.
- [ ] **Docs** — README/ARCHITECTURE/DESIGN kept in step with the source model
      and the install-as-app flow; an extension-author guide.

---

## Open design decisions

1. **Where do settings & saved filters live?** Client `localStorage` (fast,
   fine for a single installed app) vs a small **server-side settings/prefs
   surface** (new additive RPCs) so the CLI/TUI/web share one set of views and
   settings survive a cache clear. Recommendation: **localStorage for now;
   server-side prefs when a second client needs them** — except extension
   enable/disable, which is already daemon-side via `admin.AdminService`.
2. **`kind` field vs `source` discriminator.** Keep using `source != ""` to
   mean "not a plain local task" (no schema change, respects DESIGN.md's "no
   kinds"), vs adding an explicit `Task.kind`. Recommendation: **stick with
   the source discriminator**; revisit only if a source needs items that are
   genuinely non-task and non-event.
3. **Filter expressiveness.** Current `TaskFilter` is a single AND of
   dimensions — it can't express "local OR (github AND label:review)" in one
   query. For the web app, sections are arbitrary in-memory predicates over
   the replica (fully flexible, no proto change); only server-side/CLI queries
   are AND-limited. Recommendation: **rich predicates client-side; leave the
   proto filter AND-only** unless the CLI needs unions.
4. **Notification delivery.** Service worker + local scheduling vs a daemon
   background notifier vs native OS notifications. Tie to the M1 PWA and M6
   reminders.
