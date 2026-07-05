import { test, expect } from "./fixtures";
import { runCommand } from "./helpers";

// The binary sidebar toggle / ⌘K command flip between the last-used light and
// dark themes. The active theme id is exposed on <html data-theme> (a stable
// hook the later Settings picker also reads), and `.dark` is toggled so the
// existing `dark:` Tailwind variants keep working.
test.describe("theme", () => {
  test("the sidebar toggle switches the theme and the dark class", async ({ page }) => {
    const html = page.locator("html");
    await expect(html).toHaveAttribute("data-theme", "paper"); // default light
    await expect(html).not.toHaveClass(/dark/);

    const toggle = page.getByRole("button", { name: /^theme:/ });
    await toggle.click();
    await expect(html).toHaveAttribute("data-theme", "dusk");
    await expect(html).toHaveClass(/dark/);

    await toggle.click();
    await expect(html).toHaveAttribute("data-theme", "paper");
    await expect(html).not.toHaveClass(/dark/);
  });

  test("the palette command toggles the theme", async ({ page }) => {
    const html = page.locator("html");
    await expect(html).toHaveAttribute("data-theme", "paper");
    await expect(html).not.toHaveClass(/dark/);
    await runCommand(page, "Toggle theme");
    await expect(html).toHaveAttribute("data-theme", "dusk");
    await expect(html).toHaveClass(/dark/);
  });
});
