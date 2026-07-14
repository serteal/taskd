import { test, expect, daysFromNow } from "./fixtures";
import { row, rows, seed, dialog, toast } from "./helpers";

test.describe("keyboard", () => {
  test("j/k move the selection and Enter opens it", async ({ page, api }) => {
    await seed(api, [
      { title: "First", due: daysFromNow(0) },
      { title: "Second", due: daysFromNow(1) },
    ]);
    await expect(rows(page)).toHaveCount(2);

    await page.keyboard.press("j"); // → First
    await page.keyboard.press("j"); // → Second
    await page.keyboard.press("Enter");
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("Second");

    await page.keyboard.press("Escape"); // close detail, keep selection
    await page.keyboard.press("k"); // → First
    await page.keyboard.press("Enter");
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("First");
  });

  test("? opens the shortcuts help", async ({ page }) => {
    await page.keyboard.press("?");
    await expect(dialog(page, "Keyboard shortcuts")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dialog(page, "Keyboard shortcuts")).toBeHidden();
  });

  test("Cmd/Ctrl-K opens the command palette", async ({ page }) => {
    await page.keyboard.press("ControlOrMeta+k");
    await expect(dialog(page, "Command palette")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dialog(page, "Command palette")).toBeHidden();
  });

  test("c and / are aliases for q — both also open the New Task overlay", async ({
    page,
  }) => {
    // The add-task keymap action carries three default bindings (q, c, /) —
    // all equivalent, all rebindable in Settings → Keybindings.
    for (const key of ["c", "/"]) {
      await page.keyboard.press(key);
      const d = dialog(page, "New task");
      await expect(d, `"${key}" should open the New task overlay like "q"`).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(d).toBeHidden();
    }
  });

  test("collapsing a parent moves a stranded selection (and open detail) to the parent", async ({
    page,
    api,
  }) => {
    // Three rows: "Parent P" (with nested "Child C") then "Aardvark" — smart
    // sort places newer undated tasks first, and the child nests in.
    const [, parent] = await seed(api, [{ title: "Aardvark" }, { title: "Parent P" }]);
    await api.createTask({ title: "Child C", parentId: parent.id });
    await expect(rows(page)).toHaveCount(3);
    await expect(rows(page).nth(1)).toContainText("Child C");

    // Select the child by keyboard and open its detail.
    await page.keyboard.press("j"); // → Parent P
    await page.keyboard.press("j"); // → Child C
    await page.keyboard.press("Enter");
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("Child C");

    // Collapse WITHOUT hovering (a real mouse click would move the hover
    // selection onto the parent row by itself): dispatch the click directly.
    await page
      .getByRole("button", { name: "Collapse subtasks" })
      .evaluate((el) => (el as HTMLElement).click());
    await expect(rows(page)).toHaveCount(2);

    // The open detail followed the hidden child to its parent…
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("Parent P");
    await page.keyboard.press("Escape");

    // …and so did the selection: j moves parent → Aardvark (not a stranded
    // jump back to the first row).
    await page.keyboard.press("j");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("Aardvark");
  });

  test("x completes every selected task, not just the focused one", async ({ page, api }) => {
    await seed(api, [{ title: "A" }, { title: "B" }, { title: "C" }]);
    await expect(rows(page)).toHaveCount(3);

    await page.getByText("A", { exact: true }).click({ modifiers: ["ControlOrMeta"] });
    await page.getByText("B", { exact: true }).click({ modifiers: ["ControlOrMeta"] });
    await expect(page.getByTestId("bulk-bar")).toContainText("2 selected");

    await page.keyboard.press("x");

    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "C")).toBeVisible();
    await expect(page.getByTestId("bulk-bar")).toBeHidden();
  });
});

test.describe("keyboard shortcuts on synced rows", () => {
  test.use({ mode: "extensions" });

  test("e does not inline-rename a selected synced task", async ({ page, api }) => {
    await page.locator('[data-testid="side-item"][data-label="github"]').click();
    const active = await api.listActive();
    const gh = active.find((t) => t.source === "github");
    expect(gh, "expected a github-sourced task").toBeTruthy();
    const syncedRow = rows(page).filter({ hasText: gh!.title }).first();
    await syncedRow.click(); // selects it and opens detail
    await page.keyboard.press("Escape"); // close detail, keep it selected

    await page.keyboard.press("e");

    await expect(page.locator("[data-task-row] input")).toHaveCount(0);
  });

  test("x on a selected synced task shows an info toast and does not complete it", async ({
    page,
    api,
  }) => {
    await page.locator('[data-testid="side-item"][data-label="github"]').click();
    const gh = (await api.listActive()).find((t) => t.source === "github");
    expect(gh, "expected a github-sourced task").toBeTruthy();
    const syncedRow = rows(page).filter({ hasText: gh!.title }).first();
    await syncedRow.click(); // selects it and opens detail
    await page.keyboard.press("Escape"); // close detail, keep it selected

    await page.keyboard.press("x");

    // Completion is source-owned — an info toast, no write.
    await expect(toast(page, "Completion follows the source")).toBeVisible();
    expect((await api.listActive()).some((t) => t.id === gh!.id)).toBe(true);
  });

  test("the completion checkbox is disabled on a synced row", async ({ page, api }) => {
    await page.locator('[data-testid="side-item"][data-label="github"]').click();
    const gh = (await api.listActive()).find((t) => t.source === "github")!;
    const checkbox = rows(page)
      .filter({ hasText: gh.title })
      .first()
      .getByRole("button", { name: `Complete ${gh.title}` });
    await expect(checkbox).toBeDisabled();
  });
});
