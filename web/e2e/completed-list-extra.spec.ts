import { test, expect } from "./fixtures";
import type { Api, Task } from "../testkit/daemon";

type Page = import("@playwright/test").Page;

// CompletedList (web/src/components/CompletedList.tsx) is server-paged and NOT
// part of the live replica: it fetches once via `store.listCompleted()` on
// mount (pageSize hardcoded to 50, orderBy "updated desc" — see
// web/src/lib/store.ts `listCompleted`), and never refetches on its own.
//
// This file covers what web/e2e/completed.spec.ts and the single-task reopen
// case in coverage-extra.spec.ts don't: pagination, label overflow, result
// ordering, multi-task reopen bookkeeping, and the "not live" contract.

const gotoCompleted = (page: Page) =>
  page.locator('[data-testid="side-item"][data-label="Completed"]').click();
const gotoAllTasks = (page: Page) =>
  page.locator('[data-testid="side-item"][data-label="All tasks"]').click();

// Completed rows carry an explicit testid — counting by Tailwind's "group"
// class broke the moment an unrelated component (the resize handles) also
// used it.
const completedRows = (page: Page) => page.getByTestId("completed-row");

const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Mark a task completed directly via the API (bypasses the UI entirely).
 *  The raw HTTP testkit client sends the mask path string as-is (it doesn't
 *  go through protobuf's FieldMask JSON codec, which would camelCase it), so
 *  this must be "completedTime" — unlike the app's own store.ts, which builds
 *  a real FieldMask message and can use the proto name "completed_time". */
async function completeApi(api: Api, id: string, at: Date = new Date()): Promise<void> {
  await api.updateTask(id, ["completedTime"], { completedTime: at.toISOString() });
}

/** Create + immediately complete `count` tasks, sequentially (so completion
 *  order — and therefore `updated_ms` order — is deterministic). Titles are
 *  zero-padded to a fixed width so `hasText`/substring matches never collide
 *  (e.g. "Coh 05" is not a substring of "Coh 15"). */
async function seedCompleted(api: Api, count: number, prefix: string): Promise<Task[]> {
  const width = String(count).length;
  const out: Task[] = [];
  for (let i = 1; i <= count; i++) {
    const t = await api.createTask({ title: `${prefix} ${String(i).padStart(width, "0")}` });
    await completeApi(api, t.id);
    out.push(t);
  }
  return out;
}

test.describe("Completed view — pagination", () => {
  test("Load more appends page two without dropping or duplicating page one", async ({ page, api }) => {
    test.setTimeout(60_000); // 55 sequential create+complete round trips
    const n = 55; // server pageSize is hardcoded to 50 in store.ts listCompleted()
    await seedCompleted(api, n, "Page");

    await gotoCompleted(page);
    await expect(completedRows(page)).toHaveCount(50);
    await expect(page.getByRole("button", { name: "Load more" })).toBeVisible();

    // Most-recently-completed-first ("updated desc"): task 55 was completed
    // last, so it's on page one; task 01 was completed first, so it's on
    // whatever the final page is.
    await expect(page.getByText("Page 55", { exact: true })).toBeVisible();
    await expect(page.getByText("Page 01", { exact: true })).toHaveCount(0);

    await page.getByRole("button", { name: "Load more" }).click();

    await expect(completedRows(page)).toHaveCount(n); // page one rows preserved + page two appended
    await expect(page.getByText("Page 55", { exact: true })).toBeVisible(); // still there
    await expect(page.getByText("Page 01", { exact: true })).toBeVisible(); // now loaded

    // No duplicates: every row's text is distinct.
    const texts = await completedRows(page).allTextContents();
    expect(new Set(texts).size).toBe(n);

    // Exhausted: the button is gone for good (55 - 50 = 5 <= 50, so page two
    // has no next token).
    await expect(page.getByRole("button", { name: "Load more" })).toHaveCount(0);
  });
});

test.describe("Completed view — label chips", () => {
  test("labels beyond the first 3 are hidden, with a '+N' overflow indicator", async ({
    page,
    api,
  }) => {
    // CompletedList.tsx renders `t.labels.slice(0, 3)` plus a "+N" indicator
    // for the rest. Labels come back from the server alphabetically sorted
    // (internal/store/list.go labelsForTasks: "ORDER BY label"), so the first
    // 3 alphabetically are the ones shown.
    const t = await api.createTask({
      title: "Many Labels",
      labels: ["echo", "delta", "charlie", "bravo", "alpha"], // -> alpha,bravo,charlie,delta,echo
    });
    await completeApi(api, t.id);

    await gotoCompleted(page);
    const rowLoc = completedRows(page).filter({ hasText: "Many Labels" });
    await expect(rowLoc).toBeVisible();

    // Shown: the first 3 alphabetically.
    await expect(rowLoc.getByText("alpha", { exact: true }).first()).toBeVisible();
    await expect(rowLoc.getByText("bravo", { exact: true }).first()).toBeVisible();
    await expect(rowLoc.getByText("charlie", { exact: true }).first()).toBeVisible();

    // Not individually shown, but the "+2" indicator says two more exist.
    await expect(rowLoc.getByText("delta", { exact: true })).toHaveCount(0);
    await expect(rowLoc.getByText("echo", { exact: true })).toHaveCount(0);
    await expect(rowLoc.getByText("+2", { exact: true })).toBeVisible();
  });
});

test.describe("Completed view — ordering", () => {
  test("lists completions most-recently-completed first", async ({ page, api }) => {
    const a = await api.createTask({ title: "Order Alpha" });
    const b = await api.createTask({ title: "Order Bravo" });
    const c = await api.createTask({ title: "Order Charlie" });
    await completeApi(api, a.id);
    await wait(15);
    await completeApi(api, b.id);
    await wait(15);
    await completeApi(api, c.id);

    await gotoCompleted(page);
    const rowLoc = completedRows(page);
    await expect(rowLoc).toHaveCount(3);
    const texts = await rowLoc.allTextContents();
    expect(texts[0]).toContain("Order Charlie"); // completed last -> first
    expect(texts[1]).toContain("Order Bravo");
    expect(texts[2]).toContain("Order Alpha"); // completed first -> last
  });

  test("editing a completed task's title does not resurface it above a more-recently-completed one", async ({
    page,
    api,
  }) => {
    // The archive orders by completed time (store.ts listCompleted: orderBy
    // "completed desc"), not by last-updated — so a later edit to an
    // already-completed task (title/notes/labels/due) doesn't reshuffle the
    // archive; only completing something new does.
    const a = await api.createTask({ title: "Stale Alpha" });
    const b = await api.createTask({ title: "Fresh Bravo" });
    await completeApi(api, a.id);
    await wait(15);
    await completeApi(api, b.id);

    await gotoCompleted(page);
    await expect(completedRows(page)).toHaveCount(2); // wait for the async load, not a bare read
    let texts = await completedRows(page).allTextContents();
    expect(texts[0]).toContain("Fresh Bravo"); // completed more recently
    expect(texts[1]).toContain("Stale Alpha");

    // Edit Alpha's title only — completed_time is untouched (server.go
    // UpdateTask only overwrites mask paths present in the request).
    await wait(15);
    await api.updateTask(a.id, ["title"], { title: "Stale Alpha (edited)" });

    // The view isn't live (see the "not live" test below) — force a remount
    // to pick up the change.
    await gotoAllTasks(page);
    await gotoCompleted(page);

    // Same here: the remount kicks off a fresh async fetch, so wait for it
    // to actually land before reading text contents.
    await expect(completedRows(page).first()).toContainText("Fresh Bravo");
    texts = await completedRows(page).allTextContents();
    expect(texts[0]).toContain("Fresh Bravo"); // still first — completed later
    expect(texts[1]).toContain("Stale Alpha (edited)"); // the edit didn't move it
  });
});

test.describe("Completed view — reopen with multiple tasks", () => {
  test("reopening one of several completed tasks removes only that one and returns it to the active set", async ({
    page,
    api,
  }) => {
    const a = await api.createTask({ title: "Keep A" });
    const b = await api.createTask({ title: "Reopen Me" });
    const c = await api.createTask({ title: "Keep C" });
    for (const t of [a, b, c]) await completeApi(api, t.id);

    await gotoCompleted(page);
    await expect(completedRows(page)).toHaveCount(3);

    const target = completedRows(page).filter({ hasText: "Reopen Me" });
    await target.hover();
    await target.getByRole("button", { name: "Reopen" }).click();

    await expect(completedRows(page)).toHaveCount(2);
    await expect(page.getByText("Reopen Me", { exact: true })).toHaveCount(0);
    await expect(page.getByText("Keep A", { exact: true })).toBeVisible();
    await expect(page.getByText("Keep C", { exact: true })).toBeVisible();

    expect((await api.listActive()).map((t) => t.title)).toContain("Reopen Me");
  });

  test("reopening a task on page one leaves the Load-more cursor coherent for page two", async ({
    page,
    api,
  }) => {
    test.setTimeout(60_000);
    const n = 52; // 50 on page one, 2 on page two
    await seedCompleted(api, n, "Coh");

    await gotoCompleted(page);
    await expect(completedRows(page)).toHaveCount(50);
    await expect(page.getByRole("button", { name: "Load more" })).toBeVisible();

    // "Coh 52" was completed last, so it's the top row on page one.
    const topTitle = "Coh 52";
    const topRow = completedRows(page).filter({ hasText: topTitle });
    await topRow.hover();
    await topRow.getByRole("button", { name: "Reopen" }).click();
    await expect(completedRows(page)).toHaveCount(49);
    await expect(page.getByText(topTitle, { exact: true })).toHaveCount(0);

    // The pagination cursor (`next`) is independent client state, untouched
    // by the local filter() the reopen does — Load more should still work.
    await page.getByRole("button", { name: "Load more" }).click();
    await expect(completedRows(page)).toHaveCount(n - 1); // 52 - 1 reopened, no dupes, no errors
    await expect(page.getByRole("button", { name: "Load more" })).toHaveCount(0);

    const texts = await completedRows(page).allTextContents();
    expect(new Set(texts).size).toBe(n - 1); // still no duplicate rows after the append
    await expect(page.getByText("Coh 01", { exact: true })).toBeVisible(); // page two loaded correctly
  });
});

test.describe("Completed view — synced origin chip", () => {
  test("a closed synced item shows its source as a chip; a local one does not", async ({ page, api }) => {
    // A completed synced item, seeded directly as an external task. No syncer
    // is needed in the default (clean) daemon — UpsertExternal writes the row
    // (with completed_ms set) straight into the store, and it lands in the
    // archive like any other completion, keeping its source.
    await api.upsertExternal("github", [
      {
        externalRef: "issue-42",
        title: "Closed synced issue",
        completedTime: new Date().toISOString(),
      },
    ]);
    // A plain local completion, for contrast — no source, so no chip.
    const local = await api.createTask({ title: "Closed local task" });
    await completeApi(api, local.id);

    await gotoCompleted(page);
    await expect(completedRows(page)).toHaveCount(2);

    const syncedRow = completedRows(page).filter({ hasText: "Closed synced issue" });
    await expect(syncedRow.getByTestId("source-chip")).toHaveText("github");

    const localRow = completedRows(page).filter({ hasText: "Closed local task" });
    await expect(localRow).toBeVisible();
    await expect(localRow.getByTestId("source-chip")).toHaveCount(0);
  });
});

test.describe("Completed view — not live", () => {
  test("the view is fetched once on mount and does not live-update when a task is completed elsewhere", async ({
    page,
    api,
  }) => {
    const before = await api.createTask({ title: "Seen Before Mount" });
    await completeApi(api, before.id);

    await gotoCompleted(page); // CompletedList mounts and fetches once
    await expect(completedRows(page)).toHaveCount(1);
    await expect(page.getByText("Seen Before Mount", { exact: true })).toBeVisible();

    // Complete a second task via a direct API call, entirely bypassing the
    // already-open UI (no watch subscription backs this view).
    const after = await api.createTask({ title: "Completed After Mount" });
    await completeApi(api, after.id);

    // Give any (hypothetical) live-update mechanism a moment, then confirm it
    // genuinely never arrives.
    await page.waitForTimeout(500);
    await expect(completedRows(page)).toHaveCount(1);
    await expect(page.getByText("Completed After Mount", { exact: true })).toHaveCount(0);

    // Confirm the task really did complete server-side, and is only missing
    // here because this screen isn't live — a fresh mount picks it up.
    await gotoAllTasks(page);
    await gotoCompleted(page);
    await expect(completedRows(page)).toHaveCount(2);
    await expect(page.getByText("Completed After Mount", { exact: true })).toBeVisible();
  });
});
