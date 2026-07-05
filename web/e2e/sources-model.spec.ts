import { test, expect } from "./fixtures";
import { rows } from "./helpers";

// Proves the sources≠tasks model (milestone M2): the built-in lists are
// local-by-default, synced items are quarantined under Sources, and a filter
// is the opt-in mechanism that promotes a chosen synced subset onto its own
// surface. The calendar rail stays a pure consumer.
test.describe("sources ≠ tasks model", () => {
  test.use({ mode: "extensions" });

  test("synced items are hidden from the built-in lists but live in their Source view", async ({
    page,
    api,
  }) => {
    // Seed a deterministic calendar event (the gcal *mock* is anchored to the
    // daemon's real clock, so we control our own, FIXED_NOW-aligned event —
    // dated today, and even due today, to prove it still leaves the lists).
    await api.upsertExternal("gcal:test", [
      {
        externalRef: "ev-sprint",
        title: "Sprint review",
        dueTime: "2026-07-06T16:00:00Z",
        externalData: { start: "2026-07-06T16:00:00Z", end: "2026-07-06T16:30:00Z" },
      },
    ]);

    // The default landing is All — local-by-default, so with no local tasks it
    // is empty even though github + gcal have synced plenty.
    await expect(page.getByRole("heading", { name: "All tasks" })).toBeVisible();
    await expect(rows(page)).toHaveCount(0);

    // Those items are quarantined under the Sources area.
    const sources = page.getByTestId("sidebar-section-sources");
    await expect(sources).toContainText("github");
    await expect(sources).toContainText("gcal:test");

    // Opening the github Source view surfaces its items.
    await page.locator('[data-testid="side-item"][data-label="github"]').click();
    await expect(page.getByRole("heading", { name: "github" })).toBeVisible();
    await expect(rows(page).first()).toBeVisible();
    expect(await rows(page).count()).toBeGreaterThan(0);

    // Inbox and Today stay empty: no local tasks, and synced items never leak —
    // not even the calendar event that is due today.
    await page.locator('[data-testid="side-item"][data-label="Inbox"]').click();
    await expect(rows(page)).toHaveCount(0);
    await page.locator('[data-testid="side-item"][data-label="Today"]').click();
    await expect(rows(page)).toHaveCount(0);
  });

  test("a filter promotes a chosen synced subset onto its own surface", async ({ page }) => {
    // Build a filter that selects the github source.
    await page.getByTestId("new-filter").click();
    const fb = page.getByTestId("filter-builder");
    await expect(fb).toBeVisible();
    await fb.getByRole("textbox", { name: "Filter name" }).fill("GH review");
    await fb.getByLabel("Source").selectOption("github");
    await fb.getByRole("button", { name: "Save filter" }).click();

    // Saving pins it in the Filters section and navigates to it.
    await expect(page.getByRole("heading", { name: "GH review" })).toBeVisible();
    await expect(page.getByTestId("sidebar-section-filters")).toContainText("GH review");

    // The github items — hidden from the built-in lists — are promoted here.
    await expect(rows(page).first()).toBeVisible();
    expect(await rows(page).count()).toBeGreaterThan(0);

    // And the filter survives a reload (persisted to localStorage).
    await page.reload();
    await expect(page.getByTestId("sidebar-section-filters")).toContainText("GH review");
  });

  test("gcal events feed the calendar rail but leave the built-in lists", async ({ page, api }) => {
    // A deterministic event today (FIXED_NOW-aligned), due today too.
    await api.upsertExternal("gcal:test", [
      {
        externalRef: "ev-sprint",
        title: "Sprint review",
        dueTime: "2026-07-06T16:00:00Z",
        externalData: { start: "2026-07-06T16:00:00Z", end: "2026-07-06T16:30:00Z" },
      },
    ]);

    // The rail is a pure consumer (reads listTasks directly, not views), so it
    // still shows the synced calendar event.
    await expect(page.getByTestId("calendar-rail")).toBeVisible();
    await expect(
      page.getByTestId("cal-event").filter({ hasText: "Sprint review" }),
    ).toBeVisible();

    // The same event does NOT clutter the All list (local-by-default) …
    await expect(rows(page).filter({ hasText: "Sprint review" })).toHaveCount(0);

    // … but is reachable in its gcal Source view.
    await page.locator('[data-testid="side-item"][data-label="gcal:test"]').click();
    await expect(rows(page).filter({ hasText: "Sprint review" }).first()).toBeVisible();
  });
});
