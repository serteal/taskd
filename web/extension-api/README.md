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
- **`api.hooks.useTasks()` / `useNow()`** — the live task replica and a slow
  clock, as React hooks, for use in your components.
- **`api.store.create/update/delete`** — optimistic mutations. To write your
  own structured data, use `user_data` (user-owned, never touched by sync):
  `api.store.update(id, { userData: { ...task.userData, mine: {...} } })`.
- **`api.ui.openTask(id)`** — open the host detail panel.
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
