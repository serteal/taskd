import { describe, expect, it } from "vitest";
import { mockApi, makeTask } from "../../../web/testkit/unit";
import extension from "./main";

describe("github extension registration", () => {
  it("registers a presenter and the count command", () => {
    const api = mockApi();
    extension.register(api);
    expect(api.registered.presenters).toHaveLength(1);
    expect(api.registered.commands.map((c) => c.title)).toContain(
      "GitHub: count open issues & PRs",
    );
  });

  it("the presenter builds a repo#number subtitle and kind/state chips", () => {
    const api = mockApi();
    extension.register(api);
    const p = api.registered.presenters[0];

    expect(p.match(makeTask({ source: "github" }))).toBe(true);
    expect(p.match(makeTask({ source: "gcal:x" }))).toBe(false);

    const meta = p.rowMeta(
      makeTask({
        source: "github",
        externalData: { repo: "acme/app", number: 7, kind: "pr", state: "open" },
      }),
    );
    expect(meta.subtitle).toBe("acme/app#7");
    expect(meta.extraChips).toEqual(["PR", "open"]);
  });

  it("the count command reports open items via a toast", () => {
    const api = mockApi({
      tasks: [
        makeTask({ source: "github" }),
        makeTask({ source: "github", completedTime: "2026-07-01T00:00:00Z" }),
        makeTask({ source: "gcal:x" }),
      ],
    });
    extension.register(api);
    const cmd = api.registered.commands.find((c) => c.id === "open-count");
    cmd.run();
    // one open github item (the completed one and the gcal one don't count)
    expect(api.notify.toast.calls[0][0]).toMatchObject({ message: "1 open GitHub item" });
  });
});
