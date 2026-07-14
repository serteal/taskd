import { describe, expect, it } from "vitest";
import {
  isMentionURL,
  mentionMarkup,
  mentionTaskId,
  parseMentions,
  stripMentions,
} from "./mentions";
import { parseQuickAdd, tokenSpans } from "./quickadd";

describe("mention markup", () => {
  it("round-trips through markup → parse", () => {
    const text = `call about ${mentionMarkup({ title: "Paula a Londres", ref: "task:abc-123" })} tomorrow`;
    const ms = parseMentions(text);
    expect(ms).toHaveLength(1);
    expect(ms[0].title).toBe("Paula a Londres");
    expect(ms[0].ref).toBe("task:abc-123");
    expect(text.slice(ms[0].start, ms[0].end)).toBe("@[Paula a Londres](task:abc-123)");
  });

  it("strips structural brackets from titles when building markup", () => {
    expect(mentionMarkup({ title: "a [b] c", ref: "task:x" })).toBe("@[a b c](task:x)");
  });

  it("parses several mentions and leaves surrounding text intact", () => {
    const text = "@[One](task:1) and @[Two](https://doc.example/2)";
    const ms = parseMentions(text);
    expect(ms.map((m) => m.ref)).toEqual(["task:1", "https://doc.example/2"]);
    expect(stripMentions(text)).toBe("@One and @Two");
  });

  it("classifies refs", () => {
    expect(mentionTaskId("task:xyz")).toBe("xyz");
    expect(mentionTaskId("https://x")).toBeUndefined();
    expect(isMentionURL("https://doc.example/2")).toBe(true);
    expect(isMentionURL("task:xyz")).toBe(false);
  });

  it("ignores malformed markup", () => {
    expect(parseMentions("@[unclosed](task:")).toHaveLength(0);
    expect(parseMentions("plain @word")).toHaveLength(0);
  });
});

describe("quick-add × mentions", () => {
  const now = new Date(2026, 6, 12, 10, 0, 0); // Sun Jul 12 2026

  it("a mention's title words are never eaten by the date parser", () => {
    const input = "prep @[lunch today](task:1) tomorrow";
    const parsed = parseQuickAdd(input, now);
    // "today" inside the markup stays; the trailing "tomorrow" is the due.
    expect(parsed.title).toBe("prep @[lunch today](task:1)");
    expect(parsed.due?.getDate()).toBe(13);
  });

  it("labels inside mention titles stay literal", () => {
    const parsed = parseQuickAdd("see @[the #plan doc](https://d.example) #work", now);
    expect(parsed.labels).toEqual(["work"]);
    expect(parsed.title).toBe("see @[the #plan doc](https://d.example)");
  });

  it("tokenSpans marks the mention range", () => {
    const input = "x @[a today](task:1) fri";
    const spans = tokenSpans(input, now);
    const mention = spans.find((s) => s.kind === "mention");
    expect(mention).toBeDefined();
    expect(input.slice(mention!.start, mention!.end)).toBe("@[a today](task:1)");
    expect(spans.some((s) => s.kind === "due")).toBe(true); // the "fri"
  });
});
