import { describe, expect, it } from "vitest";
import { mockApi, makeTask } from "../../../web/testkit/unit";
import extension from "./main";

describe("gcal extension registration", () => {
  it("registers a presenter, the day panel, a command and a token", () => {
    const api = mockApi();
    extension.register(api);
    expect(api.registered.presenters).toHaveLength(1);
    expect(api.registered.panels.map((p) => p.id)).toContain("day");
    expect(api.registered.commands.length).toBeGreaterThan(0);
    expect(api.registered.quickAddTokens.length).toBeGreaterThan(0);
  });

  it("the presenter matches gcal sources and builds a time row", () => {
    const api = mockApi();
    extension.register(api);
    const p = api.registered.presenters[0];

    expect(p.match(makeTask({ source: "gcal:personal" }))).toBe(true);
    expect(p.match(makeTask({ source: "github" }))).toBe(false);

    const timed = p.rowMeta(
      makeTask({
        source: "gcal:personal",
        externalData: { start: "2026-07-06T09:00:00Z", end: "2026-07-06T10:00:00Z" },
      }),
    );
    expect(timed.icon).toEqual({ __icon: "calendar" }); // uses the core icon
    expect(timed.timeText).toMatch(/\d/); // a rendered time range

    const allDay = p.rowMeta(makeTask({ source: "gcal:personal", externalData: { all_day: true } }));
    expect(allDay.timeText).toBe("all day");
  });

  it("the noon token schedules for 12:00", () => {
    const api = mockApi();
    extension.register(api);
    const tok = api.registered.quickAddTokens[0];
    expect(tok.match("noon")?.due?.getHours()).toBe(12);
    expect(tok.match("dinner")).toBeNull();
  });
});
