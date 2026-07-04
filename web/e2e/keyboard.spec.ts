import { test, expect, daysFromNow } from "./fixtures";
import { rows, seed, dialog } from "./helpers";

test.describe("keyboard", () => {
  test("j/k move the selection and Enter opens it", async ({ page, api }) => {
    await seed(api, [
      { title: "First", due: daysFromNow(0) },
      { title: "Second", due: daysFromNow(1) },
    ]);
    await expect(rows(page)).toHaveCount(2);

    await page.keyboard.press("j"); // → First
    await page.keyboard.press("j"); // → Second
    await page.keyboard.press("Enter");
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("Second");

    await page.keyboard.press("Escape"); // close detail, keep selection
    await page.keyboard.press("k"); // → First
    await page.keyboard.press("Enter");
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("First");
  });

  test("? opens the shortcuts help", async ({ page }) => {
    await page.keyboard.press("?");
    await expect(dialog(page, "Keyboard shortcuts")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dialog(page, "Keyboard shortcuts")).toBeHidden();
  });

  test("Cmd/Ctrl-K opens the command palette", async ({ page }) => {
    await page.keyboard.press("ControlOrMeta+k");
    await expect(dialog(page, "Command palette")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dialog(page, "Command palette")).toBeHidden();
  });
});
