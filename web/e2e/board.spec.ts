import { test, expect } from "./fixtures";
import { rows, seed, dragTo } from "./helpers";

const toBoard = async (page: import("@playwright/test").Page) => {
  await page.locator("header").getByRole("button", { name: "board", exact: true }).click();
  await expect(page.getByTestId("board")).toBeVisible();
};

test.describe("board view", () => {
  test("toggling to board shows the priority columns", async ({ page, api }) => {
    await seed(api, [{ title: "Task X" }]);
    await expect(rows(page)).toHaveCount(1);
    await toBoard(page);

    for (const key of ["p1", "p2", "p3", "none"]) {
      await expect(page.getByTestId(`board-col-${key}`)).toBeVisible();
    }
  });

  test("dragging a card to the P1 column sets its priority", async ({ page, api }) => {
    await seed(api, [{ title: "Task X" }]);
    await expect(rows(page)).toHaveCount(1);
    await toBoard(page);

    await dragTo(page, "Task X", '[data-testid="board-col-p1"]');

    await expect(page.getByTestId("board-col-p1")).toContainText("Task X");
    await expect
      .poll(async () => (await api.listActive()).find((t) => t.title === "Task X")?.labels)
      .toContain("p1");
  });

  test("dragging a card between project columns reassigns the project", async ({ page, api }) => {
    await seed(api, [
      { title: "Home task", labels: ["project:home"] },
      { title: "Loose task" },
    ]);
    await expect(rows(page)).toHaveCount(2);
    await toBoard(page);
    await page.locator("header select").selectOption("project");

    await dragTo(page, "Loose task", '[data-testid="board-col-project:home"]');

    await expect(page.getByTestId("board-col-project:home")).toContainText("Loose task");
    await expect
      .poll(async () => (await api.listActive()).find((t) => t.title === "Loose task")?.labels)
      .toContain("project:home");
  });
});
