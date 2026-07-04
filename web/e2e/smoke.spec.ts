import { test, expect } from "./fixtures";
import { row } from "./helpers";

// Validates the whole harness end to end: global-setup built a current
// bundle+daemon, the daemon fixture spawned it clean, the page fixture froze
// the clock and navigated in test mode, and the live watch stream delivers a
// task seeded via the API. If this passes, the rest of the suite is on solid
// ground.
test.describe("smoke", () => {
  test("connects, renders first-run, then shows a live-seeded task", async ({ page, api }) => {
    // Footer reports a live connection.
    await expect(page.getByTestId("conn-status")).toHaveAttribute("data-connected", "true");

    // Empty DB → first-run screen (no task rows yet).
    await expect(page.locator("[data-task-row]")).toHaveCount(0);

    // Seed via the API; the watch stream should push it to the replica.
    await api.createTask({ title: "Smoke task", labels: ["p1"] });
    await expect(row(page, "Smoke task")).toBeVisible();

    // A second task also arrives live.
    await api.createTask({ title: "Second task" });
    await expect(page.locator("[data-task-row]")).toHaveCount(2);
  });
});

test.describe("smoke (extensions)", () => {
  test.use({ mode: "extensions" });

  test("mounts the calendar rail and shows synced extension tasks", async ({ page, api }) => {
    // The gcal day-rail panel is defaultOpen → present on load.
    await expect(page.getByTestId("calendar-rail")).toBeVisible();

    // github mock issues synced into the replica appear as source rows.
    const active = await api.listActive();
    const gh = active.filter((t) => t.source === "github");
    expect(gh.length).toBeGreaterThan(0);
    await expect(page.getByTestId("sidebar-section-sources")).toContainText("github");
  });
});
