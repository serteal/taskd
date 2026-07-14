import { test, expect } from "./fixtures";
import { dialog, rows, seed } from "./helpers";
import type { Page } from "@playwright/test";

// Settings → Keybindings: the keymap is user-editable. Rebinds apply live to
// the global handler, the "?" help sheet, and persist across reloads
// (localStorage overrides; defaults stay un-persisted).

const openKeybindings = async (page: Page) => {
  await page.getByTestId("open-settings").click();
  await page.locator('[data-testid="settings-nav"][data-page="keybindings"]').click();
  await expect(page.getByTestId("settings-keybindings")).toBeVisible();
};

const bindingRow = (page: Page, action: string) =>
  page.locator(`[data-testid="binding-row"][data-action="${action}"]`);

test.describe("keybindings", () => {
  test("lists every action with its default bindings", async ({ page }) => {
    await openKeybindings(page);
    await expect(bindingRow(page, "add-task")).toContainText("q");
    await expect(bindingRow(page, "go-inbox")).toContainText("g then i");
    await expect(bindingRow(page, "complete")).toContainText("x");
  });

  test("recording adds a binding that works immediately and survives a reload", async ({
    page,
    api,
  }) => {
    await openKeybindings(page);

    // Record "n" for New task.
    await bindingRow(page, "add-task").getByTestId("binding-add").click();
    await expect(page.getByTestId("binding-recorder")).toBeVisible();
    await page.keyboard.press("n");
    await expect(bindingRow(page, "add-task").getByTestId("binding-chip")).toHaveCount(4); // q c / n

    await page.keyboard.press("Escape"); // close settings
    await page.keyboard.press("n"); // the new binding, live
    await expect(dialog(page, "New task")).toBeVisible();
    await page.keyboard.press("Escape");

    // Persists: reload, the binding still opens the overlay.
    await page.reload();
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();
    await page.keyboard.press("n");
    await expect(dialog(page, "New task")).toBeVisible();
  });

  test("a stolen binding moves between actions (one owner per shortcut)", async ({ page, api }) => {
    await seed(api, [{ title: "Bind me" }]);
    await expect(rows(page)).toHaveCount(1);
    await openKeybindings(page);

    // Give "complete" the key "e" — edit-title's default. It must leave there.
    await bindingRow(page, "complete").getByTestId("binding-add").click();
    await page.keyboard.press("e");
    await expect(bindingRow(page, "complete")).toContainText("e");
    await expect(bindingRow(page, "edit-title")).toContainText("unbound");

    // And "e" now completes instead of renaming.
    await page.keyboard.press("Escape");
    await page.keyboard.press("j"); // select the row
    await page.keyboard.press("e");
    await expect(rows(page)).toHaveCount(0);
  });

  test("chord recording tapes two-key sequences", async ({ page }) => {
    await openKeybindings(page);
    await bindingRow(page, "go-completed").getByTestId("binding-add").click();
    await page.keyboard.press("g");
    await page.keyboard.press("d");
    await expect(bindingRow(page, "go-completed")).toContainText("g then d");

    await page.keyboard.press("Escape");
    await page.keyboard.press("g");
    await page.keyboard.press("d");
    await expect(page.getByRole("heading", { name: "Completed" })).toBeVisible();
  });

  test("reset restores an action's defaults; the help sheet renders the live map", async ({
    page,
  }) => {
    await openKeybindings(page);
    const row = bindingRow(page, "help");
    // Remove "?" then reset — it comes back.
    await row.getByTestId("binding-chip").getByRole("button").click();
    await expect(row).toContainText("unbound");
    await row.getByTestId("binding-reset").click();
    await expect(row.getByTestId("binding-chip")).toContainText("?");

    // Rebind add-task's extra key and check the "?" sheet shows it. A bare
    // letter leaves the recorder taping briefly (chord window) — wait for the
    // chip to land before Escape, or Escape cancels the recording instead.
    await bindingRow(page, "add-task").getByTestId("binding-add").click();
    await page.keyboard.press("n");
    await expect(bindingRow(page, "add-task").getByTestId("binding-chip")).toHaveCount(4);
    await page.keyboard.press("Escape"); // close settings
    await page.keyboard.press("?");
    const help = dialog(page, "Keyboard shortcuts");
    await expect(help).toBeVisible();
    await expect(help).toContainText("n");
    // Its footer link jumps straight to the Keybindings page.
    await help.getByTestId("customize-keybindings").click();
    await expect(page.getByTestId("settings-keybindings")).toBeVisible();
  });
});
