// A TypeScript mirror of the Go internal/recur package: the canonical RRULE
// subset a task's `recurrence` field carries, plus the natural-language grammar
// that maps everyday phrases onto it. Kept in lockstep with
// internal/recur/natural.go and recur.go — the same phrase yields the same
// canonical string on both sides, so a rule typed in the web UI reads back the
// same way the CLI wrote it.
//
// The subset (anything outside it is rejected):
//   FREQ=DAILY|WEEKLY|MONTHLY|YEARLY   (required)
//   INTERVAL=n                          (optional, n>=1; omitted when 1)
//   BYDAY=MO,TU,WE,TH,FR,SA,SU          (optional, WEEKLY only; MO..SU order)

export type Freq = "DAILY" | "WEEKLY" | "MONTHLY" | "YEARLY";

export interface Rule {
  freq: Freq;
  /** Always >= 1. */
  interval: number;
  /** BYDAY codes, sorted MO..SU; empty means every occurrence. */
  byDay: string[];
}

// ISO weekday order Monday..Sunday, for BYDAY sorting and canonical output.
const CODES = ["MO", "TU", "WE", "TH", "FR", "SA", "SU"] as const;
const CODE_INDEX: Record<string, number> = Object.fromEntries(CODES.map((c, i) => [c, i]));

// Full weekday names (Monday-first) paired with their BYDAY codes, for the
// natural-language weekday-list parser.
const WEEKDAY_NAMES: [string, string][] = [
  ["monday", "MO"],
  ["tuesday", "TU"],
  ["wednesday", "WE"],
  ["thursday", "TH"],
  ["friday", "FR"],
  ["saturday", "SA"],
  ["sunday", "SU"],
];

const FREQS: readonly string[] = ["DAILY", "WEEKLY", "MONTHLY", "YEARLY"];

const UNIT_FREQ: Record<string, Freq> = {
  day: "DAILY",
  week: "WEEKLY",
  month: "MONTHLY",
  year: "YEARLY",
};

const UNIT_NAME: Record<Freq, string> = {
  DAILY: "day",
  WEEKLY: "week",
  MONTHLY: "month",
  YEARLY: "year",
};

const ABBREV: Record<string, string> = {
  MO: "Mon",
  TU: "Tue",
  WE: "Wed",
  TH: "Thu",
  FR: "Fri",
  SA: "Sat",
  SU: "Sun",
};

/** Render a rule to canonical form: INTERVAL omitted when 1, BYDAY in MO..SU. */
function ruleString(r: Rule): string {
  let out = `FREQ=${r.freq}`;
  if (r.interval > 1) out += `;INTERVAL=${r.interval}`;
  if (r.byDay.length > 0) out += `;BYDAY=${r.byDay.join(",")}`;
  return out;
}

/** Validate a canonical RRULE-subset string. Case-insensitive keys; INTERVAL
 *  must be an integer >= 1; BYDAY is WEEKLY-only; duplicate or unknown keys are
 *  errors. Returns the parsed rule, or null when malformed. Mirror of Go Parse. */
export function parseRule(rule: string): Rule | null {
  const trimmed = rule.trim();
  if (trimmed === "") return null;
  let freq: Freq | null = null;
  let interval = 1;
  let byDay: string[] = [];
  const seen = new Set<string>();
  for (const part of trimmed.split(";")) {
    const eq = part.indexOf("=");
    if (eq < 0) return null;
    const key = part.slice(0, eq).trim().toUpperCase();
    const val = part.slice(eq + 1).trim();
    if (seen.has(key)) return null;
    seen.add(key);
    switch (key) {
      case "FREQ": {
        const f = val.toUpperCase();
        if (!FREQS.includes(f)) return null;
        freq = f as Freq;
        break;
      }
      case "INTERVAL": {
        if (!/^\d+$/.test(val)) return null;
        const n = parseInt(val, 10);
        if (n < 1) return null;
        interval = n;
        break;
      }
      case "BYDAY": {
        const days = parseByDay(val);
        if (!days) return null;
        byDay = days;
        break;
      }
      default:
        return null;
    }
  }
  if (freq === null) return null;
  if (byDay.length > 0 && freq !== "WEEKLY") return null;
  return { freq, interval, byDay };
}

function parseByDay(s: string): string[] | null {
  const seen = new Set<string>();
  const days: string[] = [];
  for (const raw of s.split(",")) {
    const code = raw.trim().toUpperCase();
    if (!(code in CODE_INDEX)) return null;
    if (!seen.has(code)) {
      seen.add(code);
      days.push(code);
    }
  }
  if (days.length === 0) return null;
  days.sort((a, b) => CODE_INDEX[a] - CODE_INDEX[b]);
  return days;
}

/** Map an everyday phrase to a canonical RRULE, or null if unrecognized. It is
 *  case-insensitive and a leading "every " is optional throughout. Mirror of Go
 *  FromNatural — grammar:
 *    "every day" / "daily"                 -> FREQ=DAILY
 *    "every week" / "weekly"               -> FREQ=WEEKLY
 *    "every month" / "monthly"             -> FREQ=MONTHLY
 *    "every year" / "yearly"               -> FREQ=YEARLY
 *    "every weekday"                       -> FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR
 *    "every N days|weeks|months|years"     -> FREQ=...;INTERVAL=N
 *    "every mon,wed" / "monday, friday"    -> FREQ=WEEKLY;BYDAY=...
 *  Weekday names may be full or any prefix of at least three letters. */
export function fromNatural(s: string): string | null {
  const orig = s.trim();
  if (orig === "") return null;
  let body = orig.toLowerCase();
  if (body.startsWith("every ")) body = body.slice("every ".length).trim();
  if (body === "") return null;

  switch (body) {
    case "day":
    case "daily":
      return "FREQ=DAILY";
    case "week":
    case "weekly":
      return "FREQ=WEEKLY";
    case "month":
    case "monthly":
      return "FREQ=MONTHLY";
    case "year":
    case "yearly":
      return "FREQ=YEARLY";
    case "weekday":
    case "weekdays":
      return "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR";
  }

  // "N days" / "N weeks" / "N months" / "N years".
  const fields = body.split(/\s+/);
  if (fields.length === 2 && /^\d+$/.test(fields[0])) {
    const freq = UNIT_FREQ[fields[1].replace(/s$/, "")];
    if (freq) {
      const n = parseInt(fields[0], 10);
      if (n < 1) return null;
      return ruleString({ freq, interval: n, byDay: [] });
    }
  }

  // A weekday list: "mon,wed,fri" or "monday, friday".
  const days = parseWeekdayList(body);
  if (days) return ruleString({ freq: "WEEKLY", interval: 1, byDay: days });

  return null;
}

function parseWeekdayList(s: string): string[] | null {
  const seen = new Set<string>();
  const days: string[] = [];
  for (const raw of s.split(",")) {
    const code = parseWeekdayName(raw.trim());
    if (!code) return null;
    if (!seen.has(code)) {
      seen.add(code);
      days.push(code);
    }
  }
  if (days.length === 0) return null;
  days.sort((a, b) => CODE_INDEX[a] - CODE_INDEX[b]);
  return days;
}

/** Resolve a full weekday name or a prefix of at least three letters to its
 *  BYDAY code (three letters disambiguates all seven days). */
function parseWeekdayName(tok: string): string | null {
  if (tok.length < 3) return null;
  let match: string | null = null;
  let found = 0;
  for (const [name, code] of WEEKDAY_NAMES) {
    if (name.startsWith(tok)) {
      match = code;
      found++;
    }
  }
  return found === 1 ? match : null;
}

/** Render a canonical rule as short, friendly English ("every 2 weeks",
 *  "every Mon, Wed"), or the rule verbatim if it does not parse. Weekday lists
 *  use three-letter abbreviations; a full Mon–Fri set reads "every weekday". */
export function humanize(rule: string): string {
  const r = parseRule(rule);
  if (!r) return rule;
  if (r.freq === "WEEKLY" && r.byDay.length > 0) {
    if (isWeekdaySet(r.byDay)) return "every weekday";
    return "every " + r.byDay.map((c) => ABBREV[c]).join(", ");
  }
  if (r.interval === 1) return `every ${UNIT_NAME[r.freq]}`;
  return `every ${r.interval} ${UNIT_NAME[r.freq]}s`;
}

function isWeekdaySet(byDay: string[]): boolean {
  const want = ["MO", "TU", "WE", "TH", "FR"];
  return byDay.length === want.length && want.every((c, i) => byDay[i] === c);
}
