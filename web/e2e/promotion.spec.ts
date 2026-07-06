import { test, expect, daysFromNow } from "./fixtures";
import { rows, seed } from "./helpers";

// A SavedFilter with `showIn` promotes its matches into a built-in list as a
// named section below the local tasks — the flagship of the sources≠tasks
// promotion model (filters can now pull a synced subset directly into
// Today/Inbox/Upcoming, not only onto their own sidebar surface).
test.describe("filter promotion into built-in lists (showIn)", () => {
  const gotoToday = (page: import("@playwright/test").Page) =>
    page.locator('[data-testid="side-item"][data-label="Today"]').click();

  test("a Today-promoting filter surfaces its synced task, counts it, and never duplicates it", async ({
    page,
    api,
  }) => {
    // One synced github task carrying a "review" label, one local task due
    // today (the Today base). The synced item is quarantined out of Today by
    // default — promotion is what pulls it in.
    await api.upsertExternal("github", [{ externalRef: "gh-1", title: "Synced review" }], {
      applyLabels: ["review"],
    });
    await seed(api, [{ title: "Local today", due: daysFromNow(0) }]);

    // Filter A: everything from the github source, promoted into Today.
    await page.getByTestId("new-filter").click();
    let fb = page.getByTestId("filter-builder");
    await fb.getByRole("textbox", { name: "Filter name" }).fill("GH source");
    await fb.getByLabel("Source").selectOption("github");
    await fb.getByRole("button", { name: "Show matches in today" }).click();
    await fb.getByRole("button", { name: "Save filter" }).click();

    // Filter B: anything labelled "review", also promoted into Today. The
    // synced task matches BOTH — it must appear only once (in the first).
    await page.getByTestId("new-filter").click();
    fb = page.getByTestId("filter-builder");
    await fb.getByRole("textbox", { name: "Filter name" }).fill("Has review");
    await fb.getByRole("button", { name: "Add any-of label" }).click();
    await page.getByRole("button", { name: "review", exact: true }).click();
    await fb.getByRole("button", { name: "Show matches in today" }).click();
    await fb.getByRole("button", { name: "Save filter" }).click();

    await gotoToday(page);
    await expect(page.getByRole("heading", { name: "Today", exact: true })).toBeVisible();

    // The promoted section is headed by the first claiming filter's name and
    // holds the synced task; the base group holds the local one.
    const sections = page.getByTestId("promoted-section");
    await expect(sections).toHaveCount(1); // second filter's section is empty (deduped) and dropped
    await expect(sections).toHaveAttribute("data-filter-name", "GH source");
    await expect(sections).toContainText("Synced review");

    // Not duplicated: exactly one row for the synced task across the whole view.
    await expect(rows(page).filter({ hasText: "Synced review" })).toHaveCount(1);
    // Both the base local task and the promoted synced task are present.
    await expect(rows(page)).toHaveCount(2);
    await expect(rows(page).filter({ hasText: "Local today" })).toHaveCount(1);

    // The sidebar Today badge counts base + promoted (1 + 1).
    await expect(page.locator('[data-testid="side-item"][data-label="Today"]')).toContainText("2");

    // The promoted row is reachable by keyboard (it lives in the same working
    // set as j/k): j past the base row lands on it; Space bulk-selects it.
    await page.keyboard.press("j"); // → Local today (base)
    await page.keyboard.press("j"); // → Synced review (promoted)
    await page.keyboard.press(" ");
    await expect(page.getByTestId("bulk-bar")).toContainText("1 selected");
  });
});
