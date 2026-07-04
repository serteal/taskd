import { test, expect } from "./fixtures";
import { rows, row, dialog } from "./helpers";

test.describe("extensions", () => {
  test.use({ mode: "extensions" });

  test("github issues render with a presenter subtitle", async ({ page }) => {
    await expect(page.getByTestId("sidebar-section-sources")).toContainText("github");
    // rowMeta subtitle is "<repo>#<number>".
    await expect(page.getByText(/taskd\/\w+#\d+/).first()).toBeVisible();
  });

  test("opening a github issue shows the custom detail section", async ({ page }) => {
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

// A presenter whose DetailSection throws must be isolated by the host, not
// crash the app. This logs an intentional console.error.
test.describe("extension error isolation", () => {
  test.use({ mode: "extensions", expectNoConsoleErrors: false });

  test("a throwing detail section is caught by the boundary", async ({ page, api, consoleErrors }) => {
    await api.upsertExternal("test", [{ externalRef: "t1", title: "Broken detail item" }]);
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
