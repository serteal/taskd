import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  clampWidth,
  defaultSidebarCollapsed,
  readDetailWidth,
  readPanelWidth,
  readSidebarCollapsedPref,
  readSidebarWidth,
  writeDetailWidth,
  writePanelWidth,
  writeSidebarWidth,
  DETAIL_WIDTH,
  PANEL_WIDTH,
  SIDEBAR_WIDTH,
} from "./layout";

const mem = new Map<string, string>();
beforeEach(() => {
  mem.clear();
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => (mem.has(k) ? mem.get(k)! : null),
    setItem: (k: string, v: string) => void mem.set(k, v),
    removeItem: (k: string) => void mem.delete(k),
    clear: () => mem.clear(),
  });
});

describe("clampWidth (pure)", () => {
  const wide = 4000; // viewport so wide the vw fraction never binds

  it("returns a value already inside the bounds unchanged", () => {
    expect(clampWidth(250, SIDEBAR_WIDTH, wide)).toBe(250);
    expect(clampWidth(400, DETAIL_WIDTH, wide)).toBe(400);
  });

  it("floors at the px minimum (the legibility floor always wins)", () => {
    expect(clampWidth(10, SIDEBAR_WIDTH, wide)).toBe(SIDEBAR_WIDTH.min);
    expect(clampWidth(-100, DETAIL_WIDTH, wide)).toBe(DETAIL_WIDTH.min);
  });

  it("caps at the px maximum", () => {
    expect(clampWidth(9999, SIDEBAR_WIDTH, wide)).toBe(SIDEBAR_WIDTH.max);
    expect(clampWidth(9999, PANEL_WIDTH, wide)).toBe(PANEL_WIDTH.max);
  });

  it("caps at the viewport fraction when it is the tighter bound", () => {
    // 40vw of 800 = 320, below the 400 px max — so 320 wins.
    expect(clampWidth(500, SIDEBAR_WIDTH, 800)).toBe(320);
    // 50vw of 900 = 450, below the 560 px max.
    expect(clampWidth(600, DETAIL_WIDTH, 900)).toBe(450);
  });

  it("keeps the px min even when the viewport fraction drops below it", () => {
    // 40vw of 300 = 120, under the 180 px floor → floor wins.
    expect(clampWidth(500, SIDEBAR_WIDTH, 300)).toBe(SIDEBAR_WIDTH.min);
  });

  it("rounds to an integer px", () => {
    expect(clampWidth(250.6, SIDEBAR_WIDTH, wide)).toBe(251);
  });
});

describe("sidebar width round-trip", () => {
  it("defaults when unset, then persists and reads back", () => {
    expect(readSidebarWidth()).toBe(SIDEBAR_WIDTH.default);
    writeSidebarWidth(260);
    expect(readSidebarWidth()).toBe(260);
    expect(mem.get("taskd-sidebar-width")).toBe("260");
  });

  it("falls back to the default for a non-numeric or non-positive value", () => {
    mem.set("taskd-sidebar-width", "not-a-number");
    expect(readSidebarWidth()).toBe(SIDEBAR_WIDTH.default);
    mem.set("taskd-sidebar-width", "0");
    expect(readSidebarWidth()).toBe(SIDEBAR_WIDTH.default);
    mem.set("taskd-sidebar-width", "-40");
    expect(readSidebarWidth()).toBe(SIDEBAR_WIDTH.default);
    mem.set("taskd-sidebar-width", "");
    expect(readSidebarWidth()).toBe(SIDEBAR_WIDTH.default);
  });

  it("rounds a fractional width on write", () => {
    writeSidebarWidth(233.7);
    expect(readSidebarWidth()).toBe(234);
  });
});

describe("detail width round-trip", () => {
  it("defaults when unset, then persists and reads back", () => {
    expect(readDetailWidth()).toBe(DETAIL_WIDTH.default);
    writeDetailWidth(420);
    expect(readDetailWidth()).toBe(420);
    expect(mem.get("taskd-detail-width")).toBe("420");
  });
});

describe("per-panel width keys", () => {
  it("stores and reads each panel id independently under its own key", () => {
    // Unset → the caller's fallback (the registered width ?? 300).
    expect(readPanelWidth("day", 300)).toBe(300);
    expect(readPanelWidth("agenda", 260)).toBe(260);

    writePanelWidth("day", 340);
    writePanelWidth("agenda", 500);

    expect(readPanelWidth("day", 300)).toBe(340);
    expect(readPanelWidth("agenda", 260)).toBe(500);
    // Independent keys — one id's value never leaks into another.
    expect(mem.get("taskd-panel-width:day")).toBe("340");
    expect(mem.get("taskd-panel-width:agenda")).toBe("500");
    expect(readPanelWidth("unknown", 300)).toBe(300);
  });

  it("falls back to the given default for an invalid stored value", () => {
    mem.set("taskd-panel-width:day", "garbage");
    expect(readPanelWidth("day", 300)).toBe(300);
  });
});

describe("readSidebarCollapsedPref", () => {
  it("is null when never set, and the boolean when set", () => {
    expect(readSidebarCollapsedPref()).toBeNull();
    mem.set("taskd-sidebar-collapsed", "1");
    expect(readSidebarCollapsedPref()).toBe(true);
    mem.set("taskd-sidebar-collapsed", "0");
    expect(readSidebarCollapsedPref()).toBe(false);
  });
});

describe("defaultSidebarCollapsed", () => {
  it("starts collapsed below 768px and expanded at/above it when no preference is stored", () => {
    expect(defaultSidebarCollapsed(600)).toBe(true);
    expect(defaultSidebarCollapsed(767)).toBe(true);
    expect(defaultSidebarCollapsed(768)).toBe(false);
    expect(defaultSidebarCollapsed(1280)).toBe(false);
  });

  it("a stored preference always wins over the viewport default", () => {
    mem.set("taskd-sidebar-collapsed", "0");
    expect(defaultSidebarCollapsed(500)).toBe(false); // narrow, but pref says expanded
    mem.set("taskd-sidebar-collapsed", "1");
    expect(defaultSidebarCollapsed(1600)).toBe(true); // wide, but pref says collapsed
  });
});
