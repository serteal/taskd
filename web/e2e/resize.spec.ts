import { test, expect } from "./fixtures";
import { seed } from "./helpers";
import type { Locator, Page } from "@playwright/test";

// Drag-resizable + persistent side panels: the left sidebar, the detail panel,
// and the extension panel wrappers (the calendar rail). Each handle is an ARIA
// vertical separator; drags use mouse.move/down/move/up over its box.

async function widthOf(loc: Locator): Promise<number> {
  const box = await loc.boundingBox();
  if (!box) throw new Error("element has no bounding box");
  return box.width;
}

// Drag a handle by `dx` px along the x-axis (pointerdown → move → up).
async function dragHandle(page: Page, handle: Locator, dx: number): Promise<void> {
  const box = await handle.boundingBox();
  if (!box) throw new Error("handle has no bounding box");
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;
  await page.mouse.move(cx, cy);
  await page.mouse.down();
  await page.mouse.move(cx + dx, cy, { steps: 20 });
  await page.mouse.up();
}

test.describe("resizable panels", () => {
  test("the left sidebar resizes by dragging its handle, persists across reload, and double-click resets", async ({
    page,
    api,
  }) => {
    await seed(api, [{ title: "Anchor" }]);
    const sidebar = page.getByTestId("sidebar");
    const handle = page.getByRole("separator", { name: "Resize sidebar" });

    const start = await widthOf(sidebar); // the default (208)
    await dragHandle(page, handle, 80); // right-edge handle: right grows
    const wider = await widthOf(sidebar);
    expect(wider).toBeGreaterThan(start + 50);

    // Persists across a reload.
    await page.reload();
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();
    expect(Math.abs((await widthOf(page.getByTestId("sidebar"))) - wider)).toBeLessThan(4);

    // Double-click resets to the default.
    await page.getByRole("separator", { name: "Resize sidebar" }).dblclick();
    expect(Math.abs((await widthOf(page.getByTestId("sidebar"))) - start)).toBeLessThan(4);
  });

  test("keyboard arrows resize a focused sidebar handle in 16px steps", async ({ page, api }) => {
    await seed(api, [{ title: "Anchor" }]);
    const sidebar = page.getByTestId("sidebar");
    const handle = page.getByRole("separator", { name: "Resize sidebar" });

    const start = await widthOf(sidebar);
    await handle.focus();
    await page.keyboard.press("ArrowRight"); // right-edge handle: right grows
    await page.keyboard.press("ArrowRight");
    await page.keyboard.press("ArrowRight");
    expect(await widthOf(sidebar)).toBeCloseTo(start + 48, 0);

    await page.keyboard.press("ArrowLeft");
    expect(await widthOf(sidebar)).toBeCloseTo(start + 32, 0);
  });

  test("the detail panel resizes by dragging its left-edge handle and persists across reload", async ({
    page,
    api,
  }) => {
    await seed(api, [{ title: "Peek me" }]);
    await page.getByText("Peek me", { exact: true }).click();
    const detail = page.getByTestId("detail-panel");
    await expect(detail).toBeVisible();

    const start = await widthOf(detail); // the default (340)
    // Left-edge handle: dragging LEFT grows the panel.
    await dragHandle(page, page.getByRole("separator", { name: "Resize details panel" }), -80);
    const wider = await widthOf(detail);
    expect(wider).toBeGreaterThan(start + 50);

    // The width persists: reopen the task after a reload and it's still wide.
    await page.reload();
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();
    await page.getByText("Peek me", { exact: true }).click();
    const reopened = page.getByTestId("detail-panel");
    await expect(reopened).toBeVisible();
    expect(Math.abs((await widthOf(reopened)) - wider)).toBeLessThan(4);
  });
});

test.describe("resizable extension panel", () => {
  test.use({ mode: "extensions" });

  test("the calendar rail wrapper resizes by dragging its handle and persists per panel across reload", async ({
    page,
  }) => {
    const wrapper = page.getByTestId("panel-wrapper").filter({ has: page.getByTestId("calendar-rail") });
    await expect(wrapper).toBeVisible(); // defaultOpen

    const start = await widthOf(wrapper); // the registered default (300)
    await dragHandle(page, page.getByRole("separator", { name: "Resize Today panel" }), -80);
    const wider = await widthOf(wrapper);
    expect(wider).toBeGreaterThan(start + 50);

    await page.reload();
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();
    const reloaded = page.getByTestId("panel-wrapper").filter({ has: page.getByTestId("calendar-rail") });
    await expect(reloaded).toBeVisible();
    expect(Math.abs((await widthOf(reloaded)) - wider)).toBeLessThan(4);
  });
});
