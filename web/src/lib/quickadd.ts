import { endOfDay } from "./format";
import { fromNatural } from "./recur";

// The quick-add grammar, shared spirit with the CLI's --due parser (a parallel
// agent implements the identical spec there — keep them in lockstep):
//   #label                    → label (namespaces welcome: #project:home)
//   p1 p2 p3                  → priority labels
//   today | tomorrow          → due date (end of day)
//   Nd | Nw                   → N days / N weeks from today
//   YYYY-MM-DD                → explicit date (strict: no month/day rollover)
//   mon..sun (any unambiguous ≥3-letter prefix, e.g. "tues", "thurs")
//                             → next occurrence, strictly after today
//   next <weekday>            → alias for the bare weekday (same date)
//   in N days|weeks|months|years → relative offset (month/year math clamps:
//                              Jan 31 + 1 month = Feb 28, like the CLI)
//   HH:MM | Ham/pm | H:MMam/pm → time of day
//   daily|weekly|monthly|yearly, every <…> → recurrence (see lib/recur)
// Everything else, in order, is the title. Date and time are tracked
// independently while scanning and merged at the end: the last date wins, the
// last time wins, and a time (with or without a date) yields that clock time
// instead of end-of-day. A bare time already past today still means today.
//
// classifyAt() is the single recognition path, shared by parseQuickAdd (build
// the result) and tokenSpans (locate recognized phrases for highlighting) so
// the two can never drift. It greedily matches the longest phrase (up to 3
// words) at each position before falling back to a single-word token.

export interface ParsedQuickAdd {
  title: string;
  labels: string[];
  due?: Date;
  /** Canonical RRULE subset when a recurrence phrase was recognized. */
  recurrence?: string;
}

/** An extension-contributed token handler (registry.quickAddTokens). */
export interface QuickAddTokenFn {
  match(token: string): { labels?: string[]; due?: Date } | null;
}

const PRIORITY = /^p[1-3]$/;
const RELATIVE_DAYS = /^(\d{1,3})d$/;
const RELATIVE_WEEKS = /^(\d{1,3})w$/;
const ISO_DATE = /^(\d{4})-(\d{2})-(\d{2})$/;
const IN_UNIT = /^(day|days|week|weeks|month|months|year|years)$/;
const TIME_24 = /^(\d{1,2}):(\d{2})$/;
const TIME_AMPM = /^(\d{1,2})(am|pm)$/;
const TIME_AMPM_MIN = /^(\d{1,2}):(\d{2})(am|pm)$/;
const WEEKDAYS = ["sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"];
// Unambiguous single-word recurrence words (never a due token). Bare weekdays
// stay DUE tokens; recurrence over weekdays needs the explicit "every" prefix.
const RECUR_WORDS = new Set(["daily", "weekly", "monthly", "yearly"]);
// Cap the "every …" phrase window; long enough for a spaced weekday list.
const RECUR_MAX_WORDS = 7;

export type TokenKind = "label" | "priority" | "due" | "ext" | "recur";

interface TimeOfDay {
  h: number;
  m: number;
}

interface Classified {
  kind: TokenKind;
  /** How many whitespace-separated words this recognition consumes. */
  len: number;
  labels?: string[];
  /** A calendar date, resolved to end of day. */
  date?: Date;
  /** A time of day, merged with the pending date (or today) at the end. */
  time?: TimeOfDay;
  /** A canonical RRULE subset for a recurrence phrase. */
  recurrence?: string;
}

/** Recognize whatever starts at word index `i` in `words`, preferring the
 *  longest phrase. Returns null when the word begins no known token. */
function classifyAt(
  words: string[],
  i: number,
  now: Date,
  tokens: QuickAddTokenFn[],
): Classified | null {
  // Recurrence phrases are greedy-longest ("every 3 days", "every mon,wed") and
  // start on their own words ("every"/-ly), so they never collide with the due
  // phrases below.
  const rec = matchRecurrence(words, i);
  if (rec) return { kind: "recur", len: rec.len, recurrence: rec.recurrence };
  // Longest first: 3-word "in N days|weeks|months".
  if (i + 2 < words.length) {
    const d = parseInPhrase(words[i], words[i + 1], words[i + 2], now);
    if (d) return { kind: "due", len: 3, date: d };
  }
  // 2-word "next <weekday>".
  if (i + 1 < words.length) {
    const d = parseNextWeekday(words[i], words[i + 1], now);
    if (d) return { kind: "due", len: 2, date: d };
  }
  // Single-word token.
  const one = classifyWord(words[i], now, tokens);
  return one ? { ...one, len: 1 } : null;
}

/** Recognize a recurrence phrase starting at word `i`. A bare "-ly" word is a
 *  one-word rule; an "every …" phrase greedily consumes the longest run of
 *  words that still forms a valid rule (so "every day at 5pm" takes just "every
 *  day"). Anything else — "every" alone, "every thing" — is not a recurrence. */
function matchRecurrence(words: string[], i: number): { len: number; recurrence: string } | null {
  const first = words[i].toLowerCase();
  if (RECUR_WORDS.has(first)) {
    const r = fromNatural(first);
    return r ? { len: 1, recurrence: r } : null;
  }
  if (first !== "every") return null;
  const max = Math.min(words.length, i + RECUR_MAX_WORDS);
  for (let j = max; j >= i + 2; j--) {
    const r = fromNatural(words.slice(i, j).join(" "));
    if (r) return { len: j - i, recurrence: r };
  }
  return null;
}

function classifyWord(
  tok: string,
  now: Date,
  tokens: QuickAddTokenFn[],
): Omit<Classified, "len"> | null {
  if (tok.startsWith("#") && tok.length > 1) return { kind: "label", labels: [tok.slice(1)] };
  if (PRIORITY.test(tok)) return { kind: "priority", labels: [tok] };
  const low = tok.toLowerCase();
  const date = parseDateWord(low, now);
  if (date) return { kind: "due", date };
  const time = parseTimeWord(low);
  if (time) return { kind: "due", time };
  // Extension-contributed tokens get a shot before the word becomes title. A
  // throwing provider is ignored, not fatal to typing.
  for (const p of tokens) {
    let r: { labels?: string[]; due?: Date } | null = null;
    try {
      r = p.match(tok);
    } catch {
      r = null;
    }
    if (r) return { kind: "ext", labels: r.labels, date: r.due };
  }
  return null;
}

interface Word {
  text: string;
  start: number;
  end: number;
}

function tokenize(input: string): Word[] {
  const out: Word[] = [];
  const re = /\S+/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(input))) out.push({ text: m[0], start: m.index, end: m.index + m[0].length });
  return out;
}

interface Segment {
  wi: number; // start word index
  len: number; // words consumed
  start: number; // char offset (start of first word)
  end: number; // char offset (end of last word)
  kind: TokenKind;
  labels?: string[];
  date?: Date;
  time?: TimeOfDay;
  recurrence?: string;
}

/** Walk the words left to right, emitting one Segment per recognized phrase and
 *  skipping unrecognized (title) words. The shared core of both public fns. */
function scan(words: Word[], now: Date, tokens: QuickAddTokenFn[]): Segment[] {
  const texts = words.map((w) => w.text);
  const segs: Segment[] = [];
  let i = 0;
  while (i < words.length) {
    const c = classifyAt(texts, i, now, tokens);
    if (c) {
      segs.push({
        wi: i,
        len: c.len,
        start: words[i].start,
        end: words[i + c.len - 1].end,
        kind: c.kind,
        labels: c.labels,
        date: c.date,
        time: c.time,
        recurrence: c.recurrence,
      });
      i += c.len;
    } else {
      i += 1;
    }
  }
  return segs;
}

export function parseQuickAdd(
  input: string,
  now: Date = new Date(),
  tokens: QuickAddTokenFn[] = [],
): ParsedQuickAdd {
  const words = tokenize(input);
  const segs = scan(words, now, tokens);

  const consumed = new Set<number>();
  const labels: string[] = [];
  let date: Date | undefined; // last date wins
  let time: TimeOfDay | undefined; // last time wins
  let recurrence: string | undefined; // last rule wins
  for (const s of segs) {
    for (let k = 0; k < s.len; k++) consumed.add(s.wi + k);
    if (s.labels) labels.push(...s.labels);
    if (s.date) date = s.date;
    if (s.time) time = s.time;
    if (s.recurrence) recurrence = s.recurrence;
  }

  const title = words
    .filter((_, idx) => !consumed.has(idx))
    .map((w) => w.text)
    .join(" ");

  return { title, labels: dedupe(labels), due: mergeDue(date, time, now), recurrence };
}

/** Combine the pending date and time into the final due, if any. A time takes
 *  the date's day (or today) at that clock; a lone date resolves to end of day. */
function mergeDue(date: Date | undefined, time: TimeOfDay | undefined, now: Date): Date | undefined {
  if (time) {
    const base = date ? new Date(date) : new Date(now);
    base.setHours(time.h, time.m, 0, 0);
    return base;
  }
  return date;
}

export interface TokenSpan {
  start: number;
  end: number;
  kind: TokenKind;
}

/** Locates recognized quick-add tokens (and multi-word phrases) in the raw
 *  input by character offset, for inline highlighting (NewTaskOverlay renders a
 *  styled span per token over an otherwise-transparent textarea). Every token
 *  that WOULD contribute is spanned, even one a later token overrides (e.g. two
 *  date words) — the highlight reflects recognition, not the final value. A
 *  phrase like "in 3 days" is one span across the whole phrase. */
export function tokenSpans(
  input: string,
  now: Date = new Date(),
  tokens: QuickAddTokenFn[] = [],
): TokenSpan[] {
  return scan(tokenize(input), now, tokens).map((s) => ({ start: s.start, end: s.end, kind: s.kind }));
}

// --- date/time word parsing ------------------------------------------------

function parseDateWord(w: string, now: Date): Date | undefined {
  if (w === "today") return endOfDay(now);
  if (w === "tomorrow") return endOfDay(addDays(now, 1));
  const rd = RELATIVE_DAYS.exec(w);
  if (rd) return endOfDay(addDays(now, parseInt(rd[1], 10)));
  const rw = RELATIVE_WEEKS.exec(w);
  if (rw) return endOfDay(addDays(now, parseInt(rw[1], 10) * 7));
  const iso = ISO_DATE.exec(w);
  if (iso) {
    const y = parseInt(iso[1], 10);
    const mo = parseInt(iso[2], 10);
    const da = parseInt(iso[3], 10);
    const d = new Date(y, mo - 1, da);
    // Strict: the constructed date must round-trip to the same y/m/d, so
    // rollovers like 2026-02-31 (→ Mar 3) or 2026-13-01 are rejected — the
    // word stays title text, matching the CLI's strict parse.
    if (d.getFullYear() === y && d.getMonth() === mo - 1 && d.getDate() === da) {
      return endOfDay(d);
    }
  }
  const wd = weekdayIndex(w);
  if (wd >= 0) return endOfDay(addDays(now, nextWeekdayDelta(wd, now)));
  return undefined;
}

/** 24h TimeOfDay from HH:MM, Ham/pm, or H:MMam/pm; undefined if not a time. */
function parseTimeWord(w: string): TimeOfDay | undefined {
  let m = TIME_AMPM_MIN.exec(w);
  if (m) {
    const h = to24(parseInt(m[1], 10), m[3]);
    const min = parseInt(m[2], 10);
    return h !== null && min < 60 ? { h, m: min } : undefined;
  }
  m = TIME_AMPM.exec(w);
  if (m) {
    const h = to24(parseInt(m[1], 10), m[2]);
    return h !== null ? { h, m: 0 } : undefined;
  }
  m = TIME_24.exec(w);
  if (m) {
    const h = parseInt(m[1], 10);
    const min = parseInt(m[2], 10);
    return h < 24 && min < 60 ? { h, m: min } : undefined;
  }
  return undefined;
}

function to24(h12: number, ap: string): number | null {
  if (h12 < 1 || h12 > 12) return null;
  const pm = ap === "pm";
  if (h12 === 12) return pm ? 12 : 0; // 12am = 00:00, 12pm = 12:00
  return pm ? h12 + 12 : h12;
}

function parseNextWeekday(w0: string, w1: string, now: Date): Date | undefined {
  if (w0.toLowerCase() !== "next") return undefined;
  const wd = weekdayIndex(w1.toLowerCase());
  if (wd < 0) return undefined;
  // An ALIAS for the bare weekday: the next occurrence strictly after today.
  // (The former +7 semantics surprised in live testing — "next friday" said
  // on a Monday means this coming Friday, same as plain "friday".)
  return endOfDay(addDays(now, nextWeekdayDelta(wd, now)));
}

function parseInPhrase(w0: string, w1: string, w2: string, now: Date): Date | undefined {
  if (w0.toLowerCase() !== "in") return undefined;
  if (!/^\d{1,4}$/.test(w1)) return undefined;
  const n = parseInt(w1, 10);
  const unit = w2.toLowerCase();
  if (!IN_UNIT.test(unit)) return undefined;
  if (unit.startsWith("day")) return endOfDay(addDays(now, n));
  if (unit.startsWith("week")) return endOfDay(addDays(now, n * 7));
  if (unit.startsWith("month")) return endOfDay(addMonths(now, n));
  return endOfDay(addMonths(now, n * 12)); // year(s) = 12·N months, same clamp
}

/** Weekday names accept any unambiguous prefix of at least three letters
 *  ("tue", "tues", "tuesday") — the same rule the CLI and the recurrence
 *  weekday lists (lib/recur) apply. Three letters already disambiguate all
 *  seven days. Returns the Date#getDay index, or -1. */
function weekdayIndex(w: string): number {
  if (w.length < 3) return -1;
  let idx = -1;
  let found = 0;
  for (let i = 0; i < WEEKDAYS.length; i++) {
    if (WEEKDAYS[i].startsWith(w)) {
      idx = i;
      found++;
    }
  }
  return found === 1 ? idx : -1;
}

/** Days until the next `dow` (0=Sun), strictly after today. */
function nextWeekdayDelta(dow: number, now: Date): number {
  const delta = (dow - now.getDay() + 7) % 7;
  return delta === 0 ? 7 : delta;
}

function addDays(d: Date, n: number): Date {
  const out = new Date(d);
  out.setDate(out.getDate() + n);
  return out;
}

/** Month arithmetic with day CLAMPING, matching the CLI: an overflowing day
 *  pins to the target month's last day (Jan 31 + 1 month = Feb 28; Feb 29 + 12
 *  months = Feb 28) instead of JS setMonth's rollover into the next month.
 *  Years route through here as 12·N months. */
function addMonths(d: Date, n: number): Date {
  const y = d.getFullYear();
  const m = d.getMonth() + n;
  const lastDay = new Date(y, m + 1, 0).getDate(); // day 0 of m+1 = last of m
  const out = new Date(d);
  out.setFullYear(y, m, Math.min(d.getDate(), lastDay));
  return out;
}

function dedupe(xs: string[]): string[] {
  return [...new Set(xs)];
}
