import { test, expect } from "./fixtures";
import { row, rows, dialog } from "./helpers";

// A fresh daemon starts with an empty replica. Connected + empty is the
// first-run onboarding card; connected + empty + dismissed falls back to the
// plain empty state; disconnected + empty is the explicit can't-reach state.

test.describe("first run", () => {
  test("the onboarding card shows on a fresh, connected replica", async ({ page }) => {
    const card = page.getByTestId("onboarding");
    await expect(card).toBeVisible();
    // Its three steps.
    await expect(card).toContainText("Add your first task");
    await expect(card).toContainText("Install it as an app");
    await expect(card).toContainText("Connect your sources");
    await expect(card).toContainText("Settings → Extensions");
  });

  test("adding the first task clears the onboarding card", async ({ page }) => {
    await expect(page.getByTestId("onboarding")).toBeVisible();

    await page.getByRole("button", { name: "Add your first task" }).click();
    const d = dialog(page, "New task");
    await expect(d).toBeVisible();

    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("My first task");
    await name.press("Enter");

    await expect(page.getByTestId("onboarding")).toBeHidden();
    await expect(row(page, "My first task")).toBeVisible();
    await expect(rows(page)).toHaveCount(1);
  });

  test("a fresh load never flashes the can't-reach state while connecting", async ({ page }) => {
    // Watch the DOM from document start across a reload: the store begins
    // every cold load disconnected for a few hundred ms, and that must render
    // as a quiet blank — not a premature "Can't reach the daemon".
    await page.addInitScript(() => {
      const w = window as unknown as { __sawDisconnected: boolean };
      w.__sawDisconnected = false;
      const check = () => {
        if (document.querySelector('[data-testid="disconnected-empty"]')) w.__sawDisconnected = true;
      };
      document.addEventListener("DOMContentLoaded", () => {
        check();
        new MutationObserver(check).observe(document.body, { childList: true, subtree: true });
      });
    });
    await page.reload();
    await expect(page.getByTestId("onboarding")).toBeVisible();
    expect(
      await page.evaluate(() => (window as unknown as { __sawDisconnected: boolean }).__sawDisconnected),
    ).toBe(false);
  });

  test("dismissing the card persists across a reload and falls back to the plain empty state", async ({
    page,
  }) => {
    await page.getByTestId("onboarding-dismiss").click();
    await expect(page.getByTestId("onboarding")).toBeHidden();

    // The plain empty state takes over.
    const empty = page.getByTestId("empty-state");
    await expect(empty).toBeVisible();
    await expect(empty).toContainText("No tasks yet.");

    // Reload: the dismissal persists (localStorage) — onboarding never returns.
    await page.reload();
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();
    await expect(page.getByTestId("onboarding")).toBeHidden();
    await expect(page.getByTestId("empty-state")).toBeVisible();
  });
});

test.describe("disconnected", () => {
  // A killed daemon makes the browser log failed requests; that's expected here.
  test.use({ expectNoConsoleErrors: false });

  test("shows an explicit can't-reach-daemon state when the daemon dies with an empty replica", async ({
    page,
    daemon,
  }) => {
    // Fresh, connected, empty → onboarding.
    await expect(page.getByTestId("conn-status")).toHaveAttribute("data-connected", "true");
    await expect(page.getByTestId("onboarding")).toBeVisible();

    // Kill the daemon; the store's watch stream fails and it flips to
    // reconnecting, with an empty replica.
    await daemon.stop();

    const dc = page.getByTestId("disconnected-empty");
    await expect(dc).toBeVisible();
    await expect(dc).toContainText("Can't reach the daemon");
    await expect(dc).toContainText("Is taskd running?");
    await expect(dc).toContainText(daemon.baseURL);
    // Footer reflects the same.
    await expect(page.getByTestId("conn-status")).toHaveAttribute("data-connected", "false");
  });
});
