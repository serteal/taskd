import { test, expect } from "./fixtures";
import { row, seed, openPalette, runCommand, toast, dialog } from "./helpers";

test.describe("command palette", () => {
  test("runs a sort command", async ({ page, api }) => {
    await seed(api, [{ title: "A" }, { title: "B" }]);
    await expect(row(page, "A")).toBeVisible();

    await runCommand(page, "Sort: Manual");
    await expect(page.locator("header")).toHaveAttribute("data-view-sort", "manual");
  });

  test("navigates via a Go to command", async ({ page }) => {
    await runCommand(page, "Go to Completed");
    await expect(page.getByRole("heading", { name: "Completed" })).toBeVisible();
  });

  test("Enter runs the top result", async ({ page }) => {
    await openPalette(page);
    const d = dialog(page, "Command palette");
    await d.getByRole("textbox").fill("Go to Upcoming");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("heading", { name: "Upcoming" })).toBeVisible();
  });

  test("searches tasks and opens one", async ({ page, api }) => {
    await seed(api, [{ title: "Findme now" }, { title: "Other" }]);
    await expect(row(page, "Findme now")).toBeVisible();

    await runCommand(page, "Findme now");
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("Findme now");
  });
});

test.describe("command palette (extensions)", () => {
  test.use({ mode: "extensions" });

  test("runs an extension command", async ({ page }) => {
    await runCommand(page, "Test: say ok");
    await expect(toast(page, "test ok")).toBeVisible();
  });
});

// The throwing command logs an intentional console.error, so opt out of the
// no-console-errors invariant and instead assert the catch path ran.
test.describe("command palette (throwing extension command)", () => {
  test.use({ mode: "extensions", expectNoConsoleErrors: false });

  test("a throwing extension command is caught, not fatal", async ({ page, consoleErrors }) => {
    await runCommand(page, "Test: throw");
    await expect(toast(page, "That command failed.")).toBeVisible();
    // App is still alive and interactive.
    await expect(page.getByTestId("conn-status")).toBeVisible();
    expect(consoleErrors.some((e) => e.includes("command failed"))).toBeTruthy();
  });
});
