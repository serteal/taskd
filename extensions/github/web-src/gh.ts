// Small readers over a github task's source-owned external_data, which is
// opaque JSON to the host. Every value is validated on the way out so the
// presenter and detail section never assume a shape the syncer didn't send.

import type { Task } from "@taskd/extension-api";

export interface GhData {
  repo: string;
  number?: number;
  kind: "issue" | "pr";
  state: string; // "open" | "closed" | "merged" | "draft"
  author: string;
  url: string;
  comments: number;
  additions?: number;
  deletions?: number;
  body: string;
}

function str(v: unknown, fallback = ""): string {
  return typeof v === "string" ? v : fallback;
}

function num(v: unknown): number | undefined {
  return typeof v === "number" ? v : undefined;
}

export function ghData(task: Task): GhData {
  const d = (task.externalData ?? {}) as Record<string, unknown>;
  return {
    repo: str(d.repo),
    number: num(d.number),
    kind: str(d.kind) === "pr" ? "pr" : "issue",
    state: str(d.state, "open"),
    author: str(d.author),
    url: task.externalRef || str(d.url),
    comments: num(d.comments) ?? 0,
    additions: num(d.additions),
    deletions: num(d.deletions),
    body: str(d.body),
  };
}

// GitHub's own semantics: green open, purple merged, red closed, grey draft.
// Themed vars where one fits (accent/warn/muted); a literal purple for merged
// since the palette has no equivalent.
export function stateColor(state: string): string {
  switch (state) {
    case "merged":
      return "#8957e5";
    case "closed":
      return "var(--warn)";
    case "draft":
      return "var(--muted)";
    default:
      return "var(--accent)"; // open
  }
}
