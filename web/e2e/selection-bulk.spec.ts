import { test, expect } from "./fixtures";
import { row, rows, toast, seed } from "./helpers";

test.describe("multi-select & bulk actions", () => {
  test.beforeEach(async ({ api, page }) => {
    await seed(api, [{ title: "A" }, { title: "B" }, { title: "C" }]);
    await expect(rows(page)).toHaveCount(3);
  });

  const pick = (page: import("@playwright/test").Page, title: string, mods: ("ControlOrMeta" | "Shift")[]) =>
    page.getByText(title, { exact: true }).click({ modifiers: mods });

  test("ctrl-click selects rows; bulk bar completes them", async ({ page, api }) => {
    await pick(page, "A", ["ControlOrMeta"]);
    await pick(page, "C", ["ControlOrMeta"]);

    const bar = page.getByTestId("bulk-bar");
    await expect(bar).toContainText("2 selected");

    await bar.getByRole("button", { name: "Complete" }).click();

    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "B")).toBeVisible();
    await expect(toast(page, "Completed 2 tasks")).toBeVisible();
    expect((await api.listActive()).map((t) => t.title)).toEqual(["B"]);
  });

  test("shift-click selects a contiguous range", async ({ page }) => {
    await pick(page, "A", []); // plain click sets the anchor
    await pick(page, "C", ["Shift"]);
    await expect(page.getByTestId("bulk-bar")).toContainText("3 selected");
  });

  test("escape clears the selection", async ({ page }) => {
    await pick(page, "A", ["ControlOrMeta"]);
    await expect(page.getByTestId("bulk-bar")).toContainText("1 selected");

    await page.keyboard.press("Escape");
    await expect(page.getByTestId("bulk-bar")).toBeHidden();
  });

  test("bulk delete removes all selected", async ({ page, api }) => {
    await pick(page, "A", ["ControlOrMeta"]);
    await pick(page, "B", ["ControlOrMeta"]);
    await page.getByTestId("bulk-bar").getByRole("button", { name: "Delete" }).click();

    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "C")).toBeVisible();
    await expect(toast(page, "Deleted 2 tasks")).toBeVisible();
  });

  test("bulk Label adds a label to every selected task", async ({ page, api }) => {
    await pick(page, "A", ["ControlOrMeta"]);
    await pick(page, "B", ["ControlOrMeta"]);

    const bar = page.getByTestId("bulk-bar");
    await bar.getByRole("button", { name: "Label", exact: true }).click();
    const input = page.getByPlaceholder("Type a label…");
    await input.fill("triaged");
    await input.press("Enter");

    await expect(toast(page, "Labeled 2 tasks")).toBeVisible();
    const active = await api.listActive();
    expect(active.find((t) => t.title === "A")?.labels).toContain("triaged");
    expect(active.find((t) => t.title === "B")?.labels).toContain("triaged");
    expect(active.find((t) => t.title === "C")?.labels ?? []).not.toContain("triaged");
  });
});
