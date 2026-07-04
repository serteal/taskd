import { test, expect } from "./fixtures";
import { rows, toast, seed, dragTo } from "./helpers";

test.describe("drag to reassign (sidebar)", () => {
  test("dropping a task on a project moves it there", async ({ page, api }) => {
    await seed(api, [{ title: "Home base", labels: ["project:home"] }, { title: "Loose" }]);
    await expect(rows(page)).toHaveCount(2);
    await expect(page.getByTestId("sidebar-section-projects")).toContainText("home");

    await dragTo(page, "Loose", '[data-testid="side-item"][data-label="home"]');

    await expect(toast(page, "Moved to home")).toBeVisible();
    await expect
      .poll(async () => (await api.listActive()).find((t) => t.title === "Loose")?.labels)
      .toContain("project:home");
  });

  test("dropping a task on a label adds it", async ({ page, api }) => {
    await seed(api, [{ title: "Tagged", labels: ["waiting"] }, { title: "Untagged" }]);
    await expect(rows(page)).toHaveCount(2);
    await expect(page.getByTestId("sidebar-section-labels")).toContainText("waiting");

    await dragTo(page, "Untagged", '[data-testid="side-item"][data-label="waiting"]');

    await expect(toast(page, "Added waiting")).toBeVisible();
    await expect
      .poll(async () => (await api.listActive()).find((t) => t.title === "Untagged")?.labels)
      .toContain("waiting");
  });
});
