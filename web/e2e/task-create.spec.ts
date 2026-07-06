import { test, expect } from "./fixtures";
import { row, rows, dialog } from "./helpers";

const goTo = (page: import("@playwright/test").Page, label: string) =>
  page.locator(`[data-testid="side-item"][data-label="${label}"]`).click();

test.describe("create task", () => {
  test("q opens the overlay; Enter creates and it appears live", async ({ page }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await expect(d).toBeVisible();

    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("Buy milk");
    await name.press("Enter");

    await expect(d).toBeHidden();
    await expect(row(page, "Buy milk")).toBeVisible();
  });

  test("quick-add tokens set priority, label and due", async ({ page, api }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("Write spec #docs p1 tomorrow");

    // Pills reflect the parsed tokens before submit.
    await expect(d.getByRole("button", { name: /P1/ })).toBeVisible();
    await name.press("Enter");

    await expect(row(page, "Write spec")).toBeVisible();

    // And the parse persisted to the server.
    const active = await api.listActive();
    const t = active.find((x) => x.title === "Write spec");
    expect(t, "task was created").toBeTruthy();
    expect(t!.labels).toContain("p1");
    expect(t!.labels).toContain("docs");
    expect(t!.dueTime, "due parsed from 'tomorrow'").toBeTruthy();
  });

  test("recognized quick-add tokens are highlighted live in the title field", async ({ page }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("Write spec #docs p1 tomorrow plain words");

    const tokens = d.getByTestId("quickadd-token");
    await expect(tokens).toHaveCount(3);
    await expect(tokens.nth(0)).toHaveText("#docs");
    await expect(tokens.nth(0)).toHaveAttribute("data-kind", "label");
    await expect(tokens.nth(1)).toHaveText("p1");
    await expect(tokens.nth(1)).toHaveAttribute("data-kind", "priority");
    await expect(tokens.nth(2)).toHaveText("tomorrow");
    await expect(tokens.nth(2)).toHaveAttribute("data-kind", "due");

    // The real textarea underneath keeps carrying the actual value untouched —
    // the highlight is a decorative twin, not a second source of truth.
    await expect(name).toHaveValue("Write spec #docs p1 tomorrow plain words");

    // Editing back out of a recognized word un-highlights it live.
    await name.fill("Write spec #docs p1");
    await expect(d.getByTestId("quickadd-token")).toHaveCount(2);
  });

  test("shift+Enter inserts a newline instead of submitting", async ({ page }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("Line one");
    await name.press("Shift+Enter");
    await expect(d).toBeVisible(); // still open
    await expect(name).toHaveValue("Line one\n");
  });

  test("empty title keeps the Add button disabled", async ({ page }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await expect(d.getByRole("button", { name: "Add task", exact: true })).toBeDisabled();
  });

  test("Escape closes the overlay without creating", async ({ page }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await d.getByRole("textbox", { name: "Task name" }).fill("Abandoned");
    await page.keyboard.press("Escape");
    await expect(d).toBeHidden();
    await expect(rows(page)).toHaveCount(0);
  });

  test("Cancel button closes the overlay without creating", async ({ page }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await d.getByRole("textbox", { name: "Task name" }).fill("Abandoned via cancel");
    await d.getByRole("button", { name: "Cancel" }).click();
    await expect(d).toBeHidden();
    await expect(rows(page)).toHaveCount(0);
  });

  test("clicking the backdrop closes the overlay; clicking inside does not", async ({ page }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await d.getByRole("textbox", { name: "Task name" }).fill("Should stay open");

    // A click inside the dialog panel itself must not close it.
    await d.click({ position: { x: 5, y: 5 } });
    await expect(d).toBeVisible();

    // A click on the backdrop (outside the panel) closes it.
    await page.mouse.click(2, 2);
    await expect(d).toBeHidden();
    await expect(rows(page)).toHaveCount(0);
  });

  test("Ctrl/Cmd+Enter submits even while focus is in the Description field", async ({ page }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await d.getByRole("textbox", { name: "Task name" }).fill("Meta enter task");
    const desc = d.getByRole("textbox", { name: "Description" });
    await desc.fill("some notes");
    await desc.press("ControlOrMeta+Enter");

    await expect(d).toBeHidden();
    await expect(row(page, "Meta enter task")).toBeVisible();
  });

  test("Enter in the Description field inserts a newline; it does not submit", async ({ page }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    await d.getByRole("textbox", { name: "Task name" }).fill("Has notes");
    const desc = d.getByRole("textbox", { name: "Description" });
    await desc.fill("line one");
    await desc.press("Enter");

    await expect(d).toBeVisible(); // still open — only the title field submits on Enter
    await expect(desc).toHaveValue("line one\n");
  });

  test("Add stays disabled when the title is only quick-add tokens", async ({ page }) => {
    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    // "p1" alone is consumed entirely as a priority token, leaving no title.
    await d.getByRole("textbox", { name: "Task name" }).fill("p1");
    await expect(d.getByRole("button", { name: "Add task", exact: true })).toBeDisabled();
  });

  // The Today view prefills the Schedule chip with today's date (kept), BUT a
  // date typed into the title must win over that view default — the bug was
  // that the prefill silently beat the typed token, creating the task due
  // TODAY. Precedence: explicit chip pick/clear > typed token > view default.
  test("Today prefills the chip as 'today', but a typed date token wins the created due", async ({
    page,
    api,
  }) => {
    await goTo(page, "Today");
    await expect(page.getByRole("heading", { name: "Today" })).toBeVisible();

    await page.keyboard.press("q");
    const d = dialog(page, "New task");
    // Prefill still works: before any token, the Schedule chip reads "today".
    await expect(d.getByRole("button", { name: /today/i })).toBeVisible();

    const name = d.getByRole("textbox", { name: "Task name" });
    await name.fill("Prepare demo #dev p1 tomorrow");
    // The typed "tomorrow" token now drives the chip, beating the Today default.
    await expect(d.getByRole("button", { name: /tomorrow/i })).toBeVisible();
    await name.press("Enter");
    await expect(d).toBeHidden();

    // It persisted due TOMORROW (UTC clock is pinned; FIXED_NOW is 2026-07-06),
    // not today — the whole point of the fix.
    await expect
      .poll(() => api.listActive().then((ts) => ts.find((t) => t.title === "Prepare demo")?.dueTime))
      .toBeTruthy();
    const created = (await api.listActive()).find((t) => t.title === "Prepare demo")!;
    expect(new Date(created.dueTime!).toISOString().slice(0, 10)).toBe("2026-07-07");
    expect(created.labels).toEqual(expect.arrayContaining(["dev", "p1"]));
  });

  // Regression tests for a fixed bug: Popover used to close itself on Escape
  // via a bubble-phase document listener, which ran *after* the dialog's own
  // onKeyDown had already fired and closed the whole overlay — discarding
  // whatever was typed. Popover now handles Escape in the capture phase and
  // stops propagation, so only the popover closes.
  test("Escape while a pill popover is open closes only the popover, not the whole overlay", async ({
    page,
  }) => {
    const openers: Array<[string, (d: ReturnType<typeof dialog>) => Promise<void>]> = [
      ["Schedule", (d) => d.getByRole("button", { name: "Schedule" }).click()],
      ["Priority", (d) => d.getByRole("button", { name: "Priority", exact: true }).click()],
      ["Labels", (d) => d.getByRole("button", { name: "Labels", exact: true }).click()],
      ["Project/Inbox", (d) => d.getByRole("button", { name: /Inbox/ }).click()],
    ];

    for (const [, open] of openers) {
      await page.keyboard.press("q");
      const d = dialog(page, "New task");
      await d.getByRole("textbox", { name: "Task name" }).fill("Untouched draft");
      await open(d);
      await page.keyboard.press("Escape");

      await expect(d).toBeVisible();
      await expect(d.getByRole("textbox", { name: "Task name" })).toHaveValue("Untouched draft");
      await page.keyboard.press("Escape"); // now close the overlay itself, ready for the next iteration
      await expect(d).toBeHidden();
    }
  });
});
