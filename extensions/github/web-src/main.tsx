// The github web extension: one presenter, no custom view. It gives every
// task with source "github" a state icon, a "<repo>#<number>" subtitle (which
// replaces the host's generic source badge), kind/state chips, and the rich
// detail section in IssueDetail.tsx.

import type { ExtensionAPI, RowMeta, Task, TaskdExtension } from "@taskd/extension-api";
import { ghData } from "./gh";
import { IssueDetail } from "./IssueDetail";

// One clear, consistent marker per state: green open, purple merged, red
// closed, hollow draft. Mirrors GitHub's own color language.
const stateIcon: Record<string, string> = {
  open: "🟢",
  merged: "🟣",
  closed: "🔴",
  draft: "⚪",
};

const extension: TaskdExtension = {
  name: "github",
  register(api: ExtensionAPI) {
    api.registerPresenter({
      match: (t: Task) => t.source === "github",
      rowMeta(t: Task): RowMeta {
        const d = ghData(t);
        const subtitle =
          d.repo && d.number != null ? `${d.repo}#${d.number}` : d.repo || undefined;
        return {
          icon: stateIcon[d.state] ?? "🟢",
          subtitle,
          extraChips: [d.kind === "pr" ? "PR" : "issue", d.state],
        };
      },
      DetailSection: IssueDetail,
    });
  },
};

export default extension;
