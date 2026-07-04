import { test, expect } from "./fixtures";
import { row, rows, seed } from "./helpers";

// The web store is a watch-replica: mutations made outside this page (another
// client, the API) must stream in and update the UI without a reload.
test.describe("live sync", () => {
  test("external rename and delete reflect in the open app", async ({ page, api }) => {
    const [t] = await seed(api, [{ title: "Live A" }]);
    await expect(row(page, "Live A")).toBeVisible();

    await api.updateTask(t.id, ["title"], { title: "Live A renamed" });
    await expect(row(page, "Live A renamed")).toBeVisible();
    await expect(row(page, "Live A")).toHaveCount(0);

    await api.deleteTask(t.id);
    await expect(rows(page)).toHaveCount(0);
  });

  test("a task created elsewhere appears while another is open", async ({ page, api }) => {
    const [first] = await seed(api, [{ title: "First open" }]);
    await page.getByText("First open", { exact: true }).click();
    await expect(page.getByTestId("detail-panel")).toBeVisible();

    await api.createTask({ title: "Arrived later" });
    await expect(row(page, "Arrived later")).toBeVisible();
    // The open detail is undisturbed.
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("First open");
    expect(first.id).toBeTruthy();
  });
});
