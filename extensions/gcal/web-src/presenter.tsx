// The presenter for calendar-event tasks (source "gcal:*"): a compact row
// (📅 + time range) and a detail panel showing when/where/what, read straight
// from external_data. The event is owned by its calendar, so nothing here is
// editable — the panel just reflects the source.

import type { Presenter, RowMeta, Task } from "@taskd/extension-api";
import { MONO, eventInterval, fmtFullDate, fmtRange, fmtTime, isAllDay } from "./util";

function rowMeta(t: Task): RowMeta {
  if (isAllDay(t)) return { icon: "📅", timeText: "all day" };
  const iv = eventInterval(t);
  return { icon: "📅", timeText: iv ? fmtRange(iv.start, iv.end) : undefined };
}

function DetailSection({ task }: { task: Task }) {
  const d = task.externalData ?? {};
  const allDay = isAllDay(task);
  const iv = eventInterval(task);
  const location = typeof d.location === "string" ? d.location : "";
  const description = typeof d.description === "string" ? d.description : "";

  let when = "";
  if (iv) {
    when = allDay
      ? `${fmtFullDate(iv.start)} · all day`
      : `${fmtFullDate(iv.start)} · ${fmtTime(iv.start)}–${fmtTime(iv.end)}`;
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <div
        style={{
          fontFamily: MONO,
          fontSize: 10,
          textTransform: "uppercase",
          letterSpacing: "0.16em",
          color: "var(--faint)",
        }}
      >
        calendar event
      </div>
      <dl style={{ margin: 0, display: "flex", flexDirection: "column", gap: 8 }}>
        <Field label="when" value={when} />
        {location && <Field label="where" value={location} />}
        {description && <Field label="details" value={description} />}
      </dl>
      <div
        style={{
          borderTop: "1px solid var(--line)",
          paddingTop: 8,
          fontSize: 12,
          lineHeight: 1.45,
          color: "var(--muted)",
        }}
      >
        Owned by <span style={{ fontFamily: MONO, color: "var(--ink)" }}>{task.source}</span>. Its
        time and title sync from the calendar; your labels, notes, and timebox stay yours.
      </div>
    </div>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
      <dt
        style={{
          fontFamily: MONO,
          fontSize: 10,
          textTransform: "uppercase",
          letterSpacing: "0.12em",
          color: "var(--faint)",
        }}
      >
        {label}
      </dt>
      <dd style={{ margin: 0, fontSize: 13, lineHeight: 1.4, color: "var(--ink)", whiteSpace: "pre-wrap" }}>
        {value}
      </dd>
    </div>
  );
}

export const calendarPresenter: Presenter = {
  match: (t) => t.source.startsWith("gcal"),
  rowMeta,
  DetailSection,
};
