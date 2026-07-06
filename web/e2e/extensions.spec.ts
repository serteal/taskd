import { test, expect } from "./fixtures";
import { rows, row, dialog, seed } from "./helpers";

test.describe("extensions", () => {
  test.use({ mode: "extensions" });

  // Synced items are local-by-default hidden from the built-in lists; they live
  // in their Source view. Open the github feed before asserting on its rows.
  const openGithub = (page: import("@playwright/test").Page) =>
    page.locator('[data-testid="side-item"][data-label="github"]').click();

  test("github issues render with a presenter subtitle", async ({ page }) => {
    await expect(page.getByTestId("sidebar-section-sources")).toContainText("github");
    await openGithub(page);
    // rowMeta subtitle is "<repo>#<number>".
    await expect(page.getByText(/taskd\/\w+#\d+/).first()).toBeVisible();
  });

  test("opening a github issue shows the custom detail section", async ({ page }) => {
    await openGithub(page);
    await page.getByText(/taskd\/\w+#\d+/).first().click();
    const detail = page.getByTestId("detail-panel");
    await expect(detail).toBeVisible();
    // IssueDetail (the presenter's DetailSection) renders the repo…
    await expect(detail.getByText(/taskd\//).first()).toBeVisible();
    // …and the synced-source banner is present (title is source-owned).
    await expect(detail).toContainText("Synced from");
  });

  test("an extension quick-add token adds its label", async ({ page, api }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("Ship it urgent"); // testext token: "urgent" → label
    await name.press("Enter");

    await expect(row(page, "Ship it")).toBeVisible();
    await expect
      .poll(async () => (await api.listActive()).find((t) => t.title === "Ship it")?.labels)
      .toContain("urgent");
  });

  test("the gcal 'noon' token schedules the task", async ({ page, api }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("Lunch noon");
    await name.press("Enter");

    await expect(row(page, "Lunch")).toBeVisible();
    await expect
      .poll(async () => (await api.listActive()).find((t) => t.title === "Lunch")?.dueTime)
      .toBeTruthy();
  });
});

// Detail and the extension calendar rail share the right side. On a wide
// viewport (≥1440px) both show at once; below that the detail wins the slot.
test.describe("detail + calendar rail coexistence", () => {
  test.use({ mode: "extensions" });

  test("at ≥1440px, opening a task detail keeps the calendar rail visible alongside it", async ({
    page,
    api,
  }) => {
    await page.setViewportSize({ width: 1600, height: 900 });
    await seed(api, [{ title: "Wide detail" }]);
    const rail = page.getByTestId("calendar-rail");
    await expect(rail).toBeVisible(); // defaultOpen

    await page.getByText("Wide detail", { exact: true }).click();
    await expect(page.getByTestId("detail-panel")).toBeVisible();
    // The rail stays put — both rails coexist on a wide viewport.
    await expect(rail).toBeVisible();
  });

  test("at the default viewport, detail still wins the slot and the rail hides", async ({
    page,
    api,
  }) => {
    await seed(api, [{ title: "Narrow detail" }]);
    const rail = page.getByTestId("calendar-rail");
    await expect(rail).toBeVisible();

    await page.getByText("Narrow detail", { exact: true }).click();
    await expect(page.getByTestId("detail-panel")).toBeVisible();
    await expect(rail).toBeHidden();
  });
});

// A presenter whose DetailSection throws must be isolated by the host, not
// crash the app. This logs an intentional console.error.
test.describe("extension error isolation", () => {
  test.use({ mode: "extensions", expectNoConsoleErrors: false });

  test("a throwing detail section is caught by the boundary", async ({ page, api, consoleErrors }) => {
    await api.upsertExternal("test", [{ externalRef: "t1", title: "Broken detail item" }]);
    // Synced "test" items live in the Source view, not the built-in lists.
    await page.locator('[data-testid="side-item"][data-label="test"]').click();
    // Presenter rows put the title + subtitle in one span, so match by
    // substring rather than the exact-title `row()` helper.
    const r = rows(page).filter({ hasText: "Broken detail item" });
    await expect(r).toBeVisible();
    await expect(r).toContainText("TEST"); // testext subtitle

    await r.getByText("Broken detail item").click();
    const detail = page.getByTestId("detail-panel");
    // The rest of the detail still renders; only the extension section fails.
    await expect(detail).toContainText("This extension surface failed to render.");
    expect(consoleErrors.some((e) => e.includes("render error"))).toBeTruthy();
  });
});

// registerTheme works like registerPanel/registerPresenter: an extension's
// contribution shows up in Settings' picker (grouped under its own name)
// alongside the built-in catalog.
test.describe("extension-contributed theme", () => {
  test.use({ mode: "extensions" });

  test("appears in Settings' theme picker, grouped under the extension's name, and applies live", async ({
    page,
  }) => {
    const html = page.locator("html");
    await page.getByTestId("open-settings").click();

    await expect(page.getByText("Testext", { exact: true })).toBeVisible(); // group heading
    const testextTheme = page.locator('[data-testid="theme-option"][data-theme-id="testext-theme"]');
    await expect(testextTheme).toContainText("Testext Theme");
    await expect(testextTheme).toHaveAttribute("data-active", "false");

    await testextTheme.click();
    await expect(html).toHaveAttribute("data-theme", "testext-theme");
    await expect(html).toHaveClass(/dark/);
    await expect(testextTheme).toHaveAttribute("data-active", "true");
  });

  test("a saved extension theme survives a reload (re-applied once the extension registers it)", async ({
    page,
  }) => {
    const html = page.locator("html");
    await page.getByTestId("open-settings").click();
    await page.locator('[data-testid="theme-option"][data-theme-id="testext-theme"]').click();
    await expect(html).toHaveAttribute("data-theme", "testext-theme");

    // First paint (before this extension's bundle loads) can only fall back to
    // a built-in default; the saved id is re-applied once registerTheme runs.
    await page.reload();
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();
    await expect(html).toHaveAttribute("data-theme", "testext-theme");
  });
});
