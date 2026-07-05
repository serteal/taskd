import { test, expect, daysFromNow } from "./fixtures";
import { rows, toast, seed } from "./helpers";

const dueOf = (api: import("./fixtures").Api, title: string) =>
  api.listActive().then((ts) => ts.find((t) => t.title === title)?.dueTime);

test.describe("overdue section reschedule", () => {
  test("the Overdue header batch-reschedules every overdue task in view", async ({ page, api }) => {
    await seed(api, [
      { title: "Old one", due: daysFromNow(-2) },
      { title: "Old two", due: daysFromNow(-1) },
      { title: "Fresh", due: daysFromNow(3) },
    ]);
    await expect(rows(page)).toHaveCount(3);

    // The Overdue group exists with its one-click reschedule affordance.
    const reschedule = page.getByRole("button", { name: "Reschedule overdue tasks" });
    await expect(reschedule).toBeVisible();

    await reschedule.click();
    await page.getByRole("button", { name: "Tomorrow" }).click();

    // Both overdue tasks moved; the batch toast fired…
    await expect(toast(page, "Scheduled 2 tasks")).toBeVisible();
    // …and the Overdue section (and its button) are gone.
    await expect(page.getByRole("button", { name: "Reschedule overdue tasks" })).toHaveCount(0);

    const before = daysFromNow(0).getTime();
    await expect.poll(async () => new Date((await dueOf(api, "Old one")) ?? 0).getTime()).toBeGreaterThan(before);
    await expect.poll(async () => new Date((await dueOf(api, "Old two")) ?? 0).getTime()).toBeGreaterThan(before);
  });
});
