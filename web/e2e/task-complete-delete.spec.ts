import { test, expect } from "./fixtures";
import { row, rows, toast, seed } from "./helpers";

test.describe("complete & delete", () => {
  test("checkbox completes the task and shows an undo toast", async ({ page, api }) => {
    await seed(api, [{ title: "Task A" }]);
    await expect(rows(page)).toHaveCount(1);

    await row(page, "Task A").getByRole("button", { name: "Complete Task A" }).click();

    await expect(rows(page)).toHaveCount(0);
    await expect(toast(page, "Completed")).toBeVisible();
    // Server-side it is archived, not active.
    expect(await api.listActive()).toHaveLength(0);
  });

  test("x key completes the selected row", async ({ page, api }) => {
    await seed(api, [{ title: "Task A" }, { title: "Task B" }]);
    await expect(rows(page)).toHaveCount(2);

    await page.getByText("Task A", { exact: true }).click();
    await page.keyboard.press("Escape"); // close detail, keep selection
    await page.keyboard.press("x");

    await expect(row(page, "Task A")).toHaveCount(0);
    await expect(row(page, "Task B")).toBeVisible();
    expect((await api.listActive()).map((t) => t.title)).toEqual(["Task B"]);
  });

  test("context-menu delete removes the row with an undo toast", async ({ page, api }) => {
    await seed(api, [{ title: "Task A" }]);
    await expect(rows(page)).toHaveCount(1);

    await row(page, "Task A").click({ button: "right" });
    await page.getByRole("menu", { name: "Task actions" }).getByRole("menuitem", { name: "Delete" }).click();

    await expect(rows(page)).toHaveCount(0);
    await expect(toast(page, "Deleted")).toBeVisible();
    expect(await api.listActive()).toHaveLength(0);
  });

  test("detail panel delete confirms then removes", async ({ page, api }) => {
    await seed(api, [{ title: "Task A" }]);
    await expect(rows(page)).toHaveCount(1);
    page.on("dialog", (d) => d.accept()); // accept the confirm()

    await page.getByText("Task A", { exact: true }).click();
    await page.getByRole("button", { name: "Delete", exact: true }).click();

    await expect(rows(page)).toHaveCount(0);
    expect(await api.listActive()).toHaveLength(0);
  });
});
