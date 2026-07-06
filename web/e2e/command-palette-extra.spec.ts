import { test, expect } from "./fixtures";
import { row, rows, toast, seed, dialog, openPalette, runCommand } from "./helpers";

// Extra ⌘K coverage beyond command-palette.spec.ts: saved-view/filter
// reachability, selected-task action commands, "Go to" nav for labels/
// projects/sources, keyboard nav within the open list, the empty-query
// no-match state, and backdrop-click dismissal.

test.describe("command palette — saved views / saved filters reachability", () => {
  // commands.tsx's buildStaticCommands() now loops over savedViews and
  // savedFilters to build a "Go to <name>" command for each, the same as
  // every other navigable surface (fixed views, labels/projects, sources).
  test("a saved view's name is a 'Go to' command that reapplies it", async ({ page, api }) => {
    await seed(api, [{ title: "Home task", labels: ["project:home"] }]);
    await page.locator('[data-testid="side-item"][data-label="home"]').click();
    await expect(page.getByRole("heading", { name: "home" })).toBeVisible();

    // Name distinctive enough it can't coincidentally collide with any
    // built-in command's title/group/keywords. The naming modal (not a native
    // window.prompt) collects it.
    await runCommand(page, "Save current view");
    const dlg = page.getByTestId("save-view-dialog");
    await dlg.getByRole("textbox", { name: "View name" }).fill("Quailridge Focus");
    await dlg.getByRole("button", { name: "Save" }).click();
    await expect(page.getByTestId("saved-view")).toContainText("Quailridge Focus");

    // Navigate elsewhere, then use the palette to jump back to it.
    await page.locator('[data-testid="side-item"][data-label="Inbox"]').click();
    await expect(page.getByRole("heading", { name: "Inbox" })).toBeVisible();

    await runCommand(page, "Quailridge");
    await expect(page.getByRole("heading", { name: "home" })).toBeVisible();
  });

  test("a saved filter's name is a 'Go to' command that navigates to it", async ({ page }) => {
    await page.getByTestId("new-filter").click();
    const fb = page.getByTestId("filter-builder");
    await fb.getByRole("textbox", { name: "Filter name" }).fill("Wrenfield Reviews");
    await fb.getByRole("button", { name: "Save filter" }).click();
    await expect(page.getByRole("heading", { name: "Wrenfield Reviews" })).toBeVisible();

    await page.locator('[data-testid="side-item"][data-label="Inbox"]').click();
    await expect(page.getByRole("heading", { name: "Inbox" })).toBeVisible();

    await runCommand(page, "Wrenfield");
    await expect(page.getByRole("heading", { name: "Wrenfield Reviews" })).toBeVisible();
  });
});

test.describe("command palette — selected task action commands", () => {
  test("do not appear when nothing is selected", async ({ page, api }) => {
    await seed(api, [{ title: "Solo task" }]);
    await openPalette(page);
    const d = dialog(page, "Command palette");
    await expect(d.getByText("Selected task", { exact: true })).toHaveCount(0);
  });

  test("Open and Schedule-today run on the selected task", async ({ page, api }) => {
    await seed(api, [{ title: "Origin Task" }]);
    await expect(rows(page)).toHaveCount(1);

    // Select the row: click opens the detail panel, Escape closes the panel
    // (setOpenId(null)) but leaves selectedId set (App.tsx's Escape handler
    // for the base case only clears openId) — same pattern as
    // task-edit.spec.ts's "e key inline-renames" test.
    await page.getByText("Origin Task", { exact: true }).click();
    await page.keyboard.press("Escape");

    await runCommand(page, "Open Origin Task", "Open “Origin Task”");
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("Origin Task");
    await page.keyboard.press("Escape"); // close detail again, keep selection

    await runCommand(page, "Schedule Origin Task today", "Schedule “Origin Task” today");
    await expect(toast(page, "Scheduled")).toBeVisible();
    // humanDue() renders a same-day due date as "today" in the row.
    await expect(row(page, "Origin Task")).toContainText("today");
  });

  test("Complete runs on the selected task and shows an undo toast", async ({ page, api }) => {
    await seed(api, [{ title: "Finishable" }]);
    await expect(rows(page)).toHaveCount(1);

    await page.getByText("Finishable", { exact: true }).click();
    await page.keyboard.press("Escape");

    await runCommand(page, "Complete Finishable", "Complete “Finishable”");
    await expect(toast(page, "Completed")).toBeVisible();
    await expect(toast(page).getByRole("button", { name: "Undo" }).first()).toBeVisible();
    // Completed tasks leave the active replica entirely.
    await expect(row(page, "Finishable")).toHaveCount(0);
    expect(await api.listActive()).toHaveLength(0);
  });

  test("Delete runs on the selected task and shows an undo toast, with no confirm prompt", async ({
    page,
    api,
  }) => {
    await seed(api, [{ title: "Removable" }]);
    await expect(rows(page)).toHaveCount(1);

    await page.getByText("Removable", { exact: true }).click();
    await page.keyboard.press("Escape");

    // Unlike DetailPanel's own Delete button (which calls window.confirm),
    // deleteTaskWithUndo() — shared by the palette, context menu, and bulk
    // bar — never prompts; the undo toast is the only safety net. No
    // page.on("dialog") listener is registered here on purpose: if the
    // palette ever grew a confirm(), this test would hang/timeout instead of
    // silently accepting it.
    await runCommand(page, "Delete Removable", "Delete “Removable”");
    await expect(toast(page, "Deleted")).toBeVisible();
    await expect(toast(page).getByRole("button", { name: "Undo" }).first()).toBeVisible();
    await expect(row(page, "Removable")).toHaveCount(0);
    expect(await api.listActive()).toHaveLength(0);
  });
});

test.describe("command palette — Go to label/project/source navigation", () => {
  test("Go to project <name> and Go to label <name> navigate to the filtered view", async ({
    page,
    api,
  }) => {
    await seed(api, [
      { title: "Home task", labels: ["project:home"] },
      { title: "Urgent task", labels: ["urgent"] },
      { title: "Plain task" },
    ]);
    await expect(rows(page)).toHaveCount(3);

    await runCommand(page, "Go to project home");
    // The command's own title strips the "project:" prefix ("Go to project
    // home", via chipParts().val), and the Sidebar list item is labelled just
    // "home" too — the page heading matches now (viewTitle() strips it too).
    await expect(page.getByRole("heading", { name: "home", exact: true })).toBeVisible();
    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "Home task")).toBeVisible();

    await runCommand(page, "Go to label urgent");
    await expect(page.getByRole("heading", { name: "urgent" })).toBeVisible();
    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "Urgent task")).toBeVisible();
  });
});

test.describe("command palette — Go to source navigation (extensions)", () => {
  test.use({ mode: "extensions" });

  test("Go to <source> navigates to that source's quarantined view", async ({ page }) => {
    await runCommand(page, "Go to github");
    await expect(page.getByRole("heading", { name: "github" })).toBeVisible();
    await expect(rows(page).first()).toBeVisible();
  });
});

test.describe("command palette — keyboard navigation of the open list", () => {
  test("ArrowDown/ArrowUp move the highlight; Enter runs the highlighted item, not just the first", async ({
    page,
  }) => {
    // Fresh app, no seeded tasks: querying "go to" surfaces exactly the 5
    // fixed-view commands (no label/project/source/task entries exist yet).
    // All 5 titles are "Go to <View>", so scoreMatch gives them an identical
    // score for this query — Array.prototype.sort is stable, so ties keep
    // their buildStaticCommands() push order: Inbox, Today, Upcoming,
    // All tasks, Completed.
    await openPalette(page);
    const d = dialog(page, "Command palette");
    await d.getByRole("textbox").fill("go to");
    await expect(d.getByRole("button")).toHaveText([
      "Go to Inbox",
      "Go to Today",
      "Go to Upcoming",
      "Go to All tasks",
      "Go to Completed",
    ]);

    await page.keyboard.press("ArrowDown"); // 0 -> 1 (Today)
    await page.keyboard.press("ArrowDown"); // 1 -> 2 (Upcoming)
    await page.keyboard.press("ArrowDown"); // 2 -> 3 (All tasks)
    await page.keyboard.press("ArrowUp"); // 3 -> 2 (Upcoming)
    await page.keyboard.press("Enter");

    // If Enter always ran the first result, this would be "Inbox" instead.
    await expect(page.getByRole("heading", { name: "Upcoming" })).toBeVisible();
  });
});

test.describe("command palette — empty-query no-match state", () => {
  test("a query matching nothing shows the explicit 'No matches' state", async ({ page }) => {
    await openPalette(page);
    const d = dialog(page, "Command palette");
    await d.getByRole("textbox").fill("zzz-nothing-matches-this-9999");

    await expect(d.getByText("No matches")).toBeVisible();
    await expect(d.getByRole("button")).toHaveCount(0);
  });
});

test.describe("command palette — closing via the backdrop", () => {
  test("mousedown inside the palette doesn't close it; clicking the backdrop does", async ({ page }) => {
    await openPalette(page);
    const d = dialog(page, "Command palette");

    // The dialog card stops propagation on its own onMouseDown, so a click
    // that lands inside it (here, the search input) must not bubble to the
    // full-screen backdrop's onClose handler.
    await d.getByRole("textbox").click();
    await expect(d).toBeVisible();

    // A click outside the 560px-wide, top-anchored card — near the
    // viewport's top-left corner — hits the backdrop and closes it, the
    // same onMouseDown={onClose} wiring FilterBuilder uses.
    await page.mouse.click(2, 2);
    await expect(d).toBeHidden();
  });
});
