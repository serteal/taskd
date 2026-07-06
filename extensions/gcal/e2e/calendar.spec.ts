// Uses the shared test kit — no core-repo internals — to drive the app with
// the gcal extension staged. Demonstrates how any extension tests its surfaces.
import { defineExtensionE2E, seed, row, dragTo } from "../../../web/testkit/e2e";

const { test, expect } = defineExtensionE2E("gcal");

const DAY = "2026-07-06"; // FIXED_NOW's day (UTC)

// user_data.timebox.{start,end} of a task, as ISO strings (or undefined).
async function timeboxRange(api: import("../../../web/testkit/e2e").Api, title: string) {
  const t = (await api.listAll()).find((x) => x.title === title);
  const tb = (t?.userData as Record<string, { start?: string; end?: string }> | undefined)?.timebox;
  return { start: tb?.start, end: tb?.end };
}
async function timeboxStart(api: import("../../../web/testkit/e2e").Api, title: string) {
  return (await timeboxRange(api, title)).start;
}

test.describe("gcal calendar rail", () => {
  test("renders a timed event on today and opens its detail", async ({ page, api }) => {
    await api.upsertExternal("gcal:test", [
      {
        externalRef: "ev1",
        title: "Vendor demo",
        externalData: { start: `${DAY}T09:00:00Z`, end: `${DAY}T10:00:00Z` },
      },
    ]);

    await expect(page.getByTestId("calendar-rail")).toBeVisible();
    const ev = page.getByTestId("cal-event").filter({ hasText: "Vendor demo" });
    await expect(ev).toBeVisible();
    await expect(page.getByTestId("cal-nowline")).toBeVisible(); // now-line on today

    await ev.click();
    // The title is source-owned → shown in the (disabled) Title input's value,
    // and the presenter's section renders the event time.
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("Vendor demo");
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

  test("view toggle switches between Day and Week (1 vs 7 day columns)", async ({ page }) => {
    const rail = page.getByTestId("calendar-rail");
    await expect(page.getByTestId("cal-daycolumn")).toHaveCount(1); // Day is the default

    await rail.getByRole("button", { name: "Week", exact: true }).click();
    await expect(page.getByTestId("cal-daycolumn")).toHaveCount(7);

    await rail.getByRole("button", { name: "3d", exact: true }).click();
    await expect(page.getByTestId("cal-daycolumn")).toHaveCount(3);

    await rail.getByRole("button", { name: "Day", exact: true }).click();
    await expect(page.getByTestId("cal-daycolumn")).toHaveCount(1);
  });

  test("zooming in makes the hour scale taller", async ({ page, api }) => {
    await api.upsertExternal("gcal:test", [
      {
        externalRef: "ev1",
        title: "Vendor demo",
        externalData: { start: `${DAY}T09:00:00Z`, end: `${DAY}T10:00:00Z` },
      },
    ]);
    const rail = page.getByTestId("calendar-rail");
    const ev = page.getByTestId("cal-event").filter({ hasText: "Vendor demo" });
    await expect(ev).toBeVisible();
    const before = (await ev.boundingBox())!.height;

    await rail.getByRole("button", { name: "Zoom in" }).click();
    await expect.poll(async () => (await ev.boundingBox())!.height).toBeGreaterThan(before);
  });

  test("a placed timebox can be nudged and cleared with the keyboard", async ({ page, api }) => {
    await seed(api, [{ title: "Write report" }]);
    await expect(row(page, "Write report")).toBeVisible();

    await dragTo(page, "Write report", '[data-testid="cal-daycolumn"]', { clientY: 250 });
    const box = page.getByTestId("cal-timebox");
    await expect(box).toHaveCount(1);
    const before = await timeboxStart(api, "Write report");

    // Focus the block and push its start later (ArrowDown = +30 min).
    await box.focus();
    await page.keyboard.press("ArrowDown");
    await expect.poll(() => timeboxStart(api, "Write report")).not.toBe(before);
    expect((await timeboxStart(api, "Write report"))! > before!).toBe(true);

    // Delete clears the timebox.
    await page.keyboard.press("Delete");
    await expect(box).toHaveCount(0);
  });

  test("dragging the top handle resizes the start time, keeping the end fixed", async ({ page, api }) => {
    await seed(api, [{ title: "Write report" }]);
    await expect(row(page, "Write report")).toBeVisible();

    await dragTo(page, "Write report", '[data-testid="cal-daycolumn"]', { clientY: 250 });
    const box = page.getByTestId("cal-timebox");
    await expect(box).toHaveCount(1);
    const before = await timeboxRange(api, "Write report");

    const handle = page.getByTestId("cal-timebox-resize-top");
    const handleBox = (await handle.boundingBox())!;
    const x = handleBox.x + handleBox.width / 2;
    const y = handleBox.y + handleBox.height / 2;
    await page.mouse.move(x, y);
    await page.mouse.down();
    await page.mouse.move(x, y - 54, { steps: 5 }); // -60min at the default zoom
    await page.mouse.up();

    await expect.poll(() => timeboxRange(api, "Write report").then((r) => r.start)).not.toBe(before.start);
    const after = await timeboxRange(api, "Write report");
    expect(new Date(after.start!).getTime()).toBeLessThan(new Date(before.start!).getTime());
    expect(after.end).toBe(before.end); // end untouched by a start-edge resize
  });
});
