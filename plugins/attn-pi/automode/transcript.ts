// The rolling conversation window the classifier reads. Only what the user
// and the agent SAID: tool results never enter it, because a classifier that
// reads tool output can be talked into a verdict by the file it just read.
//
// Two budgets, both tripwires. Receipts, measured 2026-08-17:
//   - 6,357 user/assistant text messages across 400 real agent transcripts on
//     this machine: p50 140 chars, p90 2,523, p99 18,929, max 632,647. One
//     pasted stack trace is enough to swamp a window with no per-entry cap.
//   - The s7 corpus that scored 0 wrong verdicts on glm-5.3
//     (spike-harness/s7-classifier-receipt.js) judged against a 321-char,
//     5-entry transcript.
// So: keep 12 entries, cap each at 4,000 chars (past p90, far under p99), and
// cap the rendered window at 8,000 (25x what the receipt scored on).
//
// The session's opening message is exempt from both, because it is the only
// one that can GRANT anything and it is the first to be squeezed out: it is
// the oldest entry, so the window budget reaches it first, and a delegation
// brief is far longer than a typed message, so the entry cap reaches it too.
// Measured 2026-08-22 over 29 real sessions: every delegation brief on this
// machine (5,881 / 5,881 / 5,058 / 4,993 / 4,495 chars) exceeded the 4,000
// cap, and clampEntry cut the middle out of all five — which is where a brief
// states what the agent is authorized to do. Typed openings were 110-484.
// So the opening gets its own cap past the largest real brief, and holds a
// reserved seat the newest-first fill cannot take.
export const transcriptEntryLimit = 12;
export const transcriptEntryCharLimit = 4_000;
export const transcriptCharLimit = 8_000;
/** Past the largest real brief (5,881) and past p99 of all messages (9,191). */
export const openingCharLimit = 12_000;

export type TranscriptRole = "user" | "assistant";

export type TranscriptEntry = {
  role: TranscriptRole;
  text: string;
};

export class TranscriptWindow {
  private readonly entries: TranscriptEntry[] = [];
  /** The session's opening message, kept whatever else the budgets drop. */
  private opening: TranscriptEntry | undefined;

  /**
   * pi announces one user message on two seams (the input event and the prompt
   * the turn starts with), and the same sentence twice reads as insistence.
   * The comparison happens here because only this class knows which cap the
   * message was stored under: past a cap the raw text never matches the stored
   * form, so the message that gets clamped is exactly the one recorded twice.
   */
  record(role: TranscriptRole, text: string): void {
    const trimmed = text.trim();
    if (trimmed === "") return;
    if (role === "user") {
      // The seat is the SESSION's opening, so it is claimed only by a message
      // with nothing before it: taking it later would render that message
      // ahead of turns it actually followed, and the window promises oldest
      // first.
      if (this.opening === undefined && this.entries.length === 0) {
        this.opening = { role, text: clampEntry(trimmed, openingCharLimit) };
        return;
      }
      if (this.opening !== undefined && clampEntry(trimmed, openingCharLimit) === this.opening.text) return;
      if (this.latest("user") === clampEntry(trimmed)) return;
    }
    this.entries.push({ role, text: clampEntry(trimmed) });
    while (this.entries.length > transcriptEntryLimit) this.entries.shift();
  }

  /** The newest entry of a role, for callers deduplicating two seams. */
  latest(role: TranscriptRole): string | undefined {
    for (let i = this.entries.length - 1; i >= 0; i--) {
      const entry = this.entries[i];
      if (entry?.role === role) return entry.text;
    }
    return this.opening?.role === role ? this.opening.text : undefined;
  }

  /**
   * Oldest first, already inside both budgets. The opening keeps its seat and
   * spends no part of the rolling window's budget: a long brief must not cost
   * the recent turns their place, and the recent turns must not cost the
   * session the only message that says what was authorized.
   */
  snapshot(): TranscriptEntry[] {
    const kept: TranscriptEntry[] = [];
    let budget = transcriptCharLimit;
    for (let i = this.entries.length - 1; i >= 0; i--) {
      const entry = this.entries[i];
      if (!entry) continue;
      if (entry.text.length > budget) break;
      budget -= entry.text.length;
      kept.unshift(entry);
    }
    return this.opening === undefined ? kept : [this.opening, ...kept];
  }
}

/** `[user] …` / `[assistant] …`, the shape the s7 receipt scored on. */
export function renderTranscript(entries: readonly TranscriptEntry[]): string {
  return entries.map((entry) => `[${entry.role}] ${entry.text}`).join("\n");
}

/**
 * Keeps the head and the tail of an oversized message. An ask opens a message
 * and a boundary often closes one ("…and don't push until I look"), so
 * dropping either end loses exactly the sentence the verdict turns on.
 */
function clampEntry(text: string, limit = transcriptEntryCharLimit): string {
  if (text.length <= limit) return text;
  const half = Math.floor((limit - 1) / 2);
  return `${text.slice(0, half)}…${text.slice(text.length - half)}`;
}
