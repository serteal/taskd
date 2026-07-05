import { test, expect } from "./fixtures";
import { rows, seed, dragTo } from "./helpers";

const openViewMenu = (page: import("@playwright/test").Page) =>
  page.locator("header").getByRole("button", { name: "View options" }).click();

// Switch to the board layout via the header's "View options" menu.
const toBoard = async (page: import("@playwright/test").Page) => {
  await openViewMenu(page);
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await page.keyboard.press("Escape"); // close the popover
  await expect(page.getByTestId("board")).toBeVisible();
};

// Pick a board group-by dimension from the (board-only) menu section.
const groupBy = async (page: import("@playwright/test").Page, label: string) => {
  await openViewMenu(page);
  await page.getByRole("radio", { name: label, exact: true }).click();
  await page.keyboard.press("Escape");
};

test.describe("board view", () => {
  test("toggling to board shows the priority columns", async ({ page, api }) => {
    await seed(api, [{ title: "Task X" }]);
    await expect(rows(page)).toHaveCount(1);
    await toBoard(page);
    await expect(page.locator("header")).toHaveAttribute("data-view-board", "true");

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
    await groupBy(page, "Project");

    await dragTo(page, "Loose task", '[data-testid="board-col-project:home"]');

    await expect(page.getByTestId("board-col-project:home")).toContainText("Loose task");
    await expect
      .poll(async () => (await api.listActive()).find((t) => t.title === "Loose task")?.labels)
      .toContain("project:home");
  });

  test("the board layout survives a reload", async ({ page, api }) => {
    await seed(api, [{ title: "Task X" }]);
    await expect(rows(page)).toHaveCount(1);
    await toBoard(page);
    await expect(page.locator("header")).toHaveAttribute("data-view-board", "true");

    await page.reload();
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();
    await expect(page.getByTestId("board")).toBeVisible();
    await expect(page.locator("header")).toHaveAttribute("data-view-board", "true");
  });
});
