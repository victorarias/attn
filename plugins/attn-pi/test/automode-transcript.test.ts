import { describe, expect, test } from "bun:test";
import {
  renderTranscript,
  TranscriptWindow,
  transcriptCharLimit,
  transcriptEntryCharLimit,
  transcriptEntryLimit,
} from "../automode/transcript";

function windowOf(entries: readonly [role: "user" | "assistant", text: string][]): TranscriptWindow {
  const transcript = new TranscriptWindow();
  for (const [role, text] of entries) transcript.record(role, text);
  return transcript;
}

describe("the transcript window", () => {
  test("keeps what was said, oldest first", () => {
    const transcript = windowOf([
      ["user", "get CI green"],
      ["assistant", "on it"],
      ["user", "then push"],
    ]);
    expect(renderTranscript(transcript.snapshot())).toBe("[user] get CI green\n[assistant] on it\n[user] then push");
  });

  test("drops empty and whitespace-only turns", () => {
    expect(windowOf([["assistant", "   \n "]]).snapshot()).toEqual([]);
  });

  test("trims text, so the same message on two seams compares equal", () => {
    const transcript = windowOf([["user", "  push it  "]]);
    expect(transcript.latest("user")).toBe("push it");
  });

  test("keeps only the newest entries", () => {
    const transcript = windowOf(
      Array.from({ length: transcriptEntryLimit + 5 }, (_, index) => ["user", `message ${index}`] as const),
    );
    const snapshot = transcript.snapshot();
    expect(snapshot).toHaveLength(transcriptEntryLimit);
    expect(snapshot[0]?.text).toBe("message 5");
  });

  test("one oversized message is clamped, keeping its head and its tail", () => {
    const text = `ASK${"x".repeat(transcriptEntryCharLimit * 2)}BOUNDARY`;
    const [entry] = windowOf([["user", text]]).snapshot();
    expect(entry?.text.length).toBeLessThanOrEqual(transcriptEntryCharLimit);
    expect(entry?.text.startsWith("ASK")).toBe(true);
    expect(entry?.text.endsWith("BOUNDARY")).toBe(true);
    expect(entry?.text).toContain("…");
  });

  test("the rendered window stays inside its budget, keeping the newest turns", () => {
    const long = "y".repeat(transcriptEntryCharLimit);
    const transcript = windowOf([
      ["user", long],
      ["assistant", long],
      ["user", "the newest thing anyone said"],
    ]);
    const snapshot = transcript.snapshot();
    const size = snapshot.reduce((total, entry) => total + entry.text.length, 0);
    expect(size).toBeLessThanOrEqual(transcriptCharLimit);
    expect(snapshot[snapshot.length - 1]?.text).toBe("the newest thing anyone said");
  });

  test("latest reads back the newest turn of a role", () => {
    const transcript = windowOf([
      ["user", "first"],
      ["assistant", "reply"],
      ["user", "second"],
    ]);
    expect(transcript.latest("user")).toBe("second");
    expect(transcript.latest("assistant")).toBe("reply");
    expect(new TranscriptWindow().latest("user")).toBeUndefined();
  });
});
