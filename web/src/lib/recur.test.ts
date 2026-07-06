import { describe, expect, it } from "vitest";
import { fromNatural, humanize, parseRule } from "./recur";

// The fromNatural grammar table is copied verbatim from the Go mirror
// (internal/recur/recur_test.go: TestFromNatural / TestFromNaturalRejects) so
// the two implementations can never drift.

describe("fromNatural — accepted phrases (mirrors Go TestFromNatural)", () => {
  const table: [string, string][] = [
    ["every day", "FREQ=DAILY"],
    ["daily", "FREQ=DAILY"],
    ["Daily", "FREQ=DAILY"],
    ["every week", "FREQ=WEEKLY"],
    ["weekly", "FREQ=WEEKLY"],
    ["every month", "FREQ=MONTHLY"],
    ["monthly", "FREQ=MONTHLY"],
    ["every year", "FREQ=YEARLY"],
    ["yearly", "FREQ=YEARLY"],
    ["every weekday", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"],
    ["weekday", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"],
    ["every 2 days", "FREQ=DAILY;INTERVAL=2"],
    ["2 weeks", "FREQ=WEEKLY;INTERVAL=2"],
    ["every 3 months", "FREQ=MONTHLY;INTERVAL=3"],
    ["every 1 year", "FREQ=YEARLY"],
    ["every 10 years", "FREQ=YEARLY;INTERVAL=10"],
    ["mon,wed,fri", "FREQ=WEEKLY;BYDAY=MO,WE,FR"],
    ["every monday, wednesday", "FREQ=WEEKLY;BYDAY=MO,WE"],
    ["every Tue,Thu", "FREQ=WEEKLY;BYDAY=TU,TH"],
    ["sat,sun", "FREQ=WEEKLY;BYDAY=SA,SU"],
    ["thursday", "FREQ=WEEKLY;BYDAY=TH"],
  ];
  it.each(table)("fromNatural(%j) === %j", (input, want) => {
    expect(fromNatural(input)).toBe(want);
    // Everything it emits must parse back.
    expect(parseRule(want)).not.toBeNull();
  });
});

describe("fromNatural — rejected phrases (mirrors Go TestFromNaturalRejects)", () => {
  const rejects = [
    "",
    "   ",
    "every",
    "sometimes",
    "every fortnight",
    "every 0 days",
    "tu", // < 3 letters
    "mon,xyz", // one bad token
    "every -2 weeks",
    "FREQ=DAILY", // canonical, not natural — falls through to reject
  ];
  it.each(rejects)("fromNatural(%j) === null", (input) => {
    expect(fromNatural(input)).toBeNull();
  });
});

describe("parseRule (mirrors Go Parse validity)", () => {
  const valid: [string, { freq: string; interval: number; byDay: string[] }, string][] = [
    ["FREQ=DAILY", { freq: "DAILY", interval: 1, byDay: [] }, "FREQ=DAILY"],
    ["FREQ=WEEKLY", { freq: "WEEKLY", interval: 1, byDay: [] }, "FREQ=WEEKLY"],
    ["FREQ=DAILY;INTERVAL=3", { freq: "DAILY", interval: 3, byDay: [] }, "FREQ=DAILY;INTERVAL=3"],
    // Case-insensitive keys/values; BYDAY sorted MO..SU regardless of input order.
    ["freq=weekly;byday=fr,mo,we", { freq: "WEEKLY", interval: 1, byDay: ["MO", "WE", "FR"] }, "FREQ=WEEKLY;BYDAY=MO,WE,FR"],
    ["FREQ=WEEKLY;INTERVAL=2;BYDAY=SA,SU", { freq: "WEEKLY", interval: 2, byDay: ["SA", "SU"] }, "FREQ=WEEKLY;INTERVAL=2;BYDAY=SA,SU"],
    ["FREQ=WEEKLY;BYDAY=MO,MO", { freq: "WEEKLY", interval: 1, byDay: ["MO"] }, "FREQ=WEEKLY;BYDAY=MO"],
  ];
  it.each(valid)("parseRule(%j) parses", (rule, want) => {
    const r = parseRule(rule);
    expect(r).not.toBeNull();
    expect(r!.freq).toBe(want.freq);
    expect(r!.interval).toBe(want.interval);
    expect(r!.byDay).toEqual(want.byDay);
  });

  const invalid = [
    "",
    "   ",
    "INTERVAL=2", // no FREQ
    "FREQ=HOURLY", // unsupported freq
    "FREQ=DAILY;INTERVAL=0", // interval < 1
    "FREQ=DAILY;INTERVAL=-1", // negative
    "FREQ=DAILY;INTERVAL=two", // not a number
    "FREQ=DAILY;BYDAY=MO", // BYDAY on non-weekly
    "FREQ=MONTHLY;BYDAY=MO", // BYDAY on non-weekly
    "FREQ=WEEKLY;BYDAY=XX", // bad day code
    "FREQ=WEEKLY;BYDAY=", // empty BYDAY
    "FREQ=DAILY;FREQ=WEEKLY", // duplicate key
    "FREQ=DAILY;COUNT=5", // unsupported key
    "DAILY", // not KEY=VALUE
  ];
  it.each(invalid)("parseRule(%j) === null", (rule) => {
    expect(parseRule(rule)).toBeNull();
  });
});

describe("humanize — friendly, abbreviated copy", () => {
  const table: [string, string][] = [
    ["FREQ=DAILY", "every day"],
    ["FREQ=WEEKLY", "every week"],
    ["FREQ=MONTHLY", "every month"],
    ["FREQ=YEARLY", "every year"],
    ["FREQ=DAILY;INTERVAL=2", "every 2 days"],
    ["FREQ=WEEKLY;INTERVAL=3", "every 3 weeks"],
    ["FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", "every weekday"],
    ["FREQ=WEEKLY;BYDAY=MO,WE,FR", "every Mon, Wed, Fri"],
    ["not a rule", "not a rule"], // unparseable passes through verbatim
  ];
  it.each(table)("humanize(%j) === %j", (rule, want) => {
    expect(humanize(rule)).toBe(want);
  });

  it("round-trips fromNatural output back to a friendly phrase", () => {
    expect(humanize(fromNatural("every 2 weeks")!)).toBe("every 2 weeks");
    expect(humanize(fromNatural("every mon, wed")!)).toBe("every Mon, Wed");
    expect(humanize(fromNatural("weekdays")!)).toBe("every weekday");
  });
});
