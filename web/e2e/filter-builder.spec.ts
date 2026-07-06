import { test, expect, daysFromNow } from "./fixtures";
import { row, rows, seed } from "./helpers";

// FilterBuilder is the authoring surface for the promotion mechanism
// (DESIGN §5b). sources-model.spec.ts proves the model end to end with one
// golden-path filter (source only); this file drives the builder's other
// fields, its live preview, edit/remove, and its dialog mechanics.
const newFilter = async (page: import("@playwright/test").Page) => {
  await page.getByTestId("new-filter").click();
  return page.getByTestId("filter-builder");
};

test.describe("filter builder", () => {
  test("Labels — any-of promotes a task carrying any of the chosen labels", async ({ page, api }) => {
    await seed(api, [
      { title: "Has urgent", labels: ["urgent"] },
      { title: "Has other", labels: ["other"] },
      { title: "No labels" },
    ]);
    const fb = await newFilter(page);
    await fb.getByRole("textbox", { name: "Filter name" }).fill("Any urgent");
    await fb.getByRole("button", { name: "Add any-of label" }).click();
    // Popover content is portaled to <body> (see Popover.tsx) — scope to the
    // page, not `fb`, for anything inside the open dropdown itself.
    await page.getByRole("button", { name: "urgent", exact: true }).click();
    await expect(fb.getByRole("button", { name: "Remove label urgent" })).toBeVisible();

    await fb.getByRole("button", { name: "Save filter" }).click();

    await expect(page.getByRole("heading", { name: "Any urgent" })).toBeVisible();
    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "Has urgent")).toBeVisible();
  });

  test("Labels — all-of requires every chosen label, not just one", async ({ page, api }) => {
    await seed(api, [
      { title: "Has both", labels: ["a", "b"] },
      { title: "Has only a", labels: ["a"] },
    ]);
    const fb = await newFilter(page);
    await fb.getByRole("textbox", { name: "Filter name" }).fill("All a and b");
    await fb.getByRole("button", { name: "Add all-of label" }).click();
    // ChipAdd's popover now stays open across picks, matching the New Task
    // overlay's Labels pill (same underlying LabelMenu widget) — add both
    // labels in one go, no reopening required.
    await page.getByRole("button", { name: "a", exact: true }).click();
    await page.getByRole("button", { name: "b", exact: true }).click();
    await expect(fb.getByRole("button", { name: "Remove label a" })).toBeVisible();
    await expect(fb.getByRole("button", { name: "Remove label b" })).toBeVisible();

    await fb.getByRole("button", { name: "Save filter" }).click();

    await expect(page.getByRole("heading", { name: "All a and b" })).toBeVisible();
    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "Has both")).toBeVisible();
  });

  test("Text field filters by a substring of title or notes", async ({ page, api }) => {
    await seed(api, [
      { title: "Write report", notes: "include the xyzzy metric" },
      { title: "Unrelated task" },
    ]);
    const fb = await newFilter(page);
    await fb.getByRole("textbox", { name: "Filter name" }).fill("Xyzzy");
    await fb.getByRole("textbox", { name: "Text contains" }).fill("xyzzy");
    await fb.getByRole("button", { name: "Save filter" }).click();

    await expect(page.getByRole("heading", { name: "Xyzzy" })).toBeVisible();
    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "Write report")).toBeVisible();
  });

  test("Due mode 'Has due date' matches only dated tasks", async ({ page, api }) => {
    await seed(api, [{ title: "Dated", due: daysFromNow(2) }, { title: "Undated" }]);
    const fb = await newFilter(page);
    await fb.getByRole("textbox", { name: "Filter name" }).fill("Has due");
    await fb.getByRole("button", { name: "Has due date" }).click();
    await fb.getByRole("button", { name: "Save filter" }).click();

    await expect(page.getByRole("heading", { name: "Has due" })).toBeVisible();
    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "Dated")).toBeVisible();
  });

  test("Due mode 'Within N days' narrows by the Days input", async ({ page, api }) => {
    await seed(api, [
      { title: "Due in 2", due: daysFromNow(2) },
      { title: "Due in 20", due: daysFromNow(20) },
    ]);
    const fb = await newFilter(page);
    await fb.getByRole("textbox", { name: "Filter name" }).fill("Within 5");
    await fb.getByRole("button", { name: "Within N days" }).click();
    await fb.getByRole("spinbutton", { name: "Days" }).fill("5");
    await fb.getByRole("button", { name: "Save filter" }).click();

    await expect(page.getByRole("heading", { name: "Within 5" })).toBeVisible();
    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "Due in 2")).toBeVisible();
  });

  // "Include completed" was removed: filter views run their predicate over
  // the active replica only, which never holds completed tasks (they leave
  // TaskStore on completion), so the control could never have an effect.
  // Guard against it quietly coming back.
  test("does not offer an 'Include completed' control", async ({ page }) => {
    const fb = await newFilter(page);
    await expect(fb.getByText("Include completed")).toHaveCount(0);
  });

  test("the live match-count preview updates as fields change, before saving", async ({ page, api }) => {
    await seed(api, [
      { title: "A", labels: ["urgent"] },
      { title: "B", labels: ["urgent"] },
      { title: "C" },
    ]);
    const fb = await newFilter(page);
    await expect(page.getByTestId("filter-preview")).toHaveText("3 matching");

    await fb.getByRole("button", { name: "Add any-of label" }).click();
    await page.getByRole("button", { name: "urgent", exact: true }).click();

    await expect(page.getByTestId("filter-preview")).toHaveText("2 matching");
  });

  test("editing an existing filter pre-fills its fields and updates in place", async ({ page, api }) => {
    await seed(api, [
      { title: "Tagged A", labels: ["alpha"] },
      { title: "Tagged B", labels: ["beta"] },
    ]);
    let fb = await newFilter(page);
    await fb.getByRole("textbox", { name: "Filter name" }).fill("Editable");
    await fb.getByRole("button", { name: "Add any-of label" }).click();
    await page.getByRole("button", { name: "alpha", exact: true }).click();
    await fb.getByRole("button", { name: "Save filter" }).click();

    await expect(page.getByRole("heading", { name: "Editable" })).toBeVisible();
    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "Tagged A")).toBeVisible();

    // Edit it: swap the any-of label from alpha to beta.
    const section = page.getByTestId("sidebar-section-filters");
    const item = section.getByTestId("saved-filter").filter({ hasText: "Editable" });
    await item.hover();
    await item.getByRole("button", { name: "Edit filter Editable" }).click();

    fb = page.getByTestId("filter-builder");
    await expect(fb).toBeVisible();
    await expect(fb.getByRole("textbox", { name: "Filter name" })).toHaveValue("Editable");
    await expect(fb.getByRole("button", { name: "Remove label alpha" })).toBeVisible(); // pre-filled

    await fb.getByRole("button", { name: "Remove label alpha" }).click();
    await fb.getByRole("button", { name: "Add any-of label" }).click();
    await page.getByRole("button", { name: "beta", exact: true }).click();
    await fb.getByRole("button", { name: "Save filter" }).click();

    await expect(page.getByRole("heading", { name: "Editable" })).toBeVisible();
    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "Tagged B")).toBeVisible();

    // Only one filter still exists — editing didn't create a duplicate.
    await expect(section.getByTestId("saved-filter")).toHaveCount(1);
  });

  test("'Also show matches in' toggles persist through save and pre-fill on edit", async ({
    page,
    api,
  }) => {
    await seed(api, [{ title: "Anything" }]);
    const fb = await newFilter(page);
    await fb.getByRole("textbox", { name: "Filter name" }).fill("Promoter");
    await fb.getByRole("button", { name: "Show matches in today" }).click();
    await fb.getByRole("button", { name: "Show matches in inbox" }).click();
    await fb.getByRole("button", { name: "Save filter" }).click();
    await expect(page.getByRole("heading", { name: "Promoter" })).toBeVisible();

    // Re-open via edit: the two chosen surfaces are pressed, the third isn't.
    const section = page.getByTestId("sidebar-section-filters");
    const item = section.getByTestId("saved-filter").filter({ hasText: "Promoter" });
    await item.hover();
    await item.getByRole("button", { name: "Edit filter Promoter" }).click();

    const fb2 = page.getByTestId("filter-builder");
    await expect(fb2).toBeVisible();
    await expect(fb2.getByRole("button", { name: "Show matches in today" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(fb2.getByRole("button", { name: "Show matches in inbox" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(fb2.getByRole("button", { name: "Show matches in upcoming" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
  });

  test("removing a filter drops it from the sidebar", async ({ page }) => {
    const fb = await newFilter(page);
    await fb.getByRole("textbox", { name: "Filter name" }).fill("Temp filter");
    await fb.getByRole("button", { name: "Save filter" }).click();

    const section = page.getByTestId("sidebar-section-filters");
    await expect(section.getByTestId("saved-filter")).toContainText("Temp filter");

    const item = section.getByTestId("saved-filter").filter({ hasText: "Temp filter" });
    await item.hover();
    await item.getByRole("button", { name: "Remove filter Temp filter" }).click();

    await expect(section.getByTestId("saved-filter")).toHaveCount(0);
  });

  test("Cancel and backdrop click close the builder without saving", async ({ page }) => {
    let fb = await newFilter(page);
    await fb.getByRole("textbox", { name: "Filter name" }).fill("Abandoned");
    await fb.getByRole("button", { name: "Cancel" }).click();
    await expect(fb).toBeHidden();
    await expect(page.getByTestId("sidebar-section-filters").getByTestId("saved-filter")).toHaveCount(0);

    fb = await newFilter(page);
    await fb.getByRole("textbox", { name: "Filter name" }).fill("Abandoned again");
    await fb.click({ position: { x: 5, y: 5 } }); // inside the panel — must not close
    await expect(fb).toBeVisible();
    await page.mouse.click(2, 2); // the backdrop — closes
    await expect(fb).toBeHidden();
    await expect(page.getByTestId("sidebar-section-filters").getByTestId("saved-filter")).toHaveCount(0);
  });

  test("Save is disabled with an empty name; Ctrl/Cmd+Enter saves from any field", async ({ page }) => {
    const fb = await newFilter(page);
    await expect(fb.getByRole("button", { name: "Save filter" })).toBeDisabled();

    await fb.getByRole("textbox", { name: "Filter name" }).fill("Meta save");
    const text = fb.getByRole("textbox", { name: "Text contains" });
    await text.fill("x"); // focus is away from the name field
    await text.press("ControlOrMeta+Enter");

    await expect(fb).toBeHidden();
    await expect(page.getByRole("heading", { name: "Meta save" })).toBeVisible();
  });

  test("a compound filter (source + label) AND-combines, narrower than either alone", async ({
    page,
    api,
  }) => {
    await api.upsertExternal("myfeed", [{ externalRef: "1", title: "Feed item" }], {
      applyLabels: ["review"],
    });
    await seed(api, [{ title: "Local reviewed", labels: ["review"] }]); // same label, wrong source

    const fb = await newFilter(page);
    await fb.getByRole("textbox", { name: "Filter name" }).fill("Myfeed reviews");
    await fb.getByLabel("Source").selectOption("myfeed");
    await fb.getByRole("button", { name: "Add any-of label" }).click();
    await page.getByRole("button", { name: "review", exact: true }).click();

    // Either constraint alone would match 2 tasks; together, only 1.
    await expect(page.getByTestId("filter-preview")).toHaveText("1 matching");

    await fb.getByRole("button", { name: "Save filter" }).click();
    await expect(page.getByRole("heading", { name: "Myfeed reviews" })).toBeVisible();
    await expect(rows(page)).toHaveCount(1);
    await expect(row(page, "Feed item")).toBeVisible();
  });

  // Regression test for the same reported bug as in task-create.spec.ts:
  // FilterBuilder's dialog also closes unconditionally on Escape, so
  // dismissing a nested label-add popover closes the whole builder too.
  test("Escape while the label-add popover is open closes only the popover, not the builder", async ({
    page,
  }) => {
    const fb = await newFilter(page);
    await fb.getByRole("textbox", { name: "Filter name" }).fill("Keep me open");
    await fb.getByRole("button", { name: "Add any-of label" }).click();
    await expect(page.getByPlaceholder("Type a label…")).toBeVisible();

    await page.keyboard.press("Escape");

    await expect(page.getByPlaceholder("Type a label…")).toBeHidden();
    await expect(fb).toBeVisible();
    await expect(fb.getByRole("textbox", { name: "Filter name" })).toHaveValue("Keep me open");
  });
});
