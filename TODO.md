# taskd — TODO

Backlog from the 2026-07-04 UI/UX + DX review (vs Todoist / Linear). The
high-leverage and medium UI/UX tiers are done; what remains is listed under
**Remaining**.

## Remaining

### Product features (need core/proto changes)
- [ ] Sub-tasks / hierarchy (a `parent_id` field; additive)
- [ ] Recurring tasks (a recurrence-rule field + expansion)
- [ ] Reminders / scheduled notifications (a scheduler + delivery; `api.notify.browser` is the delivery stub)
- [ ] Comments / activity log on a task

### UI/UX polish (no backend change)
- [ ] Sort/board choice sticky in the URL (survives reload; today it's session-local)
- [ ] Right-dock resize + a detail↔full-page toggle (Linear-style peek)
- [ ] Drag-to-move / resize a timebox within the calendar rail; cross-week nav
- [ ] Keyboard reachability for drag actions (timebox/reorder have no keyboard path yet)

### Developer & extension experience
- [ ] Scaffold command: `taskd ext new <name>` (Go syncer + web-src + manifest + build wiring)
- [ ] Extension dev hot-reload (`build-web --watch` + `?ext-dev` auto-reload) + one documented dev loop
- [ ] Extension settings UI (extension declares a schema → host renders a form → writes its config)
- [ ] Extension test harness (run a bundle against a mock `api` in vitest)
- [ ] Document the extension API stability contract (pre-freeze → frozen guarantees)
- [ ] Distribution: `taskd ext install <url>` + trust prompt; capability/permission model; registry

### Real integrations (mock today)
- [ ] gcal: swap mock data for the Google Calendar API / private ICS URL (+ OAuth)
- [ ] github: swap mock data for the GitHub REST API; optional close-on-complete write-back

## Done

Backend & architecture:
- [x] Slim backend rewrite — one unversioned Connect API, integrations as clients
- [x] Extension architecture — host, `pkg/syncer`, `web/extension-api`, in-tree ics/gcal/github
- [x] `user_data` + timeboxing

Web app:
- [x] Web frontend — watch-replica store, embedded via go:embed
- [x] Layout skeleton: left sidebar · center · right dock · slim bottom bar · overlays
- [x] Detail-peek dock that coexists with the calendar panel (no overlap)
- [x] New-task overlay — quick-add token parsing + pills
- [x] Draggable rows, day-rail calendar panel, timeboxing, manual reordering (auto-switch)
- [x] Command palette (⌘K) + global search
- [x] Notifications system — undo (incl. bulk), errors, browser notifications
- [x] Row hover actions + inline reschedule; inline title edit
- [x] Multi-select + bulk actions
- [x] Drag task → sidebar project/label to reassign
- [x] Board (kanban) view — group by priority/project, drag between columns
- [x] Saved/named views pinned to the sidebar
- [x] Shortcuts cheat sheet (`?`), first-run empty state
- [x] Hand-authored SVG icon set (no deps), theme-aware; `api.icon` + extensions ship their own

Extension API surface:
- [x] `registerPresenter` / `registerView` / `registerPanel` / `registerCommand` / `registerQuickAddToken`
- [x] `api.hooks` / `getTasks` / `store` / `client` / `ui` / `dnd` / `notify` / `icon` / `format`
- [x] Error boundary around extension surfaces; re-exported generated `Task` type
