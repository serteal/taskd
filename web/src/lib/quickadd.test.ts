import { describe, expect, it } from "vitest";
import { parseQuickAdd, tokenSpans, type QuickAddTokenFn } from "./quickadd";

// Friday 2026-07-03, 10:00 local.
const NOW = new Date(2026, 6, 3, 10, 0, 0);

describe("parseQuickAdd", () => {
  it("plain text is all title", () => {
    expect(parseQuickAdd("buy milk and eggs", NOW)).toEqual({
      title: "buy milk and eggs",
      labels: [],
      due: undefined,
    });
  });

  it("extracts #labels and priorities, keeps title order", () => {
    const p = parseQuickAdd("fix the #project:taskd deploy p1 #ops", NOW);
    expect(p.title).toBe("fix the deploy");
    expect(p.labels).toEqual(["project:taskd", "p1", "ops"]);
  });

  it("today and tomorrow resolve to end of day", () => {
    const today = parseQuickAdd("x today", NOW).due!;
    expect(today.getDate()).toBe(3);
    expect(today.getHours()).toBe(23);
    const tomorrow = parseQuickAdd("x tomorrow", NOW).due!;
    expect(tomorrow.getDate()).toBe(4);
  });

  it("relative days and ISO dates", () => {
    expect(parseQuickAdd("x 10d", NOW).due!.getDate()).toBe(13);
    const iso = parseQuickAdd("x 2026-12-24", NOW).due!;
    expect([iso.getFullYear(), iso.getMonth(), iso.getDate()]).toEqual([2026, 11, 24]);
  });

  it("weekdays mean the next occurrence, strictly after today", () => {
    // NOW is a Friday; "fri" must be next Friday, not today.
    expect(parseQuickAdd("x fri", NOW).due!.getDate()).toBe(10);
    expect(parseQuickAdd("x monday", NOW).due!.getDate()).toBe(6);
  });

  it("weekday names accept any unambiguous prefix of ≥3 letters (CLI parity)", () => {
    // The in-between spellings people actually type.
    expect(parseQuickAdd("x tues", NOW).due!.getDate()).toBe(7); // Tue Jul 7
    expect(parseQuickAdd("x thurs", NOW).due!.getDate()).toBe(9); // Thu Jul 9
    expect(parseQuickAdd("x wednes", NOW).due!.getDate()).toBe(8); // Wed Jul 8
    expect(parseQuickAdd("x satur", NOW).due!.getDate()).toBe(4); // Sat Jul 4
    // Under three letters stays title, never a date.
    expect(parseQuickAdd("x tu", NOW).due).toBeUndefined();
    expect(parseQuickAdd("x tu", NOW).title).toBe("x tu");
  });

  it("ISO dates are strict: rollovers and impossible months are rejected", () => {
    // 2026-02-31 would roll over to Mar 3 — reject it; the word stays title.
    const rollover = parseQuickAdd("pay 2026-02-31", NOW);
    expect(rollover.due).toBeUndefined();
    expect(rollover.title).toBe("pay 2026-02-31");
    // Month 13 doesn't exist.
    expect(parseQuickAdd("x 2026-13-01", NOW).due).toBeUndefined();
    // 2026 is not a leap year; 2028 is.
    expect(parseQuickAdd("x 2026-02-29", NOW).due).toBeUndefined();
    const leap = parseQuickAdd("x 2028-02-29", NOW).due!;
    expect([leap.getFullYear(), leap.getMonth(), leap.getDate()]).toEqual([2028, 1, 29]);
  });

  it("last date token wins; p4 is not a priority; dedupes labels", () => {
    const p = parseQuickAdd("ship p4 #a #a today tomorrow", NOW);
    expect(p.title).toBe("ship p4");
    expect(p.labels).toEqual(["a"]);
    expect(p.due!.getDate()).toBe(4);
  });

  it("Nw means N weeks from today", () => {
    expect(parseQuickAdd("x 1w", NOW).due!.getDate()).toBe(10); // Jul 3 + 7
    expect(parseQuickAdd("x 2w", NOW).due!.getDate()).toBe(17); // Jul 3 + 14
  });
});

describe("parseQuickAdd — phrases", () => {
  it("'next <weekday>' is an ALIAS for the bare weekday (next occurrence after today)", () => {
    // Bare "monday" is Jul 6; "next monday" is the SAME date, not +7.
    expect(parseQuickAdd("x next monday", NOW).due!.getDate()).toBe(6);
    expect(parseQuickAdd("x next monday", NOW).due!.getTime()).toBe(
      parseQuickAdd("x monday", NOW).due!.getTime(),
    );
    // Bare "friday" (NOW is Friday) is Jul 10 — strictly after today — and so
    // is "next friday".
    expect(parseQuickAdd("x next friday", NOW).due!.getDate()).toBe(10);
    // Prefix weekdays work inside the phrase too, and equal the bare form.
    expect(parseQuickAdd("x next tues", NOW).due!.getTime()).toBe(
      parseQuickAdd("x tues", NOW).due!.getTime(),
    );
    expect(parseQuickAdd("x next tues", NOW).due!.getDate()).toBe(7);
    // Both words are consumed.
    expect(parseQuickAdd("call next tues", NOW).title).toBe("call");
  });

  it("'in N days|weeks|months|years' offsets from today", () => {
    expect(parseQuickAdd("x in 3 days", NOW).due!.getDate()).toBe(6);
    expect(parseQuickAdd("x in 2 weeks", NOW).due!.getDate()).toBe(17);
    const m = parseQuickAdd("x in 1 month", NOW).due!;
    expect([m.getMonth(), m.getDate()]).toEqual([7, 3]); // Aug 3
    const y = parseQuickAdd("x in 1 year", NOW).due!;
    expect([y.getFullYear(), y.getMonth(), y.getDate()]).toEqual([2027, 6, 3]); // Jul 3 2027
    const y2 = parseQuickAdd("renew passport in 2 years", NOW);
    expect(y2.title).toBe("renew passport");
    expect(y2.due!.getFullYear()).toBe(2028);
  });

  it("month/year math CLAMPS the day instead of rolling over (CLI parity)", () => {
    // Jan 31 + 1 month must be Feb 28 (2026 is not a leap year), not Mar 3.
    const jan31 = new Date(2026, 0, 31, 10, 0, 0);
    const m = parseQuickAdd("x in 1 month", jan31).due!;
    expect([m.getFullYear(), m.getMonth(), m.getDate()]).toEqual([2026, 1, 28]);
    // Feb 29 + 1 year must be Feb 28 of the next year, not Mar 1.
    const feb29 = new Date(2028, 1, 29, 10, 0, 0); // 2028 is a leap year
    const y = parseQuickAdd("x in 1 year", feb29).due!;
    expect([y.getFullYear(), y.getMonth(), y.getDate()]).toEqual([2029, 1, 28]);
    // A non-overflowing day is untouched by the clamp.
    const mar15 = new Date(2026, 2, 15, 10, 0, 0);
    const plain = parseQuickAdd("x in 1 month", mar15).due!;
    expect([plain.getMonth(), plain.getDate()]).toEqual([3, 15]); // Apr 15
  });

  it("a phrase is consumed whole, leaving only the title", () => {
    const p = parseQuickAdd("prep demo in 3 days", NOW);
    expect(p.title).toBe("prep demo");
    expect(p.due!.getDate()).toBe(6);
  });

  it("does NOT eat a natural-language 'in the …' that isn't 'in <number> <unit>'", () => {
    const p = parseQuickAdd("remind me in the morning", NOW);
    expect(p.title).toBe("remind me in the morning");
    expect(p.due).toBeUndefined();
  });

  it("'in a week' (no number) stays title, not a date", () => {
    const p = parseQuickAdd("call back in a week", NOW);
    expect(p.title).toBe("call back in a week");
    expect(p.due).toBeUndefined();
  });
});

describe("parseQuickAdd — time of day", () => {
  it("bare times attach to today at that clock", () => {
    const t24 = parseQuickAdd("standup 09:00", NOW).due!;
    expect([t24.getDate(), t24.getHours(), t24.getMinutes()]).toEqual([3, 9, 0]);
    const pm = parseQuickAdd("gym 3pm", NOW).due!;
    expect([pm.getHours(), pm.getMinutes()]).toEqual([15, 0]);
    const withMin = parseQuickAdd("call 3:30pm", NOW).due!;
    expect([withMin.getHours(), withMin.getMinutes()]).toEqual([15, 30]);
  });

  it("12am is midnight and 12pm is noon", () => {
    expect(parseQuickAdd("x 12am", NOW).due!.getHours()).toBe(0);
    expect(parseQuickAdd("x 12pm", NOW).due!.getHours()).toBe(12);
  });

  it("a time already past today still means today (predictable, not clever)", () => {
    // NOW is 10:00; 9am is in the past but resolves to today.
    const d = parseQuickAdd("late 9am", NOW).due!;
    expect([d.getDate(), d.getHours()]).toEqual([3, 9]);
  });

  it("a date and a time combine, in either order", () => {
    const a = parseQuickAdd("meet tomorrow 09:00", NOW).due!;
    expect([a.getDate(), a.getHours(), a.getMinutes()]).toEqual([4, 9, 0]);
    const b = parseQuickAdd("meet 3pm fri", NOW).due!;
    expect([b.getDate(), b.getHours()]).toEqual([10, 15]);
    const c = parseQuickAdd("meet fri 3pm", NOW).due!;
    expect([c.getDate(), c.getHours()]).toEqual([10, 15]);
  });

  it("last date wins and last time wins, independently", () => {
    const d = parseQuickAdd("x today 3pm tomorrow 4pm", NOW).due!;
    expect([d.getDate(), d.getHours()]).toEqual([4, 16]);
  });

  it("a lone date stays end-of-day; only a time changes the clock", () => {
    const d = parseQuickAdd("x tomorrow", NOW).due!;
    expect([d.getHours(), d.getMinutes(), d.getSeconds()]).toEqual([23, 59, 59]);
  });
});

describe("parseQuickAdd — recurrence phrases", () => {
  it("bare -ly words become a recurrence and leave only the title", () => {
    expect(parseQuickAdd("daily standup", NOW)).toMatchObject({
      title: "standup",
      recurrence: "FREQ=DAILY",
    });
    expect(parseQuickAdd("review weekly", NOW).recurrence).toBe("FREQ=WEEKLY");
    expect(parseQuickAdd("rent monthly", NOW).recurrence).toBe("FREQ=MONTHLY");
    expect(parseQuickAdd("taxes yearly", NOW).recurrence).toBe("FREQ=YEARLY");
  });

  it("'every N units' and 'every weekday' parse as recurrence, consumed whole", () => {
    const a = parseQuickAdd("water plants every 3 days", NOW);
    expect(a.title).toBe("water plants");
    expect(a.recurrence).toBe("FREQ=DAILY;INTERVAL=3");

    const b = parseQuickAdd("standup every weekday", NOW);
    expect(b.title).toBe("standup");
    expect(b.recurrence).toBe("FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR");

    const c = parseQuickAdd("gym every mon,wed,fri", NOW);
    expect(c.title).toBe("gym");
    expect(c.recurrence).toBe("FREQ=WEEKLY;BYDAY=MO,WE,FR");
  });

  it("a recurrence phrase and a due token coexist (the e2e headline case)", () => {
    const p = parseQuickAdd("Water plants every 3 days tomorrow", NOW);
    expect(p.title).toBe("Water plants");
    expect(p.recurrence).toBe("FREQ=DAILY;INTERVAL=3");
    expect(p.due!.getDate()).toBe(4); // NOW is Jul 3 → tomorrow is Jul 4
  });

  it("last rule wins", () => {
    expect(parseQuickAdd("x daily weekly", NOW).recurrence).toBe("FREQ=WEEKLY");
  });

  it("does not eat 'every' alone or a non-rule 'every <word>'", () => {
    expect(parseQuickAdd("clean every thing", NOW)).toMatchObject({
      title: "clean every thing",
      recurrence: undefined,
    });
    expect(parseQuickAdd("say every", NOW)).toMatchObject({
      title: "say every",
      recurrence: undefined,
    });
  });

  it("a bare weekday stays a DUE token, not a recurrence", () => {
    const p = parseQuickAdd("call mom monday", NOW);
    expect(p.recurrence).toBeUndefined();
    expect(p.due).toBeTruthy(); // "monday" resolves to the next Monday
  });
});

describe("tokenSpans", () => {
  it("locates a #label, a priority, and a due word by character offset", () => {
    const input = "fix bug #ops p1 tomorrow";
    const spans = tokenSpans(input, NOW);
    expect(spans).toEqual([
      { start: 8, end: 12, kind: "label" },
      { start: 13, end: 15, kind: "priority" },
      { start: 16, end: 24, kind: "due" },
    ]);
    expect(spans.map((s) => input.slice(s.start, s.end))).toEqual(["#ops", "p1", "tomorrow"]);
  });

  it("spans every recognized word, even one a later token overrides", () => {
    // parseQuickAdd's due ends up as "tomorrow" (last wins), but "today" is
    // still a recognized word and should still be highlighted.
    const spans = tokenSpans("ship today tomorrow", NOW);
    expect(spans.map((s) => s.kind)).toEqual(["due", "due"]);
  });

  it("plain words and p4 (not a valid priority) get no span", () => {
    expect(tokenSpans("just a plain title p4", NOW)).toEqual([]);
  });

  it("an extension-contributed token gets an 'ext' span", () => {
    const urgent: QuickAddTokenFn = { match: (t) => (t === "urgent" ? { labels: ["urgent"] } : null) };
    const spans = tokenSpans("ship it urgent", NOW, [urgent]);
    expect(spans).toEqual([{ start: 8, end: 14, kind: "ext" }]);
  });

  it("a throwing extension token is ignored, not fatal", () => {
    const boom: QuickAddTokenFn = {
      match: () => {
        throw new Error("boom");
      },
    };
    expect(() => tokenSpans("ship it", NOW, [boom])).not.toThrow();
    expect(tokenSpans("ship it", NOW, [boom])).toEqual([]);
  });

  it("a multi-word phrase highlights as ONE span across the whole phrase", () => {
    const input = "ship in 3 days";
    expect(tokenSpans(input, NOW)).toEqual([{ start: 5, end: 14, kind: "due" }]);
    expect(input.slice(5, 14)).toBe("in 3 days");
  });

  it("'next <weekday>' spans both words as one due token", () => {
    const input = "x next friday";
    expect(tokenSpans(input, NOW)).toEqual([{ start: 2, end: 13, kind: "due" }]);
    expect(input.slice(2, 13)).toBe("next friday");
  });

  it("a time token is spanned as 'due'", () => {
    const input = "lunch 3pm";
    expect(tokenSpans(input, NOW)).toEqual([{ start: 6, end: 9, kind: "due" }]);
  });

  it("a non-phrase 'in the …' produces no spans", () => {
    expect(tokenSpans("in the morning", NOW)).toEqual([]);
  });

  it("a recurrence phrase spans as one 'recur' token", () => {
    const input = "water every 3 days";
    expect(tokenSpans(input, NOW)).toEqual([{ start: 6, end: 18, kind: "recur" }]);
    expect(input.slice(6, 18)).toBe("every 3 days");
  });

  it("a bare -ly word gets a single 'recur' span", () => {
    expect(tokenSpans("daily standup", NOW)).toEqual([{ start: 0, end: 5, kind: "recur" }]);
  });

  it("'every' alone or 'every thing' produce no recurrence span", () => {
    expect(tokenSpans("every", NOW)).toEqual([]);
    expect(tokenSpans("clean every thing", NOW)).toEqual([]);
  });
});
