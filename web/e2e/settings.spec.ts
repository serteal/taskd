import { test, expect, daysFromNow, FIXED_NOW } from "./fixtures";
import { runCommand } from "./helpers";
import type { Page } from "@playwright/test";

// The Settings overlay is a paged modal: a nav rail (General / Appearance /
// Keybindings / Notifications / Extensions / About) with one page rendered at
// a time. It opens from the Sidebar gear and from a ⌘K command, and is a
// focus-trapped, Escape-dismissable panel like ShortcutsHelp / the palette.

/** Open Settings from the sidebar, optionally navigating to a page. */
const openSettings = async (page: Page, pageId?: string) => {
  await page.getByTestId("open-settings").click();
  if (pageId) await page.locator(`[data-testid="settings-nav"][data-page="${pageId}"]`).click();
};

test.describe("settings", () => {
  test("opens from the sidebar with a page nav and closes on Escape", async ({ page }) => {
    await openSettings(page);
    const panel = page.getByTestId("settings");
    await expect(panel).toBeVisible();
    // The default page is General; every page is reachable from the nav rail.
    await expect(panel).toContainText("Startup view");
    const order = await panel
      .locator('[data-testid="settings-nav"]')
      .evaluateAll((els) => els.map((e) => e.getAttribute("data-page")));
    expect(order).toEqual([
      "general",
      "appearance",
      "keybindings",
      "notifications",
      "extensions",
      "about",
    ]);

    // Switching pages swaps the content pane.
    await panel.locator('[data-testid="settings-nav"][data-page="notifications"]').click();
    await expect(panel).toContainText("Due-task reminders");
    await expect(panel).not.toContainText("Startup view");

    await page.keyboard.press("Escape");
    await expect(panel).toBeHidden();
  });

  test("About shows the daemon version and address", async ({ page }) => {
    await openSettings(page, "about");
    // A dev build reports "dev"; a stamped build reports "v<git describe>",
    // which can carry hyphens ("v0.1.0-3-gabc123", "v2bd8777-dirty"). Either
    // way the About line carries a non-empty version string.
    await expect(page.getByTestId("app-version")).toHaveText(/^(dev|v[\w.-]+)$/);
    await expect(page.getByTestId("daemon-address")).toContainText("127.0.0.1");
  });

  test("opens from the ⌘K command", async ({ page }) => {
    await runCommand(page, "Open settings");
    await expect(page.getByTestId("settings")).toBeVisible();
  });

  test("the theme picker switches the active theme live", async ({ page }) => {
    const html = page.locator("html");
    await expect(html).toHaveAttribute("data-theme", "paper"); // default light
    await expect(html).not.toHaveClass(/dark/);

    await openSettings(page, "appearance");

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
    await openSettings(page, "appearance");
    await page.getByRole("button", { name: "dark mode" }).click();
    await expect(html).toHaveClass(/dark/);
    await page.getByRole("button", { name: "light mode" }).click();
    await expect(html).not.toHaveClass(/dark/);
  });

  test("shows no extensions on a clean daemon", async ({ page }) => {
    await openSettings(page, "extensions");
    await expect(page.getByTestId("settings-extensions")).toContainText("No extensions installed");
  });

  test("the startup view picker changes which view a bare load opens to", async ({ page, daemon }) => {
    await openSettings(page); // General is the landing page
    const inbox = page.locator('[data-testid="default-view-option"][data-view="inbox"]');
    const today = page.locator('[data-testid="default-view-option"][data-view="today"]');
    await expect(today).toHaveAttribute("aria-pressed", "true"); // default
    await expect(inbox).toHaveAttribute("aria-pressed", "false");

    await inbox.click();
    await expect(inbox).toHaveAttribute("aria-pressed", "true");
    await expect(today).toHaveAttribute("aria-pressed", "false");

    // A bare load (no ?view= at all — unlike the fixture's own navigation,
    // which always passes view=all) now opens to Inbox instead of Today.
    await page.goto(`${daemon.baseURL}/?test=1`);
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();
    await expect(page.getByRole("heading", { name: "Inbox" })).toBeVisible();
  });
});

test.describe("due-task reminders", () => {
  // Replace the real Notification API with an in-page spy so enabling the
  // toggle and a task becoming due can be asserted without a real OS
  // permission prompt or native notification. addInitScript only affects
  // navigations that happen AFTER it's registered, and the `page` fixture has
  // already loaded the app by the time this runs — so reload once to apply it.
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => {
      (window as unknown as { __notifications: unknown[] }).__notifications = [];
      class FakeNotification {
        static permission: NotificationPermission = "default";
        static requestPermission(): Promise<NotificationPermission> {
          FakeNotification.permission = "granted";
          return Promise.resolve("granted");
        }
        constructor(title: string, options?: NotificationOptions) {
          (window as unknown as { __notifications: unknown[] }).__notifications.push({
            title,
            body: options?.body,
          });
        }
      }
      // @ts-expect-error -- test stub, not a full Notification implementation
      window.Notification = FakeNotification;
    });
    await page.reload();
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();
  });

  test("enabling requests permission and the choice persists across a reload", async ({ page }) => {
    await openSettings(page, "notifications");
    const toggle = page.getByTestId("notify-due-toggle");
    await expect(toggle).toHaveAttribute("aria-checked", "false");

    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-checked", "true");
    expect(await page.evaluate(() => Notification.permission)).toBe("granted");

    await page.reload();
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();
    await openSettings(page, "notifications");
    await expect(page.getByTestId("notify-due-toggle")).toHaveAttribute("aria-checked", "true");
  });

  test("fires once a task becomes due, but not for tasks already overdue when it's turned on", async ({
    page,
    api,
  }) => {
    // Already overdue before the feature is enabled — priming should not
    // notify for this (no backlog storm on opt-in).
    await api.createTask({ title: "Old overdue thing", due: daysFromNow(-2) });

    await openSettings(page, "notifications");
    await page.getByTestId("notify-due-toggle").click();
    await page.keyboard.press("Escape");

    const count = () => page.evaluate(() => (window as unknown as { __notifications: unknown[] }).__notifications.length);
    expect(await count()).toBe(0);

    // A task created already-overdue AFTER enabling is new to the replica —
    // it wasn't part of the primed backlog, so it fires immediately.
    await api.createTask({ title: "Fresh overdue thing", due: daysFromNow(-1) });
    await expect.poll(count).toBe(1);
    const n = await page.evaluate(() => (window as unknown as { __notifications: { title: string; body: string }[] }).__notifications[0]);
    expect(n.title).toBe("Task due");
    expect(n.body).toBe("Fresh overdue thing");
  });

  test("on load, summarises tasks that became due while the app was closed", async ({ page, api }) => {
    // Simulate a prior session: reminders were on, last evaluated two days ago.
    const twoDaysAgo = FIXED_NOW.getTime() - 2 * 86_400_000;
    await page.evaluate((wm) => {
      localStorage.setItem("taskd-notify-due", "1");
      localStorage.setItem("taskd-reminders-last-seen", String(wm));
    }, twoDaysAgo);

    // Two local tasks fell due yesterday — inside the (watermark, now] window.
    await api.createTask({ title: "Missed one", due: daysFromNow(-1) });
    await api.createTask({ title: "Missed two", due: daysFromNow(-1) });

    await page.reload();
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();

    // ONE summary notification, not one-per-task.
    const count = () =>
      page.evaluate(() => (window as unknown as { __notifications: unknown[] }).__notifications.length);
    await expect.poll(count).toBe(1);
    const n = await page.evaluate(
      () =>
        (window as unknown as { __notifications: { title: string; body: string }[] }).__notifications[0],
    );
    expect(n.title).toBe("Tasks due");
    expect(n.body).toBe("2 tasks became due while you were away");

    // …and an on-screen toast says the same.
    await expect(page.getByText("2 tasks became due while you were away")).toBeVisible();
  });
});

test.describe("settings (extensions)", () => {
  test.use({ mode: "extensions" });

  test("lists installed extensions with capability badges", async ({ page }) => {
    await openSettings(page, "extensions");
    const rows = page.getByTestId("ext-row");
    await expect(rows).toHaveCount(3); // gcal, github, testext (staged fixtures)

    const github = page.locator('[data-testid="ext-row"][data-ext="github"]');
    await expect(github).toContainText("github");
    await expect(github).toContainText(/syncer/i);
    await expect(github).toContainText(/web/i);
  });

  test("each extension row lists the sources it currently feeds, with counts", async ({ page }) => {
    await openSettings(page, "extensions");

    // gcal's mock syncs into source "gcal:personal"; the row derives that from
    // the live replica and shows an item count.
    const gcal = page.locator('[data-testid="ext-row"][data-ext="gcal"]');
    await expect(gcal.getByTestId("ext-sources")).toContainText(/gcal:personal · \d+ item/);

    const github = page.locator('[data-testid="ext-row"][data-ext="github"]');
    await expect(github.getByTestId("ext-sources")).toContainText(/github · \d+ item/);
  });

  test("toggling an extension reflects the new state in the list", async ({ page }) => {
    await openSettings(page, "extensions");

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
