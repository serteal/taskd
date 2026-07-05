import { test, expect } from "./fixtures";
import { row, rows, toast, seed } from "./helpers";

const labelsOf = (api: import("./fixtures").Api, title: string) =>
  api.listActive().then((ts) => ts.find((t) => t.title === title)?.labels);
const userDataOf = (api: import("./fixtures").Api, title: string) =>
  api.listAll().then((ts) => ts.find((t) => t.title === title)?.userData);
const menu = (page: import("@playwright/test").Page) =>
  page.getByRole("menu", { name: "Task actions" });

test.describe("row context menu", () => {
  test("right-click opens the menu; outside-click and Escape dismiss it", async ({ page, api }) => {
    await seed(api, [{ title: "Ctx A" }]);
    await row(page, "Ctx A").click({ button: "right" });
    await expect(menu(page)).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(menu(page)).toBeHidden();

    await row(page, "Ctx A").click({ button: "right" });
    await expect(menu(page)).toBeVisible();
    await page.mouse.click(5, 5); // click far outside the menu
    await expect(menu(page)).toBeHidden();
  });

  test("Open reveals the detail panel", async ({ page, api }) => {
    await seed(api, [{ title: "Ctx open" }]);
    await row(page, "Ctx open").click({ button: "right" });
    await menu(page).getByRole("menuitem", { name: "Open" }).click();

    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("Ctx open");
  });

  test("Rename starts inline editing", async ({ page, api }) => {
    await seed(api, [{ title: "Ctx rename" }]);
    await row(page, "Ctx rename").click({ button: "right" });
    await menu(page).getByRole("menuitem", { name: "Rename" }).click();

    const input = page.locator("[data-task-row] input");
    await expect(input).toBeVisible();
    await input.fill("Ctx renamed");
    await input.press("Enter");
    await expect(row(page, "Ctx renamed")).toBeVisible();
  });

  test("Complete completes the task with an undo toast", async ({ page, api }) => {
    await seed(api, [{ title: "Ctx done" }]);
    await row(page, "Ctx done").click({ button: "right" });
    await menu(page).getByRole("menuitem", { name: "Complete" }).click();

    await expect(rows(page)).toHaveCount(0);
    await expect(toast(page, "Completed")).toBeVisible();
    expect(await api.listActive()).toHaveLength(0);
  });

  test("Priority submenu sets p1", async ({ page, api }) => {
    await seed(api, [{ title: "Ctx p" }]);
    await row(page, "Ctx p").click({ button: "right" });
    await menu(page).getByRole("menuitem", { name: "Priority" }).click();
    await page.getByRole("button", { name: "Priority 1" }).click();

    await expect.poll(() => labelsOf(api, "Ctx p")).toContain("p1");
  });

  test("Add label submenu adds a label", async ({ page, api }) => {
    await seed(api, [{ title: "Ctx label" }]);
    await row(page, "Ctx label").click({ button: "right" });
    await menu(page).getByRole("menuitem", { name: "Add label" }).click();
    const input = page.getByPlaceholder("Type a label…");
    await input.fill("urgent");
    await input.press("Enter");

    await expect.poll(() => labelsOf(api, "Ctx label")).toContain("urgent");
  });

  test("Add label submenu: picking an existing label from the list", async ({ page, api }) => {
    await seed(api, [{ title: "Ctx label existing" }, { title: "Has label", labels: ["reviewed"] }]);
    await row(page, "Ctx label existing").click({ button: "right" });
    await menu(page).getByRole("menuitem", { name: "Add label" }).click();
    await menu(page).getByRole("button", { name: "reviewed", exact: true }).click();

    await expect.poll(() => labelsOf(api, "Ctx label existing")).toContain("reviewed");
  });

  test("Plan today timeboxes the task and shows a planned-time chip", async ({ page, api }) => {
    await seed(api, [{ title: "Ctx plan" }]);
    await row(page, "Ctx plan").click({ button: "right" });
    await menu(page).getByRole("menuitem", { name: "Plan today" }).click();

    await expect(toast(page, "Planned")).toBeVisible();
    await expect(row(page, "Ctx plan").getByTestId("row-timebox")).toBeVisible();
    await expect.poll(() => userDataOf(api, "Ctx plan")).toHaveProperty("timebox");
  });
});

test.describe("row context menu — synced tasks", () => {
  test.use({ mode: "extensions" });

  test("a synced (source) task omits inline title editing", async ({ page, api }) => {
    const active = await api.listActive();
    const gh = active.find((t) => t.source === "github");
    expect(gh, "expected a github-sourced task").toBeTruthy();
    // Synced items live in their Source view now (built-in lists are local-only).
    await page.locator('[data-testid="side-item"][data-label="github"]').click();
    const syncedRow = rows(page).filter({ hasText: gh!.title }).first();
    await expect(syncedRow).toBeVisible();

    await syncedRow.click({ button: "right" });
    const m = page.getByRole("menu", { name: "Task actions" });
    await expect(m).toBeVisible();
    // Source-owned title → no Rename; the source-appropriate actions remain.
    await expect(m.getByRole("menuitem", { name: "Rename" })).toHaveCount(0);
    await expect(m.getByRole("menuitem", { name: "Open" })).toBeVisible();
    await expect(m.getByRole("menuitem", { name: "Delete" })).toBeVisible();
  });
});
