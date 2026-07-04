import { afterEach, describe, expect, it, vi } from "vitest";
import { notify } from "./notify";

afterEach(() => {
  for (const t of notify.getSnapshot().slice()) notify.dismiss(t.id);
  vi.useRealTimers();
});

describe("notify", () => {
  it("adds and dismisses toasts", () => {
    const id = notify.toast({ message: "hi" });
    expect(notify.getSnapshot().map((t) => t.message)).toContain("hi");
    notify.dismiss(id);
    expect(notify.getSnapshot().some((t) => t.id === id)).toBe(false);
  });

  it("notifies subscribers on change", () => {
    const fn = vi.fn();
    const off = notify.subscribe(fn);
    notify.toast({ message: "x" });
    expect(fn).toHaveBeenCalled();
    off();
  });

  it("error() is an error-kind toast", () => {
    const id = notify.error("boom");
    expect(notify.getSnapshot().find((t) => t.id === id)?.kind).toBe("error");
  });

  it("auto-dismisses after the default duration", () => {
    vi.useFakeTimers();
    const id = notify.toast({ message: "bye" });
    expect(notify.getSnapshot().some((t) => t.id === id)).toBe(true);
    vi.advanceTimersByTime(4000);
    expect(notify.getSnapshot().some((t) => t.id === id)).toBe(false);
  });

  it("keeps toasts with an action on screen longer", () => {
    vi.useFakeTimers();
    const id = notify.toast({ message: "undo me", action: { label: "Undo", onClick: () => {} } });
    vi.advanceTimersByTime(4000);
    expect(notify.getSnapshot().some((t) => t.id === id)).toBe(true); // still there at 4s
    vi.advanceTimersByTime(3000);
    expect(notify.getSnapshot().some((t) => t.id === id)).toBe(false); // gone by 7s
  });

  it("duration <= 0 keeps a toast until dismissed", () => {
    vi.useFakeTimers();
    const id = notify.toast({ message: "sticky", duration: 0 });
    vi.advanceTimersByTime(100000);
    expect(notify.getSnapshot().some((t) => t.id === id)).toBe(true);
  });
});
