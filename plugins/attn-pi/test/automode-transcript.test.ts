import { describe, expect, test } from "bun:test";
import {
  renderTranscript,
  TranscriptWindow,
  transcriptCharLimit,
  transcriptEntryLimit,
} from "../automode/transcript";

function windowOf(entries: readonly [role: "user" | "assistant", text: string][]): TranscriptWindow {
  const transcript = new TranscriptWindow();
  for (const [role, text] of entries) transcript.record(role, text);
  return transcript;
}

function rollingWindowOf(entries: readonly [role: "user" | "assistant", text: string][]): TranscriptWindow {
  return windowOf([["user", "the opening"], ...entries]);
}

describe("the transcript window", () => {
  test("keeps what was said after the opening, oldest first", () => {
    const transcript = windowOf([
      ["user", "get CI green"],
      ["assistant", "on it"],
      ["user", "then push"],
    ]);
    expect(renderTranscript(transcript.snapshot())).toBe('{"assistant":"on it"}\n{"user":"then push"}');
  });

  test("projects a tool call under the tool's own name", () => {
    const transcript = rollingWindowOf([]);
    transcript.recordToolCall("bash", "go test ./...");
    expect(renderTranscript(transcript.snapshot())).toBe('{"bash":"go test ./..."}');
  });

  test("drops empty and whitespace-only turns", () => {
    expect(rollingWindowOf([["assistant", "   \n "]]).snapshot()).toEqual([]);
  });

  test("trims text, so the same message on two seams compares equal", () => {
    expect(rollingWindowOf([["user", "  push it  "]]).latest("user")).toBe("push it");
  });

  test("records one user message once when pi announces it on both seams", () => {
    const transcript = rollingWindowOf([
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
    expect(snapshot).toHaveLength(transcriptEntryLimit);
    expect(snapshot[0]?.text).toBe("message 5");
  });

  test("never truncates a message: it drops whole older turns to stay in budget", () => {
    const long = "y".repeat(transcriptCharLimit / 2);
    const transcript = rollingWindowOf([
      ["assistant", long],
      ["assistant", long],
      ["user", "the newest thing anyone said"],
    ]);
    const snapshot = transcript.snapshot();
    const spent = snapshot.reduce((total, entry) => total + entry.text.length, 0);
    expect(spent).toBeLessThanOrEqual(transcriptCharLimit);
    expect(snapshot.every((entry) => !entry.text.includes("…"))).toBe(true);
    expect(snapshot[snapshot.length - 1]?.text).toBe("the newest thing anyone said");
  });

  test("reports a newest message that alone does not fit, rather than cutting it", () => {
    const transcript = rollingWindowOf([["user", "z".repeat(transcriptCharLimit + 1)]]);
    expect(transcript.oversized()).toBe(transcriptCharLimit + 1);
    expect(rollingWindowOf([["user", "small"]]).oversized()).toBeUndefined();
    expect(new TranscriptWindow().oversized()).toBeUndefined();
  });

  test("latest reads back the newest turn of a role", () => {
    const transcript = rollingWindowOf([
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

describe("the session's opening message", () => {
  const brief = [
    "Fix a CI flake in attn's daemon test suite.",
    "x".repeat(transcriptCharLimit),
    "You are authorized to: create branches and push them, open the PR, and force-push your own branch.",
    "y".repeat(transcriptCharLimit),
    "Done when: the fix is merged.",
  ].join("\n");

  test("survives whole, middle included, at a real brief's size", () => {
    expect(brief.length).toBeGreaterThan(transcriptCharLimit);
    const grant = windowOf([["user", brief]]).grant();
    expect(grant).toContain("You are authorized to:");
    expect(grant).not.toContain("…");
    expect(grant?.length).toBe(brief.trim().length);
  });

  test("gets its own seat, so no later turn can evict it", () => {
    const transcript = windowOf([
      ["user", brief],
      ...Array.from(
        { length: transcriptEntryLimit * 2 },
        (_, index) => ["assistant", `step ${index}: ${"z".repeat(1_000)}`] as const,
      ),
    ]);
    expect(transcript.grant()).toContain("You are authorized to:");
    expect(transcript.snapshot().some((entry) => entry.text.includes("You are authorized to:"))).toBe(false);
  });

  test("spends none of the rolling window's budget", () => {
    const long = "y".repeat(transcriptCharLimit - 1);
    const snapshot = windowOf([
      ["user", brief],
      ["assistant", long],
    ]).snapshot();
    expect(snapshot).toHaveLength(1);
    expect(snapshot[0]?.text).toBe(long);
  });

  test("is recorded once when pi announces it on both seams", () => {
    const transcript = windowOf([
      ["user", brief],
      ["user", brief],
    ]);
    expect(transcript.snapshot()).toHaveLength(0);
  });

  test("is claimed only with nothing before it, so oldest-first still holds", () => {
    const transcript = windowOf([
      ["assistant", "thinking out loud before anyone asked"],
      ["user", "the actual ask"],
    ]);
    expect(transcript.grant()).toBeUndefined();
    expect(transcript.snapshot().map((entry) => entry.text)).toEqual([
      "thinking out loud before anyone asked",
      "the actual ask",
    ]);
  });
});
