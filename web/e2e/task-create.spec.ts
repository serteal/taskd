import { test, expect } from "./fixtures";
import { row, rows, dialog } from "./helpers";

test.describe("create task", () => {
  test("q opens the overlay; Enter creates and it appears live", async ({ page }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await expect(d).toBeVisible();

    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("Buy milk");
    await name.press("Enter");

    await expect(d).toBeHidden();
    await expect(row(page, "Buy milk")).toBeVisible();
  });

  test("quick-add tokens set priority, label and due", async ({ page, api }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("Write spec #docs p1 tomorrow");

    // Pills reflect the parsed tokens before submit.
    await expect(d.getByRole("button", { name: /P1/ })).toBeVisible();
    await name.press("Enter");

    await expect(row(page, "Write spec")).toBeVisible();

    // And the parse persisted to the server.
    const active = await api.listActive();
    const t = active.find((x) => x.title === "Write spec");
    expect(t, "task was created").toBeTruthy();
    expect(t!.labels).toContain("p1");
    expect(t!.labels).toContain("docs");
    expect(t!.dueTime, "due parsed from 'tomorrow'").toBeTruthy();
  });

  test("shift+Enter inserts a newline instead of submitting", async ({ page }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("Line one");
    await name.press("Shift+Enter");
    await expect(d).toBeVisible(); // still open
    await expect(name).toHaveValue("Line one\n");
  });

  test("empty title keeps the Add button disabled", async ({ page }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await expect(d.getByRole("button", { name: "Add task", exact: true })).toBeDisabled();
  });

  test("Escape closes the overlay without creating", async ({ page }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await d.getByRole("textbox", { name: "Task name" }).fill("Abandoned");
    await page.keyboard.press("Escape");
    await expect(d).toBeHidden();
    await expect(rows(page)).toHaveCount(0);
  });
});
