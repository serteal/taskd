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

// Default zoom is 0.9 px/min (54 px/hour); each test opens with empty
// localStorage, so this is the live scale. The full-day grid (00:00–24:00)
// overflows and auto-scrolls on load, so a fixed viewport clientY no longer
// maps to a fixed time. Compute the drop's clientY from the (scrolled) day
// column's live bounding box + the wanted minute, so `dragTo`'s synthetic drop
// lands at the intended time regardless of the scroll position.
const PX_PER_MIN = 0.9;
async function dropAtTime(
  page: import("@playwright/test").Page,
  title: string,
  hour: number,
  minute = 0,
): Promise<void> {
  const col = page.getByTestId("cal-daycolumn").first();
  const box = (await col.boundingBox())!;
  const clientY = box.y + (hour * 60 + minute) * PX_PER_MIN;
  await dragTo(page, title, '[data-testid="cal-daycolumn"]', { clientY });
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

    await dropAtTime(page, "Write report", 12); // noon, comfortably mid-day
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

    await dropAtTime(page, "Write report", 12); // noon → top-edge resize has room to move up
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

  test("auto-scrolls near now on load and keeps the now-line in the viewport", async ({ page }) => {
    const scroll = page.getByTestId("cal-scroll");
    await expect(page.getByTestId("calendar-rail")).toBeVisible();

    // The full-day grid overflows, so mount parks "now" ~a third down — the
    // scroll container is not at the top.
    await expect.poll(() => scroll.evaluate((el) => el.scrollTop)).toBeGreaterThan(0);

    // …and the now-line sits inside the visible scroll viewport.
    const nowline = page.getByTestId("cal-nowline");
    await expect(nowline).toBeVisible();
    const nb = (await nowline.boundingBox())!;
    const sb = (await scroll.boundingBox())!;
    expect(nb.y).toBeGreaterThanOrEqual(sb.y);
    expect(nb.y).toBeLessThanOrEqual(sb.y + sb.height);
  });

  test("a timebox can be placed at 23:00 near the bottom of the full-day grid", async ({ page, api }) => {
    await seed(api, [{ title: "Late task" }]);
    await expect(row(page, "Late task")).toBeVisible();

    // 23:00 is far below the auto-scrolled viewport; dropAtTime computes the
    // clientY against the scrolled column so the drop still lands at 23:00.
    await dropAtTime(page, "Late task", 23);
    await expect(page.getByTestId("cal-timebox")).toHaveCount(1);

    await expect.poll(() => timeboxStart(api, "Late task")).toBeTruthy();
    const start = new Date((await timeboxStart(api, "Late task"))!);
    expect(start.getUTCHours()).toBe(23);
    expect(start.getUTCMinutes()).toBe(0);
  });
});
