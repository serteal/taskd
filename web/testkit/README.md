# @taskd/testkit — shared test utilities

Reusable Playwright + vitest helpers for testing the taskd web app **and any
extension**, against a real `taskd`. Core's own suite (`web/e2e`) is built on
these exact primitives, so an extension gets the same fidelity.

## e2e (Playwright)

An extension writes specs in its own `e2e/` folder and drives the app with its
extension staged into a fresh, throwaway daemon:

```ts
// extensions/myext/e2e/myext.spec.ts
import { defineExtensionE2E, waitForSources, rows } from "../../../web/testkit/e2e";

// Stage "myext"; wait for its syncer's first data (omit waitFor if it has none).
const { test, expect } = defineExtensionE2E("myext", { waitFor: waitForSources("myext") });

test("my presenter renders its rows", async ({ page, api }) => {
  await api.upsertExternal("myext", [
    { externalRef: "x1", title: "Hello", externalData: { foo: "bar" } },
  ]);
  await expect(rows(page).filter({ hasText: "Hello" })).toBeVisible();
});
```

Each test gets: a `taskd` with your extension staged on an ephemeral port + temp
dir, a frozen clock (`FIXED_NOW`), an `api` for seeding (`createTask`,
`upsertExternal`, …), the shared helpers (`row`/`rows`, `dragTo`, `openPalette`,
`runCommand`, `toast`, `seed`, …), and a **zero-console-error** assertion on
teardown. Opt out of the last with `test.use({ expectNoConsoleErrors: false })`
for deliberate error paths.

`defineExtensionE2E` accepts a name (resolved to `extensions/<name>`), an
explicit `{ name, dir }`, or an array to stage several at once.

### ESM note

Playwright decides a spec's module format from the nearest `package.json`. The
kit is ESM, so every `e2e/` folder that imports it must be ESM too. In-tree, a
single `extensions/package.json` (`{"type":"module"}`) and `web/e2e/package.json`
cover this; a standalone extension repo just needs its own `{"type":"module"}`.

## Unit (vitest)

Unit-test the non-rendering parts of `register(api)` — presenter `match` /
`rowMeta`, command `run`, quick-add token `match` — against a spy `api`:

```ts
// extensions/myext/web-src/myext.test.ts
import { describe, it, expect } from "vitest";
import { mockApi, makeTask } from "../../../web/testkit/unit";
import extension from "./main";

it("registers a presenter that matches its source", () => {
  const api = mockApi();
  extension.register(api);
  const p = api.registered.presenters[0];
  expect(p.match(makeTask({ source: "myext" }))).toBe(true);
  expect(p.rowMeta(makeTask({ source: "myext", externalData: { n: 1 } })).subtitle).toBe("…");
});
```

`mockApi()` records every `register*` call under `api.registered` and provides
spies for `notify`/`store`/`ui`/`dnd` (each with a `.calls` array) plus an inert
`icon(name) → { __icon: name }` so no React renderer is needed. `makeTask(partial)`
builds a valid-enough `Task`. `mockApi({ tasks, now })` controls what `getTasks`
/ hooks return.

vitest picks these up via `web/vite.config.ts` (`test.include` already globs
`../extensions/**/web-src/**/*.test.ts`).

## Running

From `web/`: `make test-web-e2e` (e2e, all extensions) and `make test-web`
(unit). Both discover extension tests automatically.
```
