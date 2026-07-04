import { defineExtensionE2E, waitForSources, rows, runCommand, toast } from "../../../web/testkit/e2e";

// Stage github and wait for its mock syncer's first sync before each test.
const { test, expect } = defineExtensionE2E("github", { waitFor: waitForSources("github") });

test.describe("github extension", () => {
  test("issues render with a repo#number subtitle", async ({ page, api }) => {
    // Replace the mock snapshot with one deterministic issue (fullSnapshot).
    await api.upsertExternal(
      "github",
      [
        {
          externalRef: "https://github.com/acme/app/issues/7",
          title: "Login button misaligned",
          externalData: {
            repo: "acme/app",
            number: 7,
            kind: "issue",
            state: "open",
            author: "alice",
            comments: 4,
            body: "It drifts left on Safari.",
          },
        },
      ],
      { applyLabels: ["bug"] },
    );

    const r = rows(page).filter({ hasText: "Login button misaligned" });
    await expect(r).toBeVisible();
    await expect(r).toContainText("acme/app#7"); // presenter subtitle
  });

  test("opening an issue shows the custom detail section", async ({ page, api }) => {
    await api.upsertExternal("github", [
      {
        externalRef: "https://github.com/acme/app/issues/7",
        title: "Login button misaligned",
        externalData: {
          repo: "acme/app",
          number: 7,
          kind: "issue",
          state: "open",
          author: "alice",
          comments: 4,
          body: "It drifts left on Safari.",
        },
      },
    ]);

    await rows(page).filter({ hasText: "Login button misaligned" }).getByText("Login button misaligned").click();
    const detail = page.getByTestId("detail-panel");
    await expect(detail).toContainText("acme/app");
    await expect(detail).toContainText("#7");
    await expect(detail).toContainText("@alice");
    await expect(detail).toContainText("It drifts left on Safari.");
  });

  test("the palette exposes the github count command", async ({ page }) => {
    await runCommand(page, "GitHub: count open issues");
    await expect(toast(page).first()).toBeVisible(); // reports a count
  });
});
