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

  it("the mention provider offers events, upcoming first", () => {
    // The provider splits upcoming/past against the wall clock, so build the
    // fixture relative to the real now.
    const at = (days: number) => new Date(Date.now() + days * 86_400_000).toISOString();
    const ev = (id: string, title: string, start: string) =>
      makeTask({ id, title, source: "gcal:personal", externalData: { start } } as never);
    const api = mockApi({
      tasks: [
        ev("past", "Old sync", at(-5)),
        ev("soon", "Dentist", at(1)),
        ev("later", "Flight", at(10)),
        makeTask({ id: "local", title: "Dentist notes" }), // not gcal → excluded
      ],
    });
    extension.register(api);
    const provider = api.registered.mentionProviders[0];
    expect(provider.id).toBe("gcal");

    const all = provider.search("");
    expect(all.map((i: { ref: string }) => i.ref)).toEqual(["task:soon", "task:later", "task:past"]);
    expect(all[0].title).toBe("Dentist");

    const filtered = provider.search("dent");
    expect(filtered.map((i: { title: string }) => i.title)).toEqual(["Dentist"]);
  });

  it("the noon token schedules for 12:00", () => {
    const api = mockApi();
    extension.register(api);
    const tok = api.registered.quickAddTokens[0];
    expect(tok.match("noon")?.due?.getHours()).toBe(12);
    expect(tok.match("dinner")).toBeNull();
  });
});
