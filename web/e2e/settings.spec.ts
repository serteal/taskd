import { test, expect } from "./fixtures";
import { runCommand } from "./helpers";

// The Settings overlay wires up two earlier-wave features: the full theme
// picker (Appearance) and extension enable/disable (Extensions). It opens from
// the Sidebar gear and from a ⌘K command, and is a focus-trapped, Escape-
// dismissable panel like ShortcutsHelp / the command palette.

test.describe("settings", () => {
  test("opens from the sidebar and closes on Escape", async ({ page }) => {
    await page.getByTestId("open-settings").click();
    const panel = page.getByTestId("settings");
    await expect(panel).toBeVisible();
    await expect(panel).toContainText("Appearance");
    await expect(panel).toContainText("Extensions");
    await expect(panel).toContainText("About");

    await page.keyboard.press("Escape");
    await expect(panel).toBeHidden();
  });

  test("opens from the ⌘K command", async ({ page }) => {
    await runCommand(page, "Open settings");
    await expect(page.getByTestId("settings")).toBeVisible();
  });

  test("the theme picker switches the active theme live", async ({ page }) => {
    const html = page.locator("html");
    await expect(html).toHaveAttribute("data-theme", "paper"); // default light
    await expect(html).not.toHaveClass(/dark/);

    await page.getByTestId("open-settings").click();

    // The active theme is marked, the others are not.
    const paper = page.locator('[data-testid="theme-option"][data-theme-id="paper"]');
    const mocha = page.locator('[data-testid="theme-option"][data-theme-id="mocha"]');
    await expect(paper).toHaveAttribute("data-active", "true");
    await expect(mocha).toHaveAttribute("data-active", "false");

    // Clicking a theme applies it live: <html data-theme> + the `.dark` class.
    await mocha.click();
    await expect(html).toHaveAttribute("data-theme", "mocha");
    await expect(html).toHaveClass(/dark/);
    await expect(mocha).toHaveAttribute("data-active", "true");
    await expect(paper).toHaveAttribute("data-active", "false");
  });

  test("the light/dark quick toggle flips the mode", async ({ page }) => {
    const html = page.locator("html");
    await page.getByTestId("open-settings").click();
    await page.getByRole("button", { name: "dark mode" }).click();
    await expect(html).toHaveClass(/dark/);
    await page.getByRole("button", { name: "light mode" }).click();
    await expect(html).not.toHaveClass(/dark/);
  });

  test("shows no extensions on a clean daemon", async ({ page }) => {
    await page.getByTestId("open-settings").click();
    await expect(page.getByTestId("settings-extensions")).toContainText("No extensions installed");
  });
});

test.describe("settings (extensions)", () => {
  test.use({ mode: "extensions" });

  test("lists installed extensions with capability badges", async ({ page }) => {
    await page.getByTestId("open-settings").click();
    const rows = page.getByTestId("ext-row");
    await expect(rows).toHaveCount(3); // gcal, github, testext (staged fixtures)

    const github = page.locator('[data-testid="ext-row"][data-ext="github"]');
    await expect(github).toContainText("github");
    await expect(github).toContainText(/syncer/i);
    await expect(github).toContainText(/web/i);
  });

  test("toggling an extension reflects the new state in the list", async ({ page }) => {
    await page.getByTestId("open-settings").click();

    const row = page.locator('[data-testid="ext-row"][data-ext="testext"]');
    const toggle = page.locator('[data-testid="ext-toggle"][data-ext="testext"]');
    await expect(row).toHaveAttribute("data-enabled", "true");
    await expect(toggle).toHaveAttribute("aria-checked", "true");

    // Disable it — the syncer stop is immediate; the list updates to reflect it.
    await toggle.click();
    await expect(row).toHaveAttribute("data-enabled", "false");
    await expect(toggle).toHaveAttribute("aria-checked", "false");
    await expect(row).toContainText("Disabled");

    // A web-bundle extension needs a reload to fully unload its already-loaded
    // UI, surfaced as a subtle hint (we don't click Reload — it navigates).
    await expect(page.getByTestId("settings-reload-hint")).toBeVisible();

    // Re-enabling flips it back.
    await toggle.click();
    await expect(row).toHaveAttribute("data-enabled", "true");
    await expect(toggle).toHaveAttribute("aria-checked", "true");
  });
});
