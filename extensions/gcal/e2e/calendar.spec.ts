// Uses the shared test kit — no core-repo internals — to drive the app with
// the gcal extension staged. Demonstrates how any extension tests its surfaces.
import { defineExtensionE2E } from "../../../web/testkit/e2e";

const { test, expect } = defineExtensionE2E("gcal");

const DAY = "2026-07-06"; // FIXED_NOW's day (UTC)

test.describe("gcal calendar rail", () => {
  test("renders a timed event on today and opens its detail", async ({ page, api }) => {
    await api.upsertExternal("gcal:test", [
      {
        externalRef: "ev1",
        title: "Design review",
        externalData: { start: `${DAY}T09:00:00Z`, end: `${DAY}T10:00:00Z` },
      },
    ]);

    await expect(page.getByTestId("calendar-rail")).toBeVisible();
    const ev = page.getByTestId("cal-event").filter({ hasText: "Design review" });
    await expect(ev).toBeVisible();
    await expect(page.getByTestId("cal-nowline")).toBeVisible(); // now-line on today

    await ev.click();
    // The title is source-owned → shown in the (disabled) Title input's value,
    // and the presenter's section renders the event time.
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("Design review");
    await expect(page.getByTestId("detail-panel")).toContainText("calendar event");
  });

  test("shows all-day events in the sticky strip", async ({ page, api }) => {
    await api.upsertExternal("gcal:test", [
      {
        externalRef: "ad1",
        title: "Team offsite",
        externalData: { start: `${DAY}T00:00:00Z`, end: "2026-07-07T00:00:00Z", all_day: true },
      },
    ]);
    await expect(
      page.getByTestId("calendar-rail").getByRole("button", { name: "Team offsite" }),
    ).toBeVisible();
  });

  test("day navigation moves off today (now-line disappears, returns)", async ({ page }) => {
    const rail = page.getByTestId("calendar-rail");
    await expect(page.getByTestId("cal-nowline")).toBeVisible();

    await rail.getByRole("button", { name: "Next day" }).click();
    await expect(page.getByTestId("cal-nowline")).toHaveCount(0);

    await rail.getByRole("button", { name: "today" }).click();
    await expect(page.getByTestId("cal-nowline")).toBeVisible();
  });
});
