import { describe, expect, it } from "vitest";
import type { Task } from "@taskd/extension-api";
import { ghData, stateColor } from "./gh";

const task = (externalData: unknown, externalRef = ""): Task =>
  ({ externalData, externalRef }) as unknown as Task;

describe("ghData", () => {
  it("reads a well-formed issue/PR", () => {
    const d = ghData(
      task(
        {
          repo: "taskd/core",
          number: 12,
          kind: "pr",
          state: "merged",
          author: "octocat",
          comments: 3,
          additions: 40,
          deletions: 5,
          body: "hello",
        },
        "https://github.com/taskd/core/pull/12",
      ),
    );
    expect(d).toMatchObject({
      repo: "taskd/core",
      number: 12,
      kind: "pr",
      state: "merged",
      author: "octocat",
      comments: 3,
      additions: 40,
      deletions: 5,
      body: "hello",
      url: "https://github.com/taskd/core/pull/12",
    });
  });

  it("applies safe defaults for missing/odd fields", () => {
    const d = ghData(task({ number: "nope", kind: "whatever" }));
    expect(d.repo).toBe("");
    expect(d.number).toBeUndefined(); // non-number ignored
    expect(d.kind).toBe("issue"); // anything but "pr"
    expect(d.state).toBe("open"); // default
    expect(d.comments).toBe(0);
  });

  it("prefers external_ref for the url, falling back to data.url", () => {
    expect(ghData(task({ url: "d-url" })).url).toBe("d-url");
    expect(ghData(task({ url: "d-url" }, "ref-url")).url).toBe("ref-url");
  });
});

describe("stateColor", () => {
  it("maps states to colors", () => {
    expect(stateColor("merged")).toBe("#8957e5");
    expect(stateColor("closed")).toBe("var(--warn)");
    expect(stateColor("draft")).toBe("var(--muted)");
    expect(stateColor("open")).toBe("var(--accent)");
    expect(stateColor("anything")).toBe("var(--accent)");
  });
});
