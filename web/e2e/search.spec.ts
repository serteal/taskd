import { test, expect } from "./fixtures";
import { rows, seed, openPalette, dialog } from "./helpers";

// The standalone header search bar was removed; ⌘K is the search path now. It
// searches all tasks (title, notes, labels) and opens the chosen match.
test.describe("search (⌘K palette)", () => {
  test.beforeEach(async ({ api, page }) => {
    await seed(api, [
      { title: "Buy milk" },
      { title: "Buy eggs" },
      { title: "Write report", notes: "include the xyzzy metric" },
    ]);
    await expect(rows(page)).toHaveCount(3);
  });

  test("finds tasks by title", async ({ page }) => {
    await openPalette(page);
    const d = dialog(page, "Command palette");
    await d.getByRole("textbox").fill("buy");

    await expect(d.getByRole("button", { name: "Buy milk" })).toBeVisible();
    await expect(d.getByRole("button", { name: "Buy eggs" })).toBeVisible();
    await expect(d.getByRole("button", { name: "Write report" })).toHaveCount(0);
  });

  test("matches note text too", async ({ page }) => {
    await openPalette(page);
    const d = dialog(page, "Command palette");
    await d.getByRole("textbox").fill("xyzzy");

    await expect(d.getByRole("button", { name: "Write report" })).toBeVisible();
  });

  test("opens the matched task", async ({ page }) => {
    await openPalette(page);
    const d = dialog(page, "Command palette");
    await d.getByRole("textbox").fill("Write report");
    await d.getByRole("button", { name: "Write report" }).first().click();

    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("Write report");
  });
});
