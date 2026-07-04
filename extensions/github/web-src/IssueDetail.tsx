// The github detail section, rendered inside the host's detail panel for any
// task with source "github". Styled with inline styles over the host's CSS
// custom properties so it stays theme-aware: mono for ids/numbers, the
// inherited sans for prose. Colors follow GitHub's state semantics.

import type { CSSProperties } from "react";
import type { ExtensionAPI, Task } from "@taskd/extension-api";
import { ghData, stateColor } from "./gh";

const mono = '"IBM Plex Mono", ui-monospace, SFMono-Regular, Menlo, monospace';
const addColor = "#3fb950"; // additions (green, readable on both themes)
const delColor = "#f85149"; // deletions (red)

const sectionLabel: CSSProperties = {
  fontFamily: mono,
  fontSize: 10,
  textTransform: "uppercase",
  letterSpacing: "0.16em",
  color: "var(--faint)",
};

const metaKey: CSSProperties = {
  fontFamily: mono,
  fontSize: 10,
  textTransform: "uppercase",
  letterSpacing: "0.14em",
  color: "var(--faint)",
  width: 68,
  flexShrink: 0,
};

const metaVal: CSSProperties = {
  fontFamily: mono,
  fontSize: 12,
  color: "var(--muted)",
  minWidth: 0,
  wordBreak: "break-word",
};

function MetaRow({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div style={{ display: "flex", gap: 8, alignItems: "baseline" }}>
      <span style={metaKey}>{k}</span>
      <span style={metaVal}>{children}</span>
    </div>
  );
}

export function IssueDetail({ task }: { task: Task; api: ExtensionAPI }) {
  const d = ghData(task);
  const color = stateColor(d.state);
  const kindLabel = d.kind === "pr" ? "Pull request" : "Issue";
  const isPR = d.kind === "pr";

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
      <div style={sectionLabel}>github</div>

      {/* header: colored kind + state badge, then repo#number */}
      <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        <span
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: 6,
            border: `1px solid ${color}`,
            color,
            borderRadius: 999,
            padding: "1px 9px",
            fontSize: 12,
            fontWeight: 600,
          }}
        >
          <span
            aria-hidden
            style={{
              width: 7,
              height: 7,
              borderRadius: 999,
              background: color,
              display: "inline-block",
            }}
          />
          {kindLabel} · {d.state}
        </span>
        {d.repo && (
          <span style={{ fontFamily: mono, fontSize: 13, color: "var(--ink)" }}>
            {d.repo}
            {d.number != null && <span style={{ color: "var(--muted)" }}>#{d.number}</span>}
          </span>
        )}
      </div>

      {/* meta */}
      <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        <MetaRow k="author">{d.author ? `@${d.author}` : "—"}</MetaRow>
        <MetaRow k="comments">{d.comments}</MetaRow>
        {isPR && (d.additions != null || d.deletions != null) && (
          <MetaRow k="diff">
            <span style={{ color: addColor }}>+{d.additions ?? 0}</span>{" "}
            <span style={{ color: delColor }}>−{d.deletions ?? 0}</span>
          </MetaRow>
        )}
      </div>

      {/* body (prose, sans) */}
      {d.body && (
        <div
          style={{
            border: "1px solid var(--line)",
            background: "var(--bg)",
            borderRadius: 6,
            padding: "8px 10px",
            fontSize: 13,
            lineHeight: 1.5,
            color: "var(--muted)",
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
          }}
        >
          {d.body}
        </div>
      )}

      {/* prominent link out */}
      {d.url && (
        <a
          href={d.url}
          target="_blank"
          rel="noreferrer"
          style={{
            alignSelf: "flex-start",
            border: "1px solid color-mix(in srgb, var(--accent) 50%, transparent)",
            color: "var(--accent)",
            borderRadius: 6,
            padding: "5px 11px",
            fontSize: 12,
            fontWeight: 500,
            textDecoration: "none",
          }}
        >
          Open on GitHub ↗
        </a>
      )}
    </div>
  );
}
