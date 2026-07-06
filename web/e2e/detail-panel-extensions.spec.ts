import { test, expect } from "./fixtures";
import { rows, seed, toasts, clickUndo } from "./helpers";

// DetailPanel coverage focused on extension-contributed sections and
// synced-task behavior. Read fully before writing this file:
//   - web/src/components/DetailPanel.tsx (213 lines)
//   - web/src/components/ExtensionBoundary.tsx
//   - web/src/lib/extensions.ts (registry.presenterFor, buildAPI)
//   - extensions/github/web-src/{main,IssueDetail}.tsx
//   - extensions/gcal/web-src/{main,presenter}.tsx
//
// Synced items are local-by-default hidden from the built-in lists (the
// "sources≠tasks" model); a synced task only appears via its Source sidebar
// item, same convention as extensions.spec.ts / context-menu.spec.ts.

const GH_ISSUE = {
  externalRef: "https://github.com/acme/widgets/issues/42",
  title: "Detail panel test issue",
  externalData: {
    repo: "acme/widgets",
    number: 42,
    kind: "issue",
    state: "open",
    author: "alice",
    comments: 3,
    body: "Body text for detail-panel tests.",
  },
};

const GCAL_EVENT = {
  externalRef: "detail-test-evt",
  title: "Detail panel test event",
  dueTime: "2026-07-08T09:00:00.000Z",
  externalData: {
    start: "2026-07-08T09:00:00Z",
    end: "2026-07-08T10:00:00Z",
    location: "Room 9",
    description: "Sync scoping check",
  },
};

const openSource = (page: import("@playwright/test").Page, label: string) =>
  page.locator(`[data-testid="side-item"][data-label="${label}"]`).click();

// Presenter rows fold title + subtitle into one span (no element's exact text
// equals just the title), so match by substring within the row, same as
// extensions/github/e2e/github.spec.ts and web/e2e/extensions.spec.ts.
const openRow = async (page: import("@playwright/test").Page, title: string) => {
  await rows(page).filter({ hasText: title }).getByText(title).click();
};

test.describe("detail panel — synced task field editability", () => {
  test.use({ mode: "extensions" });

  test("title input is disabled and shows the source-owned title", async ({ page, api }) => {
    await api.upsertExternal("github", [GH_ISSUE]);
    await openSource(page, "github");
    await openRow(page, GH_ISSUE.title);

    const title = page.getByRole("textbox", { name: "Title" });
    await expect(title).toBeDisabled();
    await expect(title).toHaveValue(GH_ISSUE.title);
    await expect(page.getByTestId("detail-panel")).toContainText("Synced from");
  });

  test("labels remain addable and removable on a synced task", async ({ page, api }) => {
    await api.upsertExternal("github", [GH_ISSUE]);
    await openSource(page, "github");
    await openRow(page, GH_ISSUE.title);

    const detail = page.getByTestId("detail-panel");
    const add = detail.getByRole("textbox", { name: "Add label" });
    await add.fill("urgent");
    await add.press("Enter");
    await expect(detail.getByText("urgent")).toBeVisible();
    await expect
      .poll(async () => (await api.listAll()).find((t) => t.title === GH_ISSUE.title)?.labels)
      .toContain("urgent");

    await detail.getByRole("button", { name: "Remove label urgent" }).click();
    await expect(detail.getByText("urgent")).toHaveCount(0);
    await expect
      .poll(async () => (await api.listAll()).find((t) => t.title === GH_ISSUE.title)?.labels ?? [])
      .not.toContain("urgent");
  });

  test("notes remain editable on a synced task, saved on blur", async ({ page, api }) => {
    await api.upsertExternal("github", [GH_ISSUE]);
    await openSource(page, "github");
    await openRow(page, GH_ISSUE.title);

    const notes = page.getByRole("textbox", { name: "Notes" });
    await notes.fill("keep an eye on this");
    await notes.blur();

    await expect
      .poll(async () => (await api.listAll()).find((t) => t.title === GH_ISSUE.title)?.notes)
      .toBe("keep an eye on this");
  });

  test("due date input is disabled with no clear affordance on a synced task", async ({ page, api }) => {
    await api.upsertExternal("gcal:test", [GCAL_EVENT]);
    await openSource(page, "gcal:test");
    await openRow(page, GCAL_EVENT.title);

    const due = page.getByRole("textbox", { name: "Due date" });
    await expect(due).toBeDisabled();
    await expect(due).not.toHaveValue("");
    await expect(page.getByRole("button", { name: "clear" })).toHaveCount(0);
  });

  // The banner says "Title, due date and completion follow the source". The
  // Complete button now honours that: until write-back to sources exists, a
  // client-side complete would just be reverted by the next full-snapshot sync,
  // so the button is disabled (the row checkbox, context-menu item, palette
  // entry and `x` key are guarded the same way). The tooltip explains why.
  test("Complete button is disabled for a synced task and cannot complete it", async ({
    page,
    api,
  }) => {
    await api.upsertExternal("github", [GH_ISSUE]);
    await openSource(page, "github");
    await openRow(page, GH_ISSUE.title);

    const detail = page.getByTestId("detail-panel");
    const complete = detail.getByRole("button", { name: "Complete", exact: true });
    await expect(complete).toBeDisabled();
    await expect(complete).toHaveAttribute("title", "Completion follows the source");

    // The row's own completion checkbox is inert on a synced task too.
    const checkbox = rows(page)
      .filter({ hasText: GH_ISSUE.title })
      .first()
      .getByRole("button", { name: `Complete ${GH_ISSUE.title}` });
    await expect(checkbox).toBeDisabled();

    // It stays active — nothing was completed client-side.
    expect((await api.listAll()).find((t) => t.title === GH_ISSUE.title)?.completedTime).toBeFalsy();
  });

  // FINDING (corrected from an initial assumption that a Source view shows
  // both active and completed items — it does not): Sidebar's "sources" list
  // and every "source"-kind view are built entirely from the ACTIVE-only
  // replica (`snap.tasks` — see Sidebar.tsx's `sourceCounts` and App.tsx's
  // `visibleTasks(snap.tasks.values(), ...)`). A synced task that's already
  // completed therefore has NO sidebar entry for its source at all (if that
  // source has no other active items) and can never be viewed via its Source
  // view. The global Completed archive is the ONLY place it's reachable —
  // with no indication of which source it came from, and no way to open it
  // for detail at all, since CompletedList's rows have no click handler
  // (only a Reopen button).
  test("a completed synced task has no Source sidebar entry and can't be opened from the Completed archive", async ({
    page,
    api,
  }) => {
    const doneIssue = {
      ...GH_ISSUE,
      externalRef: "https://github.com/acme/widgets/issues/43",
      title: "Detail panel done issue",
      completedTime: "2026-07-01T00:00:00.000Z",
    };
    await api.upsertExternal("github", [doneIssue]);

    // No active github items exist, so its Source sidebar entry never
    // appears — even though the synced task is real and in the database.
    await expect(page.locator('[data-testid="side-item"][data-label="github"]')).toHaveCount(0);

    // The only place it's visible is the global Completed archive, mixed in
    // with local completed tasks and with no source indicator.
    await page.locator('[data-testid="side-item"][data-label="Completed"]').click();
    const archiveRow = page.locator("div.group").filter({ hasText: doneIssue.title });
    await expect(archiveRow).toBeVisible();

    // It can't be opened for detail at all from here.
    await archiveRow.getByText(doneIssue.title).click();
    await expect(page.getByTestId("detail-panel")).toHaveCount(0);
  });
});

test.describe("detail panel — footer actions on a local task", () => {
  // Baseline coverage: the footer Complete button had no existing e2e test at
  // all (only the row checkbox / context-menu / bulk-bar completion paths were
  // covered).
  test("Complete button completes a local task, closes the panel, and offers Undo", async ({
    page,
    api,
  }) => {
    await seed(api, [{ title: "Finish me" }]);
    await page.getByText("Finish me", { exact: true }).click();

    const detail = page.getByTestId("detail-panel");
    await detail.getByRole("button", { name: "Complete", exact: true }).click();

    await expect(page.getByTestId("detail-panel")).toBeHidden();
    expect(await api.listActive()).toHaveLength(0);

    // Now goes through lib/actions.ts's completeTask(), the same as every
    // other completion entry point (row checkbox, context-menu, "x" key,
    // bulk-bar) — a toast with an Undo action, not a bare store.update.
    await expect(toasts(page)).toHaveCount(1);
    await clickUndo(page);
    expect((await api.listActive()).map((t) => t.title)).toContain("Finish me");
  });

  // NOTE: DetailPanel's footer also gained a completedTime-gated Reopen
  // branch (mirroring ContextMenu's Complete/Reopen toggle) for consistency
  // and forward-compatibility, but it's currently unreachable through any
  // real user flow: CompletedList's rows have no click handler (see the
  // "can't be opened from the Completed archive" test above), and a
  // completed task is never present in the live replica DetailPanel reads
  // from (TaskStore drops a task the moment it completes) — so there's no
  // path that opens a DetailPanel for an already-completed task to exercise
  // it against. Nothing to test here until some future change adds one.
});

test.describe("detail panel — extension sections stay scoped to their own source", () => {
  test.use({ mode: "extensions" });

  test("opening a github issue renders only the github detail section", async ({ page, api }) => {
    await api.upsertExternal("github", [GH_ISSUE]);
    await openSource(page, "github");
    await openRow(page, GH_ISSUE.title);

    const detail = page.getByTestId("detail-panel");
    // github's IssueDetail: section label "github", author/comments meta, and
    // the "Open on GitHub" link.
    await expect(detail).toContainText("acme/widgets");
    await expect(detail).toContainText("@alice");
    await expect(detail).toContainText("Open on GitHub");
    // gcal's DetailSection must not have leaked in alongside it.
    await expect(detail).not.toContainText("calendar event");
    await expect(detail).not.toContainText("Owned by");
  });

  test("opening a gcal event renders only the calendar detail section", async ({ page, api }) => {
    await api.upsertExternal("gcal:test", [GCAL_EVENT]);
    await openSource(page, "gcal:test");
    await openRow(page, GCAL_EVENT.title);

    const detail = page.getByTestId("detail-panel");
    // gcal's DetailSection: "calendar event" label, when/where/details fields,
    // and the "Owned by <source>" footer line.
    await expect(detail).toContainText("calendar event");
    await expect(detail).toContainText("Room 9");
    await expect(detail).toContainText("Owned by");
    // github's IssueDetail must not have leaked in alongside it.
    await expect(detail).not.toContainText("Open on GitHub");
  });
});

test.describe("detail panel — swapping the open task", () => {
  test("clicking a different row swaps title/notes cleanly (no stale values)", async ({ page, api }) => {
    await seed(api, [
      { title: "Swap A", notes: "notes for A" },
      { title: "Swap B", notes: "notes for B" },
    ]);

    await page.getByText("Swap A", { exact: true }).click();
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("Swap A");
    await expect(page.getByRole("textbox", { name: "Notes" })).toHaveValue("notes for A");

    // Detail panel stays mounted (App.tsx renders it with no `key`, just a new
    // `task` prop) — title/notes only reset via DetailPanel's own
    // `useEffect(() => { setTitle(...); setNotes(...) }, [task])`.
    await page.getByText("Swap B", { exact: true }).click();
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("Swap B");
    await expect(page.getByRole("textbox", { name: "Notes" })).toHaveValue("notes for B");
  });

  // DetailPanel's `newLabel` input state now resets via the same
  // useEffect(() => { ...; setNewLabel("") }, [task]) that already resyncs
  // title/notes — an unsubmitted "+ label" draft for task A no longer
  // survives clicking straight into task B's row.
  test("an unsubmitted '+ label' draft does not leak into the next task after swapping rows", async ({
    page,
    api,
  }) => {
    await seed(api, [{ title: "Swap Label A" }, { title: "Swap Label B" }]);

    await page.getByText("Swap Label A", { exact: true }).click();
    const addLabel = page.getByRole("textbox", { name: "Add label" });
    await addLabel.fill("stale-draft"); // typed but never submitted (no Enter)

    await page.getByText("Swap Label B", { exact: true }).click();
    await expect(page.getByRole("textbox", { name: "Title" })).toHaveValue("Swap Label B");
    await expect(page.getByRole("textbox", { name: "Add label" })).toHaveValue("");

    // It was never persisted anywhere, for either task.
    const all = await api.listAll();
    expect(all.find((t) => t.title === "Swap Label A")?.labels ?? []).not.toContain("stale-draft");
    expect(all.find((t) => t.title === "Swap Label B")?.labels ?? []).not.toContain("stale-draft");
  });
});

test.describe("detail panel — Escape while focused in a field", () => {
  // App.tsx's global keydown handler treats Escape as a no-op whenever the
  // event target is an INPUT/TEXTAREA (the `typing` guard, so Escape doesn't
  // fight normal text editing elsewhere) — DetailPanel now has its own local
  // Escape handler so its "esc" close hint holds regardless of which of its
  // fields has focus.
  test("Escape closes the panel even while focus is inside its own Notes field", async ({
    page,
    api,
  }) => {
    await seed(api, [{ title: "Escape target" }]);
    await page.getByText("Escape target", { exact: true }).click();

    const notes = page.getByRole("textbox", { name: "Notes" });
    await notes.click();
    await notes.press("Escape");
    await expect(page.getByTestId("detail-panel")).toBeHidden();
  });
});

test.describe("detail panel — delete (unified undo UX)", () => {
  // Delete no longer opens a native window.confirm() with a "cannot be undone"
  // warning. It now matches the list/context-menu/palette paths: an optimistic
  // delete with an Undo toast, via lib/actions.deleteTaskWithUndo. If a confirm
  // ever came back, this dialog listener would trip the test instead of hanging.
  test("Delete removes the task with an Undo toast — no confirm dialog — and Undo restores it", async ({
    page,
    api,
  }) => {
    let sawDialog = false;
    page.on("dialog", (d) => {
      sawDialog = true;
      void d.dismiss();
    });

    await seed(api, [{ title: "Deletable" }]);
    await page.getByText("Deletable", { exact: true }).click();
    await page.getByRole("button", { name: "Delete", exact: true }).click();

    // Panel closed, row gone, and a toast with Undo — no native dialog involved.
    await expect(page.getByTestId("detail-panel")).toBeHidden();
    await expect(rows(page)).toHaveCount(0);
    expect(sawDialog).toBe(false);

    const deleted = toasts(page).filter({ hasText: "Deleted" });
    await expect(deleted).toBeVisible();
    await clickUndo(page);

    await expect(rows(page).filter({ hasText: "Deletable" })).toHaveCount(1);
    expect((await api.listActive()).map((t) => t.title)).toContain("Deletable");
  });
});
