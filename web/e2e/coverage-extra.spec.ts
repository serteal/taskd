import { test, expect } from "./fixtures";
import { row, rows, toast, seed, dialog } from "./helpers";

const dueOf = (api: import("./fixtures").Api, title: string) =>
  api.listActive().then((ts) => ts.find((t) => t.title === title)?.dueTime);
const labelsOf = (api: import("./fixtures").Api, title: string) =>
  api.listActive().then((ts) => ts.find((t) => t.title === title)?.labels);

test.describe("completed view — reopen", () => {
  test("reopen returns a task to the active set", async ({ page, api }) => {
    await seed(api, [{ title: "Archive me" }]);
    await row(page, "Archive me").getByRole("button", { name: "Complete Archive me" }).click();
    await expect(rows(page)).toHaveCount(0);

    await page.locator('[data-testid="side-item"][data-label="Completed"]').click();
    // The Reopen button is visibility:hidden until its row is group-hovered;
    // hover the row itself so it stays revealed through the click.
    const compRow = page.locator("div.group").filter({ hasText: "Archive me" });
    await compRow.hover();
    await compRow.getByRole("button", { name: "Reopen" }).click();

    await expect(page.getByText("Nothing completed yet.")).toBeVisible();
    expect((await api.listActive()).map((t) => t.title)).toContain("Archive me");
  });
});

test.describe("keyboard — Space selects", () => {
  test("Space toggles the focused row into the selection", async ({ page, api }) => {
    await seed(api, [{ title: "One" }, { title: "Two" }]);
    await expect(rows(page)).toHaveCount(2);

    await page.keyboard.press("j"); // focus first row
    await page.keyboard.press(" "); // select it
    await expect(page.getByTestId("bulk-bar")).toContainText("1 selected");
  });
});

test.describe("detail — due date", () => {
  test("set and clear the due date via the datetime input", async ({ page, api }) => {
    await seed(api, [{ title: "Schedule me" }]);
    await page.getByText("Schedule me", { exact: true }).click();

    await page.getByRole("textbox", { name: "Due date" }).fill("2026-07-10T09:00");
    await expect.poll(() => dueOf(api, "Schedule me")).toBeTruthy();

    await page.getByRole("button", { name: "clear" }).click();
    await expect.poll(() => dueOf(api, "Schedule me")).toBeFalsy();
  });
});

test.describe("row context-menu actions", () => {
  test("Schedule submenu reschedules the row", async ({ page, api }) => {
    await seed(api, [{ title: "Ctx sched" }]);
    await row(page, "Ctx sched").click({ button: "right" });
    await page.getByRole("menuitem", { name: "Schedule" }).click();
    await page.getByRole("button", { name: "Tomorrow" }).click();

    await expect(toast(page, "Scheduled")).toBeVisible();
    await expect.poll(() => dueOf(api, "Ctx sched")).toBeTruthy();
  });

  test("Priority submenu sets the priority label", async ({ page, api }) => {
    await seed(api, [{ title: "Ctx prio" }]);
    await row(page, "Ctx prio").click({ button: "right" });
    await page.getByRole("menuitem", { name: "Priority" }).click();
    await page.getByRole("button", { name: "Priority 1" }).click();

    await expect.poll(() => labelsOf(api, "Ctx prio")).toContain("p1");
  });
});

test.describe("bulk bar — schedule", () => {
  test("schedules every selected task", async ({ page, api }) => {
    await seed(api, [{ title: "B1" }, { title: "B2" }]);
    await page.getByText("B1", { exact: true }).click({ modifiers: ["ControlOrMeta"] });
    await page.getByText("B2", { exact: true }).click({ modifiers: ["ControlOrMeta"] });

    await page.getByTestId("bulk-bar").getByRole("button", { name: "Schedule" }).click();
    await page.getByRole("button", { name: "Tomorrow" }).click();

    await expect(toast(page, "Scheduled 2 tasks")).toBeVisible();
    await expect.poll(() => dueOf(api, "B1")).toBeTruthy();
    await expect.poll(() => dueOf(api, "B2")).toBeTruthy();
  });
});

test.describe("new-task overlay — pills & prefill", () => {
  test("the Priority pill sets priority on the created task", async ({ page, api }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await d.getByRole("button", { name: "Priority", exact: true }).click();
    // The popover's own dropdown is portaled to <body> (see Popover.tsx), so
    // its content isn't a DOM descendant of `d` — scope to the page instead.
    await page.getByRole("button", { name: "Priority 1" }).click();
    await expect(d.getByRole("button", { name: "P1" })).toBeVisible();

    await d.getByRole("textbox", { name: "Task name" }).fill("Pilled");
    await d.getByRole("textbox", { name: "Task name" }).press("Enter");

    await expect(row(page, "Pilled")).toBeVisible();
    await expect.poll(() => labelsOf(api, "Pilled")).toContain("p1");
  });

  test("opening from a project view prefills that project", async ({ page, api }) => {
    await seed(api, [{ title: "Anchor", labels: ["project:home"] }]);
    await page.locator('[data-testid="side-item"][data-label="home"]').click();

    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await expect(d.getByRole("button", { name: /home/ })).toBeVisible(); // project pill prefilled

    await d.getByRole("textbox", { name: "Task name" }).fill("In home");
    await d.getByRole("textbox", { name: "Task name" }).press("Enter");

    await expect.poll(() => labelsOf(api, "In home")).toContain("project:home");
  });

  test("Description text persists as the task's notes", async ({ page, api }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await d.getByRole("textbox", { name: "Task name" }).fill("Has notes via overlay");
    await d.getByRole("textbox", { name: "Description" }).fill("remember to buy oat milk");
    await d.getByRole("button", { name: "Add task", exact: true }).click();

    await expect(row(page, "Has notes via overlay")).toBeVisible();
    const active = await api.listActive();
    expect(active.find((t) => t.title === "Has notes via overlay")?.notes).toBe("remember to buy oat milk");
  });

  test("Labels pill: picking an existing label from the list adds it as a chip", async ({ page, api }) => {
    await seed(api, [{ title: "Has label", labels: ["urgent"] }]);
    await expect(rows(page)).toHaveCount(1);

    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await d.getByRole("button", { name: "Labels", exact: true }).click();
    // Popover content is portaled to <body> (see Popover.tsx) — scope to the
    // page, not `d`, for anything inside the open dropdown itself.
    await page.getByRole("button", { name: "urgent", exact: true }).click();
    await expect(d.getByRole("button", { name: "Remove label urgent" })).toBeVisible();

    // Dismiss the labels popover with an outside click, not Escape: Escape
    // here also closes the whole overlay (see reported bug), not just the
    // popover, which would lose anything typed so far.
    const name = d.getByRole("textbox", { name: "Task name" });
    await name.click();
    await name.fill("Labeled via pill");
    await name.press("Enter");

    await expect.poll(() => labelsOf(api, "Labeled via pill")).toContain("urgent");
  });

  test("removing a label chip before submit drops it from the created task", async ({ page, api }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("Chip removal #temp");
    await expect(d.getByRole("button", { name: "Remove label temp" })).toBeVisible();

    await d.getByRole("button", { name: "Remove label temp" }).click();
    await name.press("Enter");

    // An empty `labels` comes back as undefined (proto JSON omits empty
    // repeated fields), so poll for the task's existence first, then assert
    // on labels separately rather than polling `.not.toContain` directly.
    await expect.poll(() => api.listActive().then((ts) => ts.some((t) => t.title === "Chip removal"))).toBe(
      true,
    );
    const created = (await api.listActive()).find((t) => t.title === "Chip removal");
    expect(created?.labels ?? []).not.toContain("temp");
  });

  test("Project pill: picking a project manually (not just via prefill)", async ({ page, api }) => {
    await seed(api, [{ title: "Anchor", labels: ["project:home"] }]);
    await expect(rows(page)).toHaveCount(1);

    await page.keyboard.press("q"); // opened from the default view — no prefill
    const d = dialog(page, "New task");
    await d.getByRole("button", { name: /Inbox/ }).click();
    await page.getByRole("button", { name: "home", exact: true }).click();
    await expect(d.getByRole("button", { name: /home/ })).toBeVisible();

    await d.getByRole("textbox", { name: "Task name" }).fill("Picked via pill");
    await d.getByRole("textbox", { name: "Task name" }).press("Enter");

    await expect.poll(() => labelsOf(api, "Picked via pill")).toContain("project:home");
  });

  // Regression test for a fixed bug: the Inbox/project pill sits in the
  // dialog's footer row, near its bottom edge. Popover used to render its
  // dropdown inline (`position: absolute`, `top-full`) as a DOM descendant
  // of the dialog, and the dialog itself is `overflow-hidden` — so once
  // there was more than one project option, the dropdown extended below the
  // dialog's clipped bottom edge and was invisible (confirmed live:
  // elementFromPoint at the option's center resolved to the modal's
  // backdrop, not the option — a real click there would have closed the
  // dialog instead of picking a project). Popover now portals its dropdown
  // to <body> with `position: fixed`, flipping above the trigger when there
  // isn't room below, so it's never subject to an ancestor's overflow clip.
  // A plain `.click()` isn't a reliable enough check on its own — some click
  // paths can still land on a clipped element — so this asserts the same
  // elementFromPoint fact used to prove the original bug, then verifies the
  // click actually has effect.
  test("the Inbox/project dropdown is not clipped and is genuinely clickable", async ({ page, api }) => {
    await seed(api, [
      { title: "Home task", labels: ["project:home"] },
      { title: "Taskd task", labels: ["project:taskd"] },
    ]);

    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await d.getByRole("button", { name: /Inbox/ }).click();

    const option = page.getByRole("button", { name: "taskd", exact: true });
    await expect(option).toBeVisible();

    const hitsSelf = await option.evaluate((el) => {
      const r = el.getBoundingClientRect();
      const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
      return hit === el || el.contains(hit); // the button's own text is a nested <span>
    });
    expect(hitsSelf, "a real click at the option's center must land on the option itself").toBe(true);

    await option.click();
    await expect(d.getByRole("button", { name: /taskd/ })).toBeVisible();
  });

  test("Schedule pill: picking Tomorrow sets the due date (not via quick-add)", async ({ page, api }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await d.getByRole("button", { name: "Schedule" }).click();
    await page.getByRole("button", { name: "Tomorrow" }).click();

    await d.getByRole("textbox", { name: "Task name" }).fill("Scheduled via pill");
    await d.getByRole("textbox", { name: "Task name" }).press("Enter");

    await expect.poll(() => dueOf(api, "Scheduled via pill")).toBeTruthy();
  });

  test("Schedule pill: the custom datetime-local input sets an exact due date", async ({ page, api }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await d.getByRole("button", { name: "Schedule" }).click();
    await page.getByRole("textbox", { name: "Custom date and time" }).fill("2026-08-15T09:30");

    await d.getByRole("textbox", { name: "Task name" }).fill("Custom scheduled");
    await d.getByRole("textbox", { name: "Task name" }).press("Enter");

    await expect.poll(() => dueOf(api, "Custom scheduled")).toBeTruthy();
  });

  test("Schedule pill: clearing the due date via its × does not persist a due", async ({ page, api }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    const name = d.getByRole("textbox", { name: "Task name" });
    // "today" is a quick-add token, so the Schedule pill's clear (×) appears.
    await name.fill("Cleared before create today");
    await expect(d.getByRole("button", { name: "Clear", exact: true })).toBeVisible();

    await d.getByRole("button", { name: "Clear", exact: true }).click();
    await name.press("Enter");

    await expect.poll(() => dueOf(api, "Cleared before create")).toBeFalsy();
  });

});
