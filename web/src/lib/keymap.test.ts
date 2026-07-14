import { beforeEach, describe, expect, it } from "vitest";
import {
  ACTIONS,
  addBinding,
  getKeymap,
  isOverridden,
  matchStroke,
  prettyBinding,
  removeBinding,
  resetAction,
  resetAllBindings,
  resolveBinding,
  strokeOf,
  toStoredBinding,
} from "./keymap";

const ev = (key: string, mods: Partial<Record<"ctrl" | "alt" | "meta" | "shift", boolean>> = {}) => ({
  key,
  ctrlKey: mods.ctrl ?? false,
  altKey: mods.alt ?? false,
  metaKey: mods.meta ?? false,
  shiftKey: mods.shift ?? false,
});

beforeEach(() => {
  resetAllBindings();
});

describe("strokeOf", () => {
  it("normalizes plain keys", () => {
    expect(strokeOf(ev("j"))).toBe("j");
    expect(strokeOf(ev(" "))).toBe("space");
    expect(strokeOf(ev("Enter"))).toBe("enter");
  });

  it("keeps a shifted printable as its char", () => {
    expect(strokeOf(ev("?", { shift: true }))).toBe("?");
  });

  it("prefixes modifiers in a stable order and spells shift only when needed", () => {
    expect(strokeOf(ev("j", { ctrl: true }))).toBe("ctrl+j");
    expect(strokeOf(ev("k", { meta: true }))).toBe("meta+k");
    expect(strokeOf(ev("Z", { meta: true, shift: true }))).toBe("meta+shift+z");
    expect(strokeOf(ev("Enter", { shift: true }))).toBe("shift+enter");
  });

  it("returns null for bare modifiers and reserved keys", () => {
    expect(strokeOf(ev("Control", { ctrl: true }))).toBeNull();
    expect(strokeOf(ev("Shift", { shift: true }))).toBeNull();
    expect(strokeOf(ev("Escape"))).toBeNull();
    expect(strokeOf(ev("Tab"))).toBeNull();
  });
});

describe("binding resolution & display", () => {
  it("expands mod+ per platform", () => {
    expect(resolveBinding("mod+k", true)).toBe("meta+k");
    expect(resolveBinding("mod+k", false)).toBe("ctrl+k");
  });

  it("stores the platform primary back as mod+", () => {
    expect(toStoredBinding(["meta+k"], true)).toBe("mod+k");
    expect(toStoredBinding(["ctrl+k"], false)).toBe("mod+k");
    expect(toStoredBinding(["ctrl+j"], true)).toBe("ctrl+j"); // not primary on mac
    expect(toStoredBinding(["g", "i"], true)).toBe("g i");
  });

  it("pretty-prints per platform", () => {
    expect(prettyBinding("mod+k", true)).toBe("⌘k");
    expect(prettyBinding("mod+k", false)).toBe("Ctrl+k");
    expect(prettyBinding("g i", true)).toBe("g then i");
    expect(prettyBinding("space", true)).toBe("Space");
  });
});

describe("matchStroke", () => {
  it("matches single strokes to their actions", () => {
    const m = getKeymap();
    expect(matchStroke(m, "", "q").action).toBe("add-task");
    expect(matchStroke(m, "", "x").action).toBe("complete");
    expect(matchStroke(m, "", "ctrl+n").action).toBe("move-down");
  });

  it("reports a chord prefix, then completes it", () => {
    const m = getKeymap();
    expect(matchStroke(m, "", "g")).toEqual({ prefix: true });
    expect(matchStroke(m, "g", "i").action).toBe("go-inbox");
    expect(matchStroke(m, "g", "u").action).toBe("go-upcoming");
    expect(matchStroke(m, "g", "z")).toEqual({});
  });

  it("honors the allow filter (whileTyping)", () => {
    const m = getKeymap();
    const typingOnly = (a: (typeof ACTIONS)[number]) => a.whileTyping === true;
    expect(matchStroke(m, "", "q", typingOnly).action).toBeUndefined();
    const palette = resolveBinding("mod+k");
    expect(matchStroke(m, "", palette, typingOnly).action).toBe("palette");
  });
});

describe("overrides", () => {
  it("adds and removes bindings, tracking overridden state", () => {
    expect(isOverridden("complete")).toBe(false);
    addBinding("complete", "d");
    expect(getKeymap()["complete"]).toEqual(["x", "d"]);
    expect(isOverridden("complete")).toBe(true);
    removeBinding("complete", "x");
    expect(getKeymap()["complete"]).toEqual(["d"]);
    resetAction("complete");
    expect(getKeymap()["complete"]).toEqual(["x"]);
    expect(isOverridden("complete")).toBe(false);
  });

  it("a binding lives on at most one action — assigning steals it", () => {
    const moved = addBinding("complete", "e"); // "e" is edit-title's default
    expect(moved).toBe("edit-title");
    expect(getKeymap()["complete"]).toContain("e");
    expect(getKeymap()["edit-title"]).not.toContain("e");
    expect(matchStroke(getKeymap(), "", "e").action).toBe("complete");
  });

  it("resetting an action re-claims its defaults from whoever took them", () => {
    addBinding("complete", "e");
    resetAction("edit-title");
    expect(getKeymap()["edit-title"]).toEqual(["e"]);
    expect(getKeymap()["complete"]).not.toContain("e");
  });

  it("adding an existing binding to the same action is a no-op", () => {
    const moved = addBinding("complete", "x");
    expect(moved).toBeUndefined();
    expect(getKeymap()["complete"]).toEqual(["x"]);
  });

  it("chord bindings can be added and matched", () => {
    addBinding("go-completed", "g d");
    expect(matchStroke(getKeymap(), "g", "d").action).toBe("go-completed");
    expect(matchStroke(getKeymap(), "", "g")).toEqual({ prefix: true });
  });
});
