import { test, expect } from "./fixtures";
import { row, rows, toast, clickUndo, seed } from "./helpers";

test.describe("undo", () => {
  test("undo restores a completed task", async ({ page, api }) => {
    await seed(api, [{ title: "Task A" }]);
    await expect(rows(page)).toHaveCount(1);

    await row(page, "Task A").getByRole("button", { name: "Complete Task A" }).click();
    await expect(rows(page)).toHaveCount(0);

    await expect(toast(page, "Completed")).toBeVisible();
    await clickUndo(page);

    await expect(row(page, "Task A")).toBeVisible();
    expect((await api.listActive()).map((t) => t.title)).toEqual(["Task A"]);
  });

  test("undo recreates a deleted task", async ({ page, api }) => {
    await seed(api, [{ title: "Task A" }]);
    await expect(rows(page)).toHaveCount(1);

    await row(page, "Task A").click({ button: "right" });
    await page.getByRole("menu", { name: "Task actions" }).getByRole("menuitem", { name: "Delete" }).click();
    await expect(rows(page)).toHaveCount(0);

    await expect(toast(page, "Deleted")).toBeVisible();
    await clickUndo(page);

    // Recreated with the same title (a fresh id).
    await expect(row(page, "Task A")).toBeVisible();
  });
});
