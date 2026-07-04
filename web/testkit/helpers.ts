import { expect, type Page, type Locator } from "@playwright/test";
import type { Api, Task } from "./daemon";

// --- seeding ---------------------------------------------------------------

/** Create tasks sequentially (so createTime ordering is deterministic) and
 *  return them. Tasks arrive in the open app via the live watch stream. */
export async function seed(api: Api, items: Parameters<Api["createTask"]>[0][]): Promise<Task[]> {
  const out: Task[] = [];
  for (const it of items) out.push(await api.createTask(it));
  return out;
}

// --- locators --------------------------------------------------------------

export const rows = (page: Page): Locator => page.locator("[data-task-row]");
// Match the row whose title element is EXACTLY `title`. (A `hasText` filter is
// case-insensitive and substring, so short titles like "A" collide with row
// chrome such as "+ date".) For presenter rows that fold a subtitle into the
// title span, filter on rows() by substring instead.
export const row = (page: Page, title: string): Locator =>
  page.locator("[data-task-row]").filter({ has: page.getByText(title, { exact: true }) });
export const dialog = (page: Page, name: string): Locator => page.getByRole("dialog", { name });
export const toasts = (page: Page): Locator => page.getByRole("status");

// --- drag and drop ---------------------------------------------------------
//
// Native HTML5 DnD ignores Playwright's mouse simulation, so we dispatch the
// real drag events with one shared DataTransfer — the same mechanism the app
// relies on (a custom mime carries the task id).

/** Drag the draggable element containing `sourceText` onto `targetSelector`,
 *  optionally at a point (used for the calendar time position). */
export async function dragTo(
  page: Page,
  sourceText: string,
  targetSelector: string,
  at?: { clientX?: number; clientY?: number },
): Promise<void> {
  await page.evaluate(
    ({ text, target, at }) => {
      const src = [...document.querySelectorAll<HTMLElement>('[draggable="true"]')].find((e) =>
        (e.textContent || "").includes(text),
      );
      const dst = document.querySelector<HTMLElement>(target);
      if (!src) throw new Error(`drag source not found: ${text}`);
      if (!dst) throw new Error(`drag target not found: ${target}`);
      const dt = new DataTransfer();
      src.dispatchEvent(new DragEvent("dragstart", { dataTransfer: dt, bubbles: true }));
      const r = dst.getBoundingClientRect();
      const o: DragEventInit = {
        dataTransfer: dt,
        bubbles: true,
        cancelable: true,
        clientX: at?.clientX ?? r.left + r.width / 2,
        clientY: at?.clientY ?? r.top + r.height / 2,
      };
      dst.dispatchEvent(new DragEvent("dragenter", o));
      dst.dispatchEvent(new DragEvent("dragover", o));
      dst.dispatchEvent(new DragEvent("drop", o));
      src.dispatchEvent(new DragEvent("dragend", { dataTransfer: dt, bubbles: true }));
    },
    { text: sourceText, target: targetSelector, at },
  );
}

/** Reorder within the list: drop the `from` row onto the `to` row's wrapper,
 *  on its top (default) or bottom half. */
export async function dragReorder(
  page: Page,
  fromTitle: string,
  toTitle: string,
  below = false,
): Promise<void> {
  await page.evaluate(
    ({ from, to, below }) => {
      const byText = (t: string) =>
        [...document.querySelectorAll<HTMLElement>("[data-task-row]")].find((r) =>
          (r.textContent || "").includes(t),
        );
      const src = byText(from);
      const targetRow = byText(to);
      if (!src || !targetRow) throw new Error("reorder rows not found");
      const dst = targetRow.parentElement!; // each row is wrapped in a drop target
      const dt = new DataTransfer();
      src.dispatchEvent(new DragEvent("dragstart", { dataTransfer: dt, bubbles: true }));
      const r = dst.getBoundingClientRect();
      const o: DragEventInit = {
        dataTransfer: dt,
        bubbles: true,
        cancelable: true,
        clientX: r.left + 40,
        clientY: below ? r.bottom - 2 : r.top + 2,
      };
      dst.dispatchEvent(new DragEvent("dragover", o));
      dst.dispatchEvent(new DragEvent("drop", o));
    },
    { from: fromTitle, to: toTitle, below },
  );
}

// --- command palette -------------------------------------------------------

export async function openPalette(page: Page): Promise<void> {
  await page.locator("footer button", { hasText: "⌘K" }).click();
  await expect(dialog(page, "Command palette")).toBeVisible();
}

/** Open the palette, type `query`, and run the first matching command. */
export async function runCommand(page: Page, query: string, exactTitle?: string): Promise<void> {
  await openPalette(page);
  const d = dialog(page, "Command palette");
  await d.getByRole("textbox").fill(query);
  await d.getByRole("button", { name: exactTitle ?? query }).first().click();
}

// --- toasts ----------------------------------------------------------------

export function toast(page: Page, text?: string): Locator {
  const all = toasts(page);
  return text ? all.filter({ hasText: text }) : all;
}

export async function clickUndo(page: Page): Promise<void> {
  await toasts(page).getByRole("button", { name: "Undo" }).first().click();
}
