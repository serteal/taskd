import { test, expect, daysFromNow } from "./fixtures";
import { row, rows, toast, seed, clickUndo } from "./helpers";

// Deeper undo coverage than web/e2e/undo.spec.ts's two golden-path tests
// (undo a single complete; undo a single delete). This file targets: undo
// restoring the ORIGINAL value (not just "cleared"), undo racing a
// concurrent edit, undo on a synced (source) task's delete, undo when
// multiple toasts are stacked, and bulk-undo restoring per-task state.
//
// See web/src/lib/actions.ts for the undo implementations under test.

const menu = (page: import("@playwright/test").Page) =>
  page.getByRole("menu", { name: "Task actions" });
const dueOf = (api: import("./fixtures").Api, title: string) =>
  api.listActive().then((ts) => ts.find((t) => t.title === title)?.dueTime);

test.describe("undo — single-task reschedule", () => {
  test("Undo restores the ORIGINAL due date, not null and not the new date", async ({
    page,
    api,
  }) => {
    const [seeded] = await seed(api, [{ title: "Resched orig", due: daysFromNow(5) }]);
    const originalDue = seeded.dueTime!;
    await expect(rows(page)).toHaveCount(1);

    await row(page, "Resched orig").click({ button: "right" });
    await menu(page).getByRole("menuitem", { name: "Schedule" }).click();
    await page.getByRole("button", { name: "Tomorrow" }).click();

    await expect(toast(page, "Scheduled")).toBeVisible();
    await expect.poll(() => dueOf(api, "Resched orig")).toBeTruthy();
    const rescheduled = await dueOf(api, "Resched orig");
    expect(new Date(rescheduled!).getTime()).not.toBe(new Date(originalDue).getTime());

    await clickUndo(page);

    // Restored to the ORIGINAL due date (rescheduleTask's `prev` capture),
    // not cleared and not left on the "Tomorrow" value.
    await expect
      .poll(() => dueOf(api, "Resched orig").then((d) => (d ? new Date(d).getTime() : null)))
      .toBe(new Date(originalDue).getTime());
  });
});

test.describe("undo — concurrent edit", () => {
  test("Undo after Complete is a narrow patch: a concurrent rename is not clobbered", async ({
    page,
    api,
  }) => {
    const [seeded] = await seed(api, [{ title: "Concurrent A" }]);
    await expect(rows(page)).toHaveCount(1);

    await row(page, "Concurrent A").getByRole("button", { name: "Complete Concurrent A" }).click();
    await expect(rows(page)).toHaveCount(0);
    await expect(toast(page, "Completed")).toBeVisible();

    // Once completed (even optimistically), TaskStore.place() deletes the
    // task from the live replica entirely (store.ts: "Completed tasks leave
    // the replica"). So there is no row, no DetailPanel, and no inline-edit
    // affordance left in THIS window to rename it through — the task
    // genuinely doesn't exist in the client model while completed. The only
    // way a "concurrent edit while completed, before Undo" can happen is
    // from elsewhere (another tab/device, or an extension) — simulated here
    // via a direct API call, the same technique web/e2e/live-sync.spec.ts
    // uses for "external rename reflects in the open app".
    await api.updateTask(seeded.id, ["title"], { title: "Renamed while completed" });

    await clickUndo(page);

    // completeTask's undo callback is `store.update(id, { completed: false })`
    // — a single-field patch, not a full snapshot restore. It must reopen
    // the task WITHOUT reverting the concurrent rename.
    await expect(row(page, "Renamed while completed")).toBeVisible();
    await expect(row(page, "Concurrent A")).toHaveCount(0);
    const after = (await api.listActive()).find((t) => t.id === seeded.id);
    expect(after?.title).toBe("Renamed while completed");
    expect(after?.completedTime).toBeFalsy();
  });
});

test.describe("undo — delete on a synced task", () => {
  test.use({ mode: "extensions" });

  test("offers no Undo at all — recreating it could only ever produce a duplicate local task", async ({
    page,
    api,
  }) => {
    const active = await api.listActive();
    const gh = active.find((t) => t.source === "github");
    expect(gh, "expected a github-sourced seed task").toBeTruthy();

    await page.locator('[data-testid="side-item"][data-label="github"]').click();
    const syncedRow = rows(page).filter({ hasText: gh!.title }).first();
    await expect(syncedRow).toBeVisible();

    await syncedRow.click({ button: "right" });
    await expect(menu(page)).toBeVisible();
    await menu(page).getByRole("menuitem", { name: "Delete" }).click();

    // The toast shows, but with no Undo action: store.create() (the only
    // thing an undo could call) has no `source` field at all — see
    // CreateTaskRequest (proto/task/task.proto) — so it could only ever
    // recreate a brand-new LOCAL task under a new id, immediately visible in
    // "All tasks"/Inbox (a surface the original never appeared in), and a
    // guaranteed duplicate once the source resyncs the same item. Rather than
    // offer an undo that can't be honored, deleteTaskWithUndo omits the
    // action entirely for a synced task.
    const deletedToast = toast(page, "Deleted");
    await expect(deletedToast).toBeVisible();
    await expect(deletedToast.getByRole("button", { name: "Undo" })).toHaveCount(0);

    const afterDelete = await api.listActive();
    expect(afterDelete.find((t) => t.id === gh!.id)).toBeUndefined();
  });

  test("bulk delete: undo recreates only the local tasks in the selection, not the synced one", async ({
    page,
    api,
  }) => {
    await seed(api, [{ title: "Bulk local task" }]);
    const gh = (await api.listActive()).find((t) => t.source === "github")!;

    // A filter with no source constraint promotes local and synced tasks
    // onto one surface, where both can be multi-selected together.
    await page.getByTestId("new-filter").click();
    const fb = page.getByTestId("filter-builder");
    await fb.getByRole("textbox", { name: "Filter name" }).fill("Everything");
    await fb.getByRole("button", { name: "Save filter" }).click();
    await expect(page.getByRole("heading", { name: "Everything" })).toBeVisible();

    await page.getByText("Bulk local task", { exact: true }).click({ modifiers: ["ControlOrMeta"] });
    await rows(page)
      .filter({ hasText: gh.title })
      .first()
      .click({ modifiers: ["ControlOrMeta"] });
    await expect(page.getByTestId("bulk-bar")).toContainText("2 selected");

    await page.getByTestId("bulk-bar").getByRole("button", { name: "Delete" }).click();
    await clickUndo(page);

    const afterUndo = await api.listActive();
    expect(afterUndo.some((t) => t.title === "Bulk local task")).toBe(true);
    // The synced task isn't recreated as a local duplicate — undo skips it.
    expect(afterUndo.some((t) => t.id === gh.id)).toBe(false);
    expect(afterUndo.filter((t) => t.title === gh.title)).toHaveLength(0);
  });
});

test.describe("undo — multiple stacked toasts", () => {
  test("clicking a specific toast's Undo affects only that task, not the other completed one", async ({
    page,
    api,
  }) => {
    await seed(api, [{ title: "Stack A" }, { title: "Stack B" }]);
    await expect(rows(page)).toHaveCount(2);

    await row(page, "Stack A").getByRole("button", { name: "Complete Stack A" }).click();
    await expect(rows(page)).toHaveCount(1);
    await expect(toast(page, "Stack A")).toBeVisible();

    await row(page, "Stack B").getByRole("button", { name: "Complete Stack B" }).click();
    await expect(rows(page)).toHaveCount(0);
    await expect(toast(page, "Stack B")).toBeVisible();

    // Both toasts are now stacked (notify.ts keeps them under ?test=1 —
    // NO_AUTODISMISS). NOTE for anyone reusing the shared `clickUndo` helper:
    // ToastStack.tsx renders `useToasts()` in array order, and notify.ts's
    // `toast()` appends new toasts to the END of that array — so the OLDEST
    // toast is the first DOM match, not the newest. `clickUndo` (which
    // clicks `.first()`) would therefore hit Stack A's Undo here, not the
    // most-recently-shown Stack B — a real footgun if a future test calls
    // the generic helper with more than one toast on screen. To be precise,
    // scope by each toast's own text instead of relying on click order.
    await toast(page, "Stack B").getByRole("button", { name: "Undo" }).click();

    await expect(row(page, "Stack B")).toBeVisible();
    await expect(row(page, "Stack A")).toHaveCount(0); // still completed — untouched
    expect((await api.listActive()).map((t) => t.title)).toEqual(["Stack B"]);
  });
});

test.describe("undo — global ⌘Z", () => {
  test("⌘Z restores a task completed via the row checkbox", async ({ page, api }) => {
    await seed(api, [{ title: "Zap me" }]);
    await expect(rows(page)).toHaveCount(1);

    await row(page, "Zap me").getByRole("button", { name: "Complete Zap me" }).click();
    await expect(rows(page)).toHaveCount(0);
    await expect(toast(page, "Completed")).toBeVisible();

    // Focus is on the list surface (no editable focused), so ⌘Z reaches the
    // global undo stack — the same entry the toast's Undo button would consume.
    await page.keyboard.press("ControlOrMeta+z");

    await expect(row(page, "Zap me")).toBeVisible();
    await expect(toast(page, "Undid")).toBeVisible();
    expect((await api.listActive()).map((t) => t.title)).toContain("Zap me");
  });

  test("⌘Z after a detail-panel title edit restores the old title", async ({ page, api }) => {
    await seed(api, [{ title: "Before edit" }]);
    await page.getByText("Before edit", { exact: true }).click();

    const title = page.getByRole("textbox", { name: "Title" });
    await title.fill("After edit");
    await title.press("Enter"); // Enter blurs → commits + pushes an undo entry
    await expect.poll(() => api.listActive().then((ts) => ts[0]?.title)).toBe("After edit");

    // Close the panel so focus is on the app surface, not the (now blurred)
    // input — ⌘Z must be the GLOBAL undo here, not a native in-field undo.
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("detail-panel")).toBeHidden();
    await page.keyboard.press("ControlOrMeta+z");

    await expect.poll(() => api.listActive().then((ts) => ts[0]?.title)).toBe("Before edit");
    await expect(toast(page, "Undid")).toBeVisible();
  });

  test("⌘Z with nothing on the stack shows a subtle 'Nothing to undo'", async ({ page }) => {
    await page.keyboard.press("ControlOrMeta+z");
    await expect(toast(page, "Nothing to undo")).toBeVisible();
  });
});

test.describe("undo — delete restores full fidelity", () => {
  test("deleting a parent with a nested child, then Undo, re-nests the child under the restored parent", async ({
    page,
    api,
  }) => {
    const [parent] = await seed(api, [{ title: "Undo Parent" }]);
    await api.createTask({ title: "Undo Child", parentId: parent.id });
    await expect(rows(page)).toHaveCount(2);
    await expect(rows(page).nth(1)).toHaveAttribute("data-depth", "1");

    // Delete the parent. The server re-parents the child to top-level (it
    // survives), so the list drops to just the (now depth-0) child.
    await row(page, "Undo Parent").click({ button: "right" });
    await menu(page).getByRole("menuitem", { name: "Delete" }).click();
    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "Undo Child")).toHaveAttribute("data-depth", "0");

    // Undo rebuilds the parent (a new id) AND re-nests the surviving child
    // under it. The pre-fix undo recreated only title/notes/labels/due, so the
    // child stayed orphaned and the parent's count chip never came back.
    await clickUndo(page);
    await expect(rows(page)).toHaveCount(2);
    await expect(row(page, "Undo Parent").getByTestId("row-subtasks")).toContainText("1 subtask");
    await expect(row(page, "Undo Child")).toHaveAttribute("data-depth", "1");

    // Server truth: the child points at the rebuilt parent again.
    const all = await api.listActive();
    const rebuiltParent = all.find((t) => t.title === "Undo Parent")!;
    const child = all.find((t) => t.title === "Undo Child")!;
    expect(child.parentId).toBe(rebuiltParent.id);
  });

  test("deleting a recurring task, then Undo, restores the recurrence rule (not a one-off)", async ({
    page,
    api,
  }) => {
    await seed(api, [{ title: "Undo Recur", due: daysFromNow(1), recurrence: "FREQ=DAILY;INTERVAL=3" }]);
    await expect(row(page, "Undo Recur").getByTestId("row-recur")).toBeVisible();

    await row(page, "Undo Recur").click({ button: "right" });
    await menu(page).getByRole("menuitem", { name: "Delete" }).click();
    await expect(rows(page)).toHaveCount(0);

    // The rebuilt task carries the rule forward — before the fix it was
    // silently downgraded to a one-off (no glyph, empty recurrence).
    await clickUndo(page);
    await expect(row(page, "Undo Recur").getByTestId("row-recur")).toBeVisible();
    await expect
      .poll(async () => (await api.listActive()).find((t) => t.title === "Undo Recur")?.recurrence)
      .toBe("FREQ=DAILY;INTERVAL=3");
  });
});

test.describe("undo — bulk reschedule", () => {
  test("restores each task's OWN original due date individually, not a shared or cleared one", async ({
    page,
    api,
  }) => {
    const [a, b] = await seed(api, [
      { title: "Bulk resched A", due: daysFromNow(2) },
      { title: "Bulk resched B", due: daysFromNow(9) },
    ]);
    await expect(rows(page)).toHaveCount(2);

    await page.getByText("Bulk resched A", { exact: true }).click({ modifiers: ["ControlOrMeta"] });
    await page.getByText("Bulk resched B", { exact: true }).click({ modifiers: ["ControlOrMeta"] });

    await page.getByTestId("bulk-bar").getByRole("button", { name: "Schedule" }).click();
    await page.getByRole("button", { name: "Tomorrow" }).click();

    await expect(toast(page, "Scheduled 2 tasks")).toBeVisible();
    await expect.poll(() => dueOf(api, "Bulk resched A")).toBeTruthy();
    await expect.poll(() => dueOf(api, "Bulk resched B")).toBeTruthy();
    // Sanity: both landed on the SAME new ("Tomorrow") date before undo.
    const newA = await dueOf(api, "Bulk resched A");
    const newB = await dueOf(api, "Bulk resched B");
    expect(new Date(newA!).getTime()).toBe(new Date(newB!).getTime());

    await clickUndo(page);

    await expect
      .poll(() => dueOf(api, "Bulk resched A").then((d) => (d ? new Date(d).getTime() : null)))
      .toBe(new Date(a.dueTime!).getTime());
    await expect
      .poll(() => dueOf(api, "Bulk resched B").then((d) => (d ? new Date(d).getTime() : null)))
      .toBe(new Date(b.dueTime!).getTime());
    // The two original dates were different — proves it's per-task restore,
    // not both swapped onto each other or both cleared to the same thing.
    expect(new Date(a.dueTime!).getTime()).not.toBe(new Date(b.dueTime!).getTime());
  });
});
