import { test, expect } from "./fixtures";
import { row, seed, dragTo } from "./helpers";

// The calendar rail is an extension surface, so these run with extensions on.
test.use({ mode: "extensions" });

test.describe("calendar timebox", () => {
  test("dragging a task onto the rail timeboxes it — and does NOT switch sort", async ({ page, api }) => {
    await seed(api, [{ title: "Plan the day" }]);
    await expect(row(page, "Plan the day")).toBeVisible();
    await expect(page.getByTestId("calendar-rail")).toBeVisible();
    await expect(page.locator("header select")).toHaveValue("smart");

    await dragTo(page, "Plan the day", '[data-testid="cal-daycolumn"]', { clientY: 400 });

    // A timebox block renders in the rail…
    await expect(page.getByTestId("cal-timebox")).toHaveCount(1);
    // …the timebox persisted to user_data…
    await expect
      .poll(async () => (await api.listAll()).find((t) => t.title === "Plan the day")?.userData)
      .toHaveProperty("timebox");
    // …and, crucially, the list sort stays smart (calendar drops are not reorders).
    await expect(page.locator("header select")).toHaveValue("smart");
  });

  test("clearing a timebox removes the block", async ({ page, api }) => {
    await seed(api, [{ title: "Plan the day" }]);
    await expect(row(page, "Plan the day")).toBeVisible();

    await dragTo(page, "Plan the day", '[data-testid="cal-daycolumn"]', { clientY: 400 });
    await expect(page.getByTestId("cal-timebox")).toHaveCount(1);

    await page.getByRole("button", { name: "Remove timebox" }).click();

    await expect(page.getByTestId("cal-timebox")).toHaveCount(0);
    await expect
      .poll(async () => (await api.listAll()).find((t) => t.title === "Plan the day")?.userData ?? {})
      .not.toHaveProperty("timebox");
  });

  test("a placed timebox is keyboard-reachable: focus + arrows move it, Delete clears", async ({ page, api }) => {
    await seed(api, [{ title: "Plan the day" }]);
    await expect(row(page, "Plan the day")).toBeVisible();

    await dragTo(page, "Plan the day", '[data-testid="cal-daycolumn"]', { clientY: 300 });
    const box = page.getByTestId("cal-timebox");
    await expect(box).toHaveCount(1);

    const startOf = async () => {
      const ud = (await api.listAll()).find((t) => t.title === "Plan the day")?.userData as
        | Record<string, { start?: string }>
        | undefined;
      return ud?.timebox?.start;
    };
    const before = await startOf();

    // Focus the block (it is a real focusable control) and nudge its start.
    await box.focus();
    await page.keyboard.press("ArrowDown"); // +30 min later
    await expect.poll(startOf).not.toBe(before);
    expect((await startOf())! > before!).toBe(true);

    // Delete/Backspace clears it — the keyboard path to "Remove timebox".
    await page.keyboard.press("Delete");
    await expect(box).toHaveCount(0);
    await expect
      .poll(async () => (await api.listAll()).find((t) => t.title === "Plan the day")?.userData ?? {})
      .not.toHaveProperty("timebox");
  });
});
