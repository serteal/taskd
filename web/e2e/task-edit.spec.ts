import { test, expect } from "./fixtures";
import { row, rows, seed } from "./helpers";

test.describe("edit task", () => {
  test.beforeEach(async ({ api, page }) => {
    await seed(api, [{ title: "Alpha" }]);
    await expect(rows(page)).toHaveCount(1);
  });

  test("detail panel renames the task and persists", async ({ page, api }) => {
    await page.getByText("Alpha", { exact: true }).click();
    const title = page.getByRole("textbox", { name: "Title" });
    await expect(title).toHaveValue("Alpha");

    await title.fill("Alpha renamed");
    await title.press("Enter");

    await expect(row(page, "Alpha renamed")).toBeVisible();
    const active = await api.listActive();
    expect(active.map((t) => t.title)).toContain("Alpha renamed");
  });

  test("detail panel adds a label", async ({ page, api }) => {
    await page.getByText("Alpha", { exact: true }).click();
    // "+ label" opens the autocomplete menu; Enter accepts (creating "urgent").
    await page.getByTestId("add-label").click();
    const add = page.getByRole("textbox", { name: "Label search" });
    await add.fill("urgent");
    await add.press("Enter");

    // Chip shows in the detail (scoped to avoid the sidebar/row copies) and
    // the label persisted.
    await expect(page.getByTestId("detail-panel").getByText("urgent")).toBeVisible();
    await expect
      .poll(async () => (await api.listActive()).find((t) => t.title === "Alpha")?.labels)
      .toContain("urgent");
  });

  test("detail panel edits notes on blur", async ({ page, api }) => {
    await page.getByText("Alpha", { exact: true }).click();
    const notes = page.getByRole("textbox", { name: "Notes" });
    await notes.fill("remember the milk");
    await notes.blur();

    await expect
      .poll(async () => (await api.listActive()).find((t) => t.title === "Alpha")?.notes)
      .toBe("remember the milk");
  });

  test("e key inline-renames the selected row", async ({ page }) => {
    // Select the row (open + close detail leaves it selected), then edit.
    await page.getByText("Alpha", { exact: true }).click();
    await page.keyboard.press("Escape");
    await page.keyboard.press("e");

    const input = page.locator("[data-task-row] input");
    await expect(input).toBeVisible();
    await input.fill("Alpha inline");
    await input.press("Enter");

    await expect(row(page, "Alpha inline")).toBeVisible();
  });
});
