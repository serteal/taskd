import { endOfDay } from "./format";

// The quick-add grammar, shared spirit with the CLI's --due parser:
//   #label        → label (namespaces welcome: #project:home)
//   p1 p2 p3      → priority labels
//   today | tomorrow | 3d | 2026-07-10 | mon..sun → due date
// Everything else, in order, is the title. The last date token wins.

export interface ParsedQuickAdd {
  title: string;
  labels: string[];
  due?: Date;
}

const PRIORITY = /^p[1-3]$/;
const RELATIVE_DAYS = /^(\d{1,3})d$/;
const ISO_DATE = /^(\d{4})-(\d{2})-(\d{2})$/;
const WEEKDAYS = ["sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"];

export function parseQuickAdd(input: string, now: Date = new Date()): ParsedQuickAdd {
  const words: string[] = [];
  const labels: string[] = [];
  let due: Date | undefined;

  for (const tok of input.trim().split(/\s+/)) {
    if (tok === "") continue;
    if (tok.startsWith("#") && tok.length > 1) {
      labels.push(tok.slice(1));
      continue;
    }
    if (PRIORITY.test(tok)) {
      labels.push(tok);
      continue;
    }
    const d = parseDueWord(tok.toLowerCase(), now);
    if (d) {
      due = d;
      continue;
    }
    words.push(tok);
  }
  return { title: words.join(" "), labels: dedupe(labels), due };
}

function parseDueWord(w: string, now: Date): Date | undefined {
  if (w === "today") return endOfDay(now);
  if (w === "tomorrow") return endOfDay(addDays(now, 1));
  const rel = RELATIVE_DAYS.exec(w);
  if (rel) return endOfDay(addDays(now, parseInt(rel[1], 10)));
  const iso = ISO_DATE.exec(w);
  if (iso) {
    const d = new Date(parseInt(iso[1], 10), parseInt(iso[2], 10) - 1, parseInt(iso[3], 10));
    if (!isNaN(d.getTime())) return endOfDay(d);
  }
  const idx = WEEKDAYS.findIndex((n) => n === w || n.slice(0, 3) === w);
  if (idx >= 0) {
    // Next occurrence, strictly after today.
    let delta = (idx - now.getDay() + 7) % 7;
    if (delta === 0) delta = 7;
    return endOfDay(addDays(now, delta));
  }
  return undefined;
}

function addDays(d: Date, n: number): Date {
  const out = new Date(d);
  out.setDate(out.getDate() + n);
  return out;
}

function dedupe(xs: string[]): string[] {
  return [...new Set(xs)];
}
