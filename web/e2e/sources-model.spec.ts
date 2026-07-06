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

  test("syncer-applied labels stay out of the sidebar; a label view is local-by-default with a synced escape hatch", async ({
    page,
    daemon,
  }) => {
    // github's mock applies "bug" to its issues; gcal's applies "calendar".
    // Neither carries any LOCAL task, so neither may clutter the LABELS section
    // (they remain reachable via SOURCES and via filters).
    await expect(page.getByTestId("sidebar-section-sources")).toContainText("github");
    await expect(page.locator('[data-testid="side-item"][data-label="bug"]')).toHaveCount(0);
    await expect(page.locator('[data-testid="side-item"][data-label="calendar"]')).toHaveCount(0);

    // The label view itself is local-by-default: a bare ?label=bug shows no
    // rows (every bug-carrier is synced) …
    await page.goto(`${daemon.baseURL}/?test=1&label=bug`);
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByRole("heading", { name: "bug" })).toBeVisible();
    await expect(rows(page)).toHaveCount(0);
    // … but the header offers the escape hatch, since synced matches exist.
    const chip = page.getByTestId("label-synced-toggle");
    await expect(chip).toContainText("synced");

    // synced=1 (via the chip) folds the synced items in and flips the chip.
    await chip.click();
    await expect(page).toHaveURL(/label=bug&synced=1/);
    await expect(rows(page).first()).toBeVisible();
    expect(await rows(page).count()).toBeGreaterThan(0);
    await expect(page.getByTestId("label-synced-toggle")).toHaveText("hide synced");
  });

  test("gcal events feed the calendar rail but leave the built-in lists", async ({ page, api }) => {
    // A deterministic event today (FIXED_NOW-aligned), due today too. The
    // title must be UNIQUE vs the gcal mock's own seed list (mock.go): the
    // mock is anchored to the daemon's REAL clock, so on the day the real
    // date coincides with FIXED_NOW's date its "Sprint review" lands on the
    // same displayed rail day and a shared title makes the strict-mode
    // locator below match two blocks.
    await api.upsertExternal("gcal:test", [
      {
        externalRef: "ev-audit",
        title: "Quarterly audit sync",
        dueTime: "2026-07-06T16:00:00Z",
        externalData: { start: "2026-07-06T16:00:00Z", end: "2026-07-06T16:30:00Z" },
      },
    ]);

    // The rail is a pure consumer (reads listTasks directly, not views), so it
    // still shows the synced calendar event.
    await expect(page.getByTestId("calendar-rail")).toBeVisible();
    await expect(
      page.getByTestId("cal-event").filter({ hasText: "Quarterly audit sync" }),
    ).toBeVisible();

    // The same event does NOT clutter the All list (local-by-default) …
    await expect(rows(page).filter({ hasText: "Quarterly audit sync" })).toHaveCount(0);

    // … but is reachable in its gcal Source view.
    await page.locator('[data-testid="side-item"][data-label="gcal:test"]').click();
    await expect(rows(page).filter({ hasText: "Quarterly audit sync" }).first()).toBeVisible();
  });
});
