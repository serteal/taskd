# taskd — TODO

Backlog from the 2026-07-04 UI/UX + DX review (vs Todoist / Linear). Items
under **Current slate** are what we're building next; the rest is the backlog.

## UI/UX backlog

Medium:
- [ ] Inline title edit (double-click / `e`)
- [ ] Board (kanban) view — a second shape of the same data
- [ ] Saved/named views (pin filter+sort combos to the sidebar)
- [ ] Shortcuts cheat sheet (`?`) + real first-run / empty states

Longer-term (need core/proto changes):
- [ ] Sub-tasks / hierarchy (`parent_id`)
- [ ] Recurring tasks (recurrence rule)
- [ ] Reminders / scheduled notifications (scheduler + delivery)
- [ ] Comments / activity on a task

## Layout

- [x] Skeleton validated: left sidebar · center · right dock · slim bottom bar · overlays
- [x] ⌘K affordance in the bottom bar
- [ ] Right-region dock cleanup: detail "peek" and the calendar panel coexist without overlapping

## Developer & extension experience

- [ ] Scaffold command: `taskd ext new <name>` (Go syncer + web-src + manifest + build wiring)
- [ ] Extension dev hot-reload (`build-web --watch` + `?ext-dev` auto-reload) + one documented dev loop
- [ ] Extension settings UI (extension declares a schema → host renders a form → writes its config)
- [ ] Extension test harness (run a bundle against a mock `api` in vitest)
- [ ] Document the extension API stability contract (pre-freeze → frozen guarantees)
- [ ] Distribution: `taskd ext install <url>` + trust prompt; capability/permission model; registry

## Done

- [x] Slim backend rewrite — one unversioned Connect API, integrations as clients
- [x] Web frontend — watch-replica store, embedded via go:embed
- [x] Extension architecture — host, `pkg/syncer`, `web/extension-api`, ics/gcal/github
- [x] `user_data` + timeboxing
- [x] New-task overlay — quick-add token parsing + pills
- [x] Draggable rows, `registerPanel`, day-rail calendar, manual reordering (auto-switch)
- [x] Notifications system — undo, errors, browser notifications (`api.notify`)
- [x] Command palette (⌘K) + global search, with extension-contributed commands
- [x] Undo (complete / reschedule / delete, incl. bulk)
- [x] Row hover actions + inline reschedule
- [x] Multi-select + bulk actions
- [x] Drag task → sidebar project/label to reassign
- [x] Extension `registerCommand` / `registerQuickAddToken` / `getTasks`; error boundary; re-exported `Task`
