import { test, expect, daysFromNow } from "./fixtures";
import { row, rows, seed, dialog, toast, clickUndo } from "./helpers";

// Recurring tasks + subtasks (the depth-1 hierarchy), end to end against a
// real daemon. Server semantics under test: completing a recurring task does
// NOT complete it — the server archives a frozen copy and advances the live
// task's due in one transaction (UpdateTaskResponse.spawned_occurrence);
// recurrence is rejected on synced tasks (so the editor is never offered
// there); deleting a parent re-parents its children to top-level.

const panel = (page: import("@playwright/test").Page) => page.getByTestId("detail-panel");
const goTo = (page: import("@playwright/test").Page, label: string) =>
  page.locator(`[data-testid="side-item"][data-label="${label}"]`).click();

test.describe("recurring tasks", () => {
  test("quick-add 'every 3 days' shows the chip and creates a row with the repeat glyph", async ({
    page,
    api,
  }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("Water plants every 3 days tomorrow");

    // The phrase is recognized live: a 'recur' token highlight + the chip
    // (humanized, on the same pill row as the due chip).
    await expect(d.locator('[data-testid="quickadd-token"][data-kind="recur"]')).toHaveText(
      "every 3 days",
    );
    await expect(d.getByTestId("recurrence-chip")).toContainText("every 3 days");
    await expect(d.getByRole("button", { name: /tomorrow/i })).toBeVisible();

    await name.press("Enter");
    await expect(d).toBeHidden();

    // The title lost both phrases; the row carries the repeat glyph with the
    // humanized rule as its tooltip.
    const created = row(page, "Water plants");
    await expect(created).toBeVisible();
    await expect(created.getByTestId("row-recur")).toBeVisible();
    await expect(created.getByTestId("row-recur")).toHaveAttribute("title", "every 3 days");

    // And the canonical rule + the due both persisted.
    const t = (await api.listActive()).find((x) => x.title === "Water plants")!;
    expect(t.recurrence).toBe("FREQ=DAILY;INTERVAL=3");
    expect(t.dueTime).toBeTruthy();
  });

  test("the recurrence chip's × clears the typed rule before create", async ({ page, api }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("One-off after all daily");
    await expect(d.getByTestId("recurrence-chip")).toContainText("every day");

    await d.getByTestId("recurrence-chip").getByRole("button", { name: "Clear", exact: true }).click();
    await expect(d.getByTestId("recurrence-chip")).toHaveCount(0);
    await name.press("Enter");

    const t = (await api.listActive()).find((x) => x.title === "One-off after all")!;
    expect(t.recurrence ?? "").toBe("");
  });

  test("completing keeps the row (due advanced), archives a frozen copy, and undo reverses both", async ({
    page,
    api,
  }) => {
    const [seeded] = await seed(api, [
      { title: "Recur A", due: daysFromNow(1), recurrence: "FREQ=DAILY;INTERVAL=3" },
    ]);
    await expect(rows(page)).toHaveCount(1);

    await row(page, "Recur A").getByRole("button", { name: "Complete Recur A" }).click();

    // A recurring completion updates the row IN PLACE (the task advances, it
    // doesn't leave), so the leaving strike-through must never flash on.
    // Sampled immediately and without retry: the old bug showed the strike for
    // the first 250ms after the click.
    expect(await row(page, "Recur A").locator("span.line-through").count()).toBe(0);

    // The toast names the next occurrence rather than plain "Completed …".
    await expect(toast(page, "Completed — next")).toBeVisible();
    // The row REMAINS — the live task rolled forward instead of completing.
    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "Recur A")).toBeVisible();

    // Server truth: due advanced by the rule's 3 days, task still active…
    const after = (await api.listActive()).find((x) => x.title === "Recur A")!;
    expect(new Date(after.dueTime!).getTime()).toBe(
      new Date(seeded.dueTime!).getTime() + 3 * 86_400_000,
    );
    // …and the archive holds the frozen (completed, non-recurring) copy.
    const archived = (await api.listAll()).filter(
      (x) => x.title === "Recur A" && x.completedTime,
    );
    expect(archived).toHaveLength(1);
    expect(archived[0].recurrence ?? "").toBe("");

    // The Completed view surfaces it too.
    await goTo(page, "Completed");
    await expect(page.getByText("Recur A", { exact: true })).toBeVisible();
    await goTo(page, "All tasks");

    // Undo: the previous due comes back and the archive copy is deleted.
    await clickUndo(page);
    await expect
      .poll(async () => {
        const t = (await api.listActive()).find((x) => x.title === "Recur A");
        return t?.dueTime ? new Date(t.dueTime).getTime() : null;
      })
      .toBe(new Date(seeded.dueTime!).getTime());
    await expect
      .poll(async () => (await api.listAll()).filter((x) => x.completedTime).length)
      .toBe(0);
  });

  test("the detail editor sets a preset, a custom rule, rejects garbage inline, and clears", async ({
    page,
    api,
  }) => {
    const [seeded] = await seed(api, [{ title: "Editable" }]);
    await page.getByText("Editable", { exact: true }).click();
    const p = panel(page);
    await expect(p).toBeVisible();

    // Preset write.
    await p.getByLabel("Recurrence", { exact: true }).selectOption({ label: "Every week" });
    await expect.poll(async () => (await api.getTask(seeded.id)).recurrence).toBe("FREQ=WEEKLY");
    await expect(p.getByTestId("recurrence-current")).toContainText("every week");
    await expect(row(page, "Editable").getByTestId("row-recur")).toBeVisible();

    // Custom natural language.
    await p.getByLabel("Recurrence", { exact: true }).selectOption({ label: "Custom…" });
    const custom = p.getByLabel("Custom recurrence");
    await custom.fill("every mon,fri");
    await custom.press("Enter");
    await expect
      .poll(async () => (await api.getTask(seeded.id)).recurrence)
      .toBe("FREQ=WEEKLY;BYDAY=MO,FR");
    await expect(p.getByTestId("recurrence-current")).toContainText("every Mon, Fri");

    // Invalid input: inline hint, no write.
    await p.getByLabel("Recurrence", { exact: true }).selectOption({ label: "Custom…" });
    await custom.fill("every blue moon");
    await custom.press("Enter");
    await expect(p.getByTestId("recurrence-error")).toBeVisible();
    expect((await api.getTask(seeded.id)).recurrence).toBe("FREQ=WEEKLY;BYDAY=MO,FR");

    // Clear back to a one-off.
    await p.getByLabel("Recurrence", { exact: true }).selectOption({ label: "None" });
    await expect.poll(async () => (await api.getTask(seeded.id)).recurrence ?? "").toBe("");
    await expect(row(page, "Editable").getByTestId("row-recur")).toHaveCount(0);
  });
});

test.describe("recurrence on synced tasks", () => {
  test.use({ mode: "extensions" });

  test("a synced task's detail offers no recurrence editor (but does offer subtasks)", async ({
    page,
    api,
  }) => {
    await goTo(page, "github");
    const gh = (await api.listActive()).find((t) => t.source === "github");
    expect(gh, "expected a github-sourced task").toBeTruthy();
    await rows(page).filter({ hasText: gh!.title }).first().click();

    await expect(panel(page)).toBeVisible();
    await expect(panel(page).getByTestId("recurrence-editor")).toHaveCount(0);
    // A synced task can still be a PARENT — a local checklist under a PR.
    await expect(panel(page).getByTestId("subtasks-section")).toBeVisible();
  });
});

test.describe("subtasks", () => {
  test("adding via the detail panel lists it and puts the count chip on the parent row", async ({
    page,
    api,
  }) => {
    await seed(api, [{ title: "Parent P" }]);
    await page.getByText("Parent P", { exact: true }).click();
    const p = panel(page);

    const add = p.getByLabel("Add subtask");
    await add.fill("Child C");
    await add.press("Enter");

    await expect(p.getByTestId("subtask-row")).toHaveCount(1);
    await expect(p.getByTestId("subtask-row")).toContainText("Child C");

    await page.keyboard.press("Escape");
    await expect(row(page, "Parent P").getByTestId("row-subtasks")).toContainText("1 subtask");

    // The created child inherits nothing but the parent link.
    const child = (await api.listActive()).find((t) => t.title === "Child C")!;
    expect(child.parentId).toBeTruthy();
    expect(child.labels ?? []).toEqual([]);
    expect(child.dueTime).toBeFalsy();
  });

  test("All view nests the child indented under its parent; collapse hides it from view AND j/k", async ({
    page,
    api,
  }) => {
    const [parent] = await seed(api, [{ title: "Parent P" }]);
    await api.createTask({ title: "Child C", parentId: parent.id });
    await expect(rows(page)).toHaveCount(2);

    // Visual order: parent first, child directly after at depth 1.
    await expect(rows(page).nth(0)).toContainText("Parent P");
    await expect(rows(page).nth(1)).toContainText("Child C");
    await expect(rows(page).nth(1)).toHaveAttribute("data-depth", "1");

    // Keyboard order matches: j, j lands on the child.
    await page.keyboard.press("j");
    await page.keyboard.press("j");
    await page.keyboard.press("Enter");
    await expect(panel(page).getByRole("textbox", { name: "Title" })).toHaveValue("Child C");
    await page.keyboard.press("Escape");

    // Collapse via the parent's subtask chip: the child leaves the list…
    await row(page, "Parent P").getByRole("button", { name: "Collapse subtasks" }).click();
    await expect(rows(page)).toHaveCount(1);

    // …but the counts don't move: the header keeps the same single source of
    // truth as the sidebar badge (countView over the full snapshot), so
    // collapse — pure presentation — can never make the two disagree.
    await expect(page.getByTestId("view-count")).toHaveText("2");
    await expect(
      page.locator('[data-testid="side-item"][data-label="All tasks"]').locator("span.font-mono"),
    ).toHaveText("2");

    // Drop focus from the chip (click an inert spot) so the Enter below goes
    // to the global keyboard handler, not the still-focused toggle button.
    await page.getByRole("heading", { name: "All tasks" }).click();

    // …and the keyboard order: j from the parent has nowhere further to go.
    await page.keyboard.press("j");
    await page.keyboard.press("Enter");
    await expect(panel(page).getByRole("textbox", { name: "Title" })).toHaveValue("Parent P");
    await page.keyboard.press("Escape");

    // Expand brings it back.
    await row(page, "Parent P").getByRole("button", { name: "Expand subtasks" }).click();
    await expect(rows(page)).toHaveCount(2);
  });

  test("All view: a dated child nests under its undated parent — hierarchy beats time-grouping", async ({
    page,
    api,
  }) => {
    // Parent undated ("No date" group), child due today ("Today" group). The
    // parent is visible, so the child attaches beneath it IN THE PARENT'S
    // group instead of scattering flat into Today with a breadcrumb.
    const [parent] = await seed(api, [{ title: "Parent P" }]);
    await api.createTask({ title: "Child today", parentId: parent.id, due: daysFromNow(0) });

    await expect(rows(page)).toHaveCount(2);
    await expect(rows(page).nth(0)).toContainText("Parent P");
    await expect(rows(page).nth(1)).toContainText("Child today");
    await expect(rows(page).nth(1)).toHaveAttribute("data-depth", "1");
    await expect(row(page, "Child today").getByTestId("row-breadcrumb")).toHaveCount(0);

    // Group headers reflect the move: the parent's group counts the child,
    // and the child's own group (Today) emptied out and disappeared.
    await expect(page.getByRole("heading", { name: "No date" })).toContainText("2");
    await expect(page.getByRole("heading", { name: "Today" })).toHaveCount(0);
  });

  test("a child due today renders flat in Today with the parent breadcrumb (parent absent)", async ({
    page,
    api,
  }) => {
    // The undated parent does NOT match the Today view, so the child keeps
    // the flat-with-breadcrumb rendering — the breadcrumb appears ONLY when
    // the parent is genuinely absent from the list.
    const [parent] = await seed(api, [{ title: "Parent P" }]); // undated → not in Today
    await api.createTask({ title: "Child today", parentId: parent.id, due: daysFromNow(0) });

    await goTo(page, "Today");
    await expect(rows(page)).toHaveCount(1);
    const child = row(page, "Child today");
    await expect(child).toHaveAttribute("data-depth", "0");
    await expect(child.getByTestId("row-breadcrumb")).toContainText("Parent P");
  });

  test("deleting the parent re-parents the child to top-level", async ({ page, api }) => {
    const [parent] = await seed(api, [{ title: "Parent P" }]);
    const child = await api.createTask({ title: "Child C", parentId: parent.id });
    await expect(rows(page)).toHaveCount(2);
    await expect(rows(page).nth(1)).toHaveAttribute("data-depth", "1");

    await api.deleteTask(parent.id);

    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "Child C")).toHaveAttribute("data-depth", "0");
    await expect(row(page, "Child C").getByTestId("row-breadcrumb")).toHaveCount(0);
    await expect.poll(async () => (await api.getTask(child.id)).parentId ?? "").toBe("");
  });

  test("a child's detail shows the parent breadcrumb instead of an Add-subtask section", async ({
    page,
    api,
  }) => {
    const [parent] = await seed(api, [{ title: "Parent P" }]);
    await api.createTask({ title: "Child C", parentId: parent.id });
    await expect(rows(page)).toHaveCount(2);

    await page.getByText("Child C", { exact: true }).click();
    const p = panel(page);
    await expect(p).toBeVisible();
    await expect(p.getByTestId("subtasks-section")).toHaveCount(0);
    await expect(p.getByLabel("Add subtask")).toHaveCount(0);

    // The breadcrumb names the parent and opens it.
    const crumb = p.getByTestId("parent-breadcrumb");
    await expect(crumb).toContainText("Parent P");
    await crumb.click();
    await expect(p.getByRole("textbox", { name: "Title" })).toHaveValue("Parent P");
    // The parent's panel offers the subtasks section (with the child listed).
    await expect(p.getByTestId("subtasks-section")).toBeVisible();
    await expect(p.getByTestId("subtask-row")).toContainText("Child C");
  });

  test("completing a subtask from the parent's panel drops it from the list and the count", async ({
    page,
    api,
  }) => {
    const [parent] = await seed(api, [{ title: "Parent P" }]);
    await api.createTask({ title: "Child C", parentId: parent.id });
    await expect(rows(page)).toHaveCount(2);

    await page.getByText("Parent P", { exact: true }).click();
    const p = panel(page);
    await p.getByRole("button", { name: "Complete Child C" }).click();

    await expect(p.getByTestId("subtask-row")).toHaveCount(0);
    await expect(rows(page)).toHaveCount(1);
    await page.keyboard.press("Escape");
    await expect(row(page, "Parent P").getByTestId("row-subtasks")).toHaveCount(0);
  });
});
