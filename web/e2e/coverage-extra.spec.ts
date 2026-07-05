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
    await d.getByRole("button", { name: "Priority 1" }).click();
    await expect(d.getByRole("button", { name: "P1" })).toBeVisible();

    await d.getByRole("textbox", { name: "Task name" }).fill("Pilled");
    await d.getByRole("textbox", { name: "Task name" }).press("Enter");

    await expect(row(page, "Pilled")).toBeVisible();
    await expect.poll(() => labelsOf(api, "Pilled")).toContain("p1");
  });

  test("'add more' keeps the overlay open across creates", async ({ page, api }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await d.getByRole("checkbox").check();
    const name = d.getByRole("textbox", { name: "Task name" });

    await name.fill("First more");
    await name.press("Enter");
    await expect(d).toBeVisible(); // stays open
    await name.fill("Second more");
    await name.press("Enter");
    await expect(d).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(row(page, "First more")).toBeVisible();
    await expect(row(page, "Second more")).toBeVisible();
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
});
