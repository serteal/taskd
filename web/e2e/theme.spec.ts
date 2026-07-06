import { test, expect } from "./fixtures";
import { runCommand } from "./helpers";

// The ⌘K command flips between the last-used light and dark themes (the
// sidebar used to have its own duplicate toggle button; it's gone now —
// settings.spec.ts's "light/dark quick toggle" test covers the Settings
// picker's equivalent control). The active theme id is exposed on
// <html data-theme> (a stable hook the Settings picker also reads), and
// `.dark` is toggled so the existing `dark:` Tailwind variants keep working.
test.describe("theme", () => {
  test("the palette command toggles the theme", async ({ page }) => {
    const html = page.locator("html");
    await expect(html).toHaveAttribute("data-theme", "paper");
    await expect(html).not.toHaveClass(/dark/);
    await runCommand(page, "Toggle theme");
    await expect(html).toHaveAttribute("data-theme", "dusk");
    await expect(html).toHaveClass(/dark/);
  });
});
