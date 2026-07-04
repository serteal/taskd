import { test, expect } from "./fixtures";
import { row, rows, seed } from "./helpers";

test.describe("search", () => {
  test.beforeEach(async ({ api, page }) => {
    await seed(api, [
      { title: "Buy milk" },
      { title: "Buy eggs" },
      { title: "Write report", notes: "include the xyzzy metric" },
    ]);
    await expect(rows(page)).toHaveCount(3);
  });

  test("narrows the list by title", async ({ page }) => {
    await page.getByRole("textbox", { name: "Search this view" }).fill("buy");
    await expect(rows(page)).toHaveCount(2);
    await expect(row(page, "Buy milk")).toBeVisible();
    await expect(row(page, "Buy eggs")).toBeVisible();
    await expect(row(page, "Write report")).toHaveCount(0);
  });

  test("matches note text too", async ({ page }) => {
    await page.getByRole("textbox", { name: "Search this view" }).fill("xyzzy");
    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "Write report")).toBeVisible();
  });

  test("escape clears the query and restores the list", async ({ page }) => {
    const search = page.getByRole("textbox", { name: "Search this view" });
    await search.fill("buy");
    await expect(rows(page)).toHaveCount(2);
    await search.press("Escape");
    await expect(search).toHaveValue("");
    await expect(rows(page)).toHaveCount(3);
  });

  test("no matches shows the empty state", async ({ page }) => {
    await page.getByRole("textbox", { name: "Search this view" }).fill("nonexistent-zzz");
    await expect(rows(page)).toHaveCount(0);
  });
});
