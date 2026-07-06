# @taskd/extension-api

The contract a taskd **web extension** builds against. An extension's
frontend half is an ESM bundle at `<extension>/web/main.js` whose default
export is a `TaskdExtension`; the app imports it at startup and calls
`register(api)`. The full typed surface is [`index.d.ts`](index.d.ts).

## What an extension can do

Inside `register(api)`:

- **`api.registerPresenter({ match, rowMeta?, DetailSection? })`** —
  customize how *your* tasks render. `match(task)` selects them (usually by
  `source`); `rowMeta` returns row extras (`icon`, `subtitle`, `timeText`,
  `extraChips`) so rows stay visually consistent; `DetailSection` is a React
  component shown in the detail panel for matching tasks.
- **`api.registerView({ id, title, Component })`** — contribute a whole
  screen. It gets a sidebar entry and the URL `?ext=<id>`; `Component`
  receives `{ api }`.
- **`api.registerPanel({ id, title, side, width, defaultOpen, Component })`** —
  contribute a *persistent* panel docked beside the main view (visible on
  top of whatever view the user is in). Use it for always-available surfaces
  like a day timeline. A panel is an ordinary drop target — see dnd below.
- **`api.registerCommand({ id, title, group?, icon?, keywords?, when?, run })`** —
  add an entry to the ⌘K command palette.
- **`api.registerQuickAddToken({ match, hint? })`** — interpret a word in the
  new-task field, e.g. `match: t => t === "noon" ? { due: todayNoon } : null`.
- **`api.registerTheme({ id, label, group, mode, vars })`** — contribute a
  theme to Settings' theme picker. It shows alongside the built-in catalog,
  grouped under `group` (e.g. your extension's name); `mode` is `"light"` or
  `"dark"` and `vars` sets the eight theme CSS variables
  (`bg surface ink muted faint line accent warn`).
- **`api.hooks.useTasks()` / `useNow()`** — the live task replica and a slow
  clock, as React hooks, for use in your components.
- **`api.getTasks()`** — a one-shot snapshot of active tasks for imperative
  code (a command's `run()`), where a hook can't be used.
- **`api.notify.toast/error/browser(...)`** — transient toasts (with an
  optional action button) and native browser notifications.
- **`api.icon(name, opts?)`** — a crisp SVG from the core icon set (calendar,
  tag, flag, check, trash, circle, …) for `RowMeta`/`Command` icon fields.
  Icons use `currentColor`. To ship your own look instead, inline any `<svg>`
  in those fields (the github extension does this for colored state icons).
- **`api.store.create/update/delete`** — optimistic mutations. To write your
  own structured data, use `user_data` (user-owned, never touched by sync):
  `api.store.update(id, { userData: { ...task.userData, mine: {...} } })`.
  The host enforces the `TaskPatch` field-ownership contract on `update` at
  runtime: patch keys outside `TaskPatch` — host-managed structure like
  `recurrence`/`parent_id` — are dropped with a one-time console warning, and
  `completed: true` on a *synced* task is stripped (its completion follows the
  source). `update` resolves to `void`; the write's result payload is **not**
  part of the contract, so don't rely on a returned value. For anything beyond
  this curated surface, **`api.client`** (the full generated `TaskService`
  client) remains the documented full-access escape hatch.
- **`api.ui.openTask(id)`** — open the host detail panel.
- **`api.dnd`** — drag bridge. Core task rows are drag sources; a panel that
  accepts them handles `onDragOver` (`preventDefault`) + `onDrop` and reads
  the dragged task's id with `api.dnd.readTaskId(e.dataTransfer)`. The
  calendar panel uses this to timebox a dropped task
  (`user_data.timebox = { start, end }`).
- **`api.format.tsDate/chipParts`** — the same helpers the core UI uses.

## Building a bundle

Bundle with esbuild, aliasing React to the host shims so hooks work across
the boundary (a second React copy would break them). The in-tree helper does
this for you:

```sh
node extensions/build-web.mjs extensions/<name>   # → extensions/<name>/web/main.js
```

Rules:

- Import **types** from `@taskd/extension-api` (erased at build).
- Import `react` / `react/jsx-runtime` normally — the aliases resolve them
  to the host's instance.
- Style with **inline styles** and the host's theme CSS variables
  (`--bg --surface --ink --muted --faint --line --accent --warn`) and fonts
  (`"IBM Plex Sans"`, `"IBM Plex Mono"`). Tailwind classes are **not** part
  of the contract — the core bundle only contains the classes it uses.

### Dev loop

There is no HMR — you rebuild and reload. Run the bundler in watch mode so it
re-emits `web/main.js` on every change:

```sh
node extensions/build-web.mjs extensions/<name> --watch
```

Rather than reinstalling into `~/.taskd/extensions/` after each edit, serve that
`web/main.js` from any static dev server and load it *on top of* the installed
extensions with the `?ext-dev=<url>` query param:

```
http://127.0.0.1:8888/?ext-dev=<url-to-your-dev-main.js>
```

The app loads every daemon-served bundle plus your dev URL, so your
in-progress extension's `register(api)` runs alongside the rest; reload the
page after each rebuild to pick up the change.

## Minimal example

```tsx
import type { TaskdExtension } from "@taskd/extension-api";

const ext: TaskdExtension = {
  name: "example",
  register(api) {
    api.registerPresenter({
      match: (t) => t.source === "example",
      rowMeta: (t) => ({ icon: "★", subtitle: String(t.externalData?.ref ?? "") }),
    });
  },
};
export default ext;
```

## Testing

Test your extension against a real `taskd` with the shared kit — Playwright
e2e (`defineExtensionE2E`) and vitest units (`mockApi` / `makeTask`). See
[`../testkit/README.md`](../testkit/README.md); in-tree examples live in
`extensions/{gcal,github}/e2e` and `.../web-src/*.test.ts`.
