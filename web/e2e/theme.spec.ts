import { test, expect } from "./fixtures";
import { runCommand } from "./helpers";

test.describe("theme", () => {
  test("the sidebar toggle switches the dark class", async ({ page }) => {
    const html = page.locator("html");
    await expect(html).not.toHaveClass(/dark/); // default light

    const toggle = page.getByRole("button", { name: /^theme:/ });
    await toggle.click();
    await expect(html).toHaveClass(/dark/);
    await toggle.click();
    await expect(html).not.toHaveClass(/dark/);
  });

  test("the palette command toggles the theme", async ({ page }) => {
    const html = page.locator("html");
    await expect(html).not.toHaveClass(/dark/);
    await runCommand(page, "Toggle theme");
    await expect(html).toHaveClass(/dark/);
  });
});
