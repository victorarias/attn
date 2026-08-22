import { describe, expect, test } from "bun:test";
import { renderTranscript, TranscriptWindow } from "../automode/transcript";

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

  test("holds every turn whole, however long the conversation runs", () => {
    const long = "y".repeat(20_000);
    const transcript = rollingWindowOf(
      Array.from({ length: 200 }, (_, index) => ["assistant", `${index}: ${long}`] as const),
    );
    const snapshot = transcript.snapshot();
    expect(snapshot).toHaveLength(200);
    expect(snapshot[0]?.text.startsWith("0: ")).toBe(true);
    expect(snapshot.every((entry) => !entry.text.includes("…"))).toBe(true);
  });

  test("drops what pi dropped when pi compacts, and keeps the grant", () => {
    const transcript = windowOf([
      ["user", "you may force-push your own branch"],
      ["assistant", "before the compaction"],
    ]);
    transcript.compacted();
    expect(transcript.snapshot()).toEqual([]);
    expect(transcript.grant()).toBe("you may force-push your own branch");

    transcript.record("assistant", "after the compaction");
    expect(renderTranscript(transcript.snapshot())).toBe('{"assistant":"after the compaction"}');
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
    "x".repeat(20_000),
    "You are authorized to: create branches and push them, open the PR, and force-push your own branch.",
    "y".repeat(20_000),
    "Done when: the fix is merged.",
  ].join("\n");

  test("survives whole, middle included, at a real brief's size", () => {
    const grant = windowOf([["user", brief]]).grant();
    expect(grant).toContain("You are authorized to:");
    expect(grant).not.toContain("…");
    expect(grant?.length).toBe(brief.trim().length);
  });

  test("gets its own seat, so it is never repeated in the rolling window", () => {
    const transcript = windowOf([
      ["user", brief],
      ...Array.from({ length: 40 }, (_, index) => ["assistant", `step ${index}`] as const),
    ]);
    expect(transcript.grant()).toContain("You are authorized to:");
    expect(transcript.snapshot().some((entry) => entry.text.includes("You are authorized to:"))).toBe(false);
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
