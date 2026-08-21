import { describe, expect, test } from "bun:test";
import {
  openingCharLimit,
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

/** A window whose opening is already spent, for the rolling-entry rules. */
function rollingWindowOf(entries: readonly [role: "user" | "assistant", text: string][]): TranscriptWindow {
  return windowOf([["user", "the opening"], ...entries]);
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

  test("records one user message once when pi announces it on both seams", () => {
    const transcript = windowOf([
      ["user", "get CI green"],
      ["user", "get CI green"],
    ]);
    expect(transcript.snapshot()).toHaveLength(1);
  });

  test("keeps only the newest rolling entries", () => {
    const transcript = rollingWindowOf(
      Array.from({ length: transcriptEntryLimit + 5 }, (_, index) => ["assistant", `message ${index}`] as const),
    );
    const snapshot = transcript.snapshot();
    expect(snapshot).toHaveLength(transcriptEntryLimit + 1);
    expect(snapshot[0]?.text).toBe("the opening");
    expect(snapshot[1]?.text).toBe("message 5");
  });

  test("one oversized rolling message is clamped, keeping its head and its tail", () => {
    const text = `ASK${"x".repeat(transcriptEntryCharLimit * 2)}BOUNDARY`;
    const snapshot = rollingWindowOf([["user", text]]).snapshot();
    const entry = snapshot[snapshot.length - 1];
    expect(entry?.text.length).toBeLessThanOrEqual(transcriptEntryCharLimit);
    expect(entry?.text.startsWith("ASK")).toBe(true);
    expect(entry?.text.endsWith("BOUNDARY")).toBe(true);
    expect(entry?.text).toContain("…");
  });

  test("the rolling window stays inside its budget, keeping the newest turns", () => {
    const long = "y".repeat(transcriptEntryCharLimit);
    const transcript = rollingWindowOf([
      ["assistant", long],
      ["assistant", long],
      ["user", "the newest thing anyone said"],
    ]);
    const snapshot = transcript.snapshot();
    const rolling = snapshot.slice(1).reduce((total, entry) => total + entry.text.length, 0);
    expect(rolling).toBeLessThanOrEqual(transcriptCharLimit);
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

  test("latest falls back to the opening when nothing has rolled yet", () => {
    expect(windowOf([["user", "the opening"]]).latest("user")).toBe("the opening");
  });
});

// A delegation brief states what the agent may do, and it is both the longest
// message a session sees and the oldest. Measured 2026-08-22: every real brief
// on this machine (4,495-5,881 chars) was clamped head-and-tail at 4,000, and
// the middle it dropped is where the brief says what is authorized.
describe("the session's opening message", () => {
  const brief = [
    "Fix a CI flake in attn's daemon test suite.",
    "x".repeat(transcriptEntryCharLimit),
    "You are authorized to: create branches and push them, open the PR, and force-push your own branch.",
    "y".repeat(transcriptEntryCharLimit),
    "Done when: the fix is merged.",
  ].join("\n");

  test("survives whole, middle included, at a real brief's size", () => {
    expect(brief.length).toBeGreaterThan(transcriptEntryCharLimit);
    const [entry] = windowOf([["user", brief]]).snapshot();
    expect(entry?.text).toContain("You are authorized to:");
    expect(entry?.text).not.toContain("…");
  });

  test("is not evicted by a flood of later turns", () => {
    const transcript = windowOf([
      ["user", brief],
      ...Array.from(
        { length: transcriptEntryLimit * 2 },
        (_, index) => ["assistant", `step ${index}: ${"z".repeat(1_000)}`] as const,
      ),
    ]);
    const snapshot = transcript.snapshot();
    expect(snapshot[0]?.text).toContain("You are authorized to:");
  });

  test("spends none of the rolling window's budget", () => {
    const long = "y".repeat(transcriptEntryCharLimit);
    const snapshot = windowOf([
      ["user", brief],
      ["assistant", long],
      ["assistant", long],
    ]).snapshot();
    expect(snapshot).toHaveLength(3);
  });

  test("is recorded once when pi announces it on both seams", () => {
    const transcript = windowOf([
      ["user", brief],
      ["user", brief],
    ]);
    expect(transcript.snapshot()).toHaveLength(1);
  });

  test("is still clamped past its own cap, so nothing is unbounded", () => {
    const huge = `HEAD${"x".repeat(openingCharLimit * 2)}TAIL`;
    const [entry] = windowOf([["user", huge]]).snapshot();
    expect(entry?.text.length).toBeLessThanOrEqual(openingCharLimit);
    expect(entry?.text.startsWith("HEAD")).toBe(true);
    expect(entry?.text.endsWith("TAIL")).toBe(true);
  });

  test("is claimed only with nothing before it, so oldest-first still holds", () => {
    const transcript = windowOf([
      ["assistant", "thinking out loud before anyone asked"],
      ["user", "the actual ask"],
    ]);
    expect(transcript.snapshot().map((entry) => entry.text)).toEqual([
      "thinking out loud before anyone asked",
      "the actual ask",
    ]);
    expect(windowOf([["user", "the actual ask"]]).snapshot()[0]?.text).toBe("the actual ask");
  });
});
