import { test, expect } from "./fixtures";
import { row, rows, dialog } from "./helpers";

test.describe("first run", () => {
  test("empty state invites the first task and clears once added", async ({ page }) => {
    await expect(page.getByText("No tasks yet.")).toBeVisible();

    await page.getByRole("button", { name: "Add your first task" }).click();
    const d = dialog(page, "New task");
    await expect(d).toBeVisible();

    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("My first task");
    await name.press("Enter");

    await expect(page.getByText("No tasks yet.")).toBeHidden();
    await expect(row(page, "My first task")).toBeVisible();
    await expect(rows(page)).toHaveCount(1);
  });
});
