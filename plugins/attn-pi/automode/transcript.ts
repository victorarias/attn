// The rolling window the classifier reads. Only what the user and the agent
// SAID: tool output in here can talk a classifier into a verdict.
//
// Receipts, measured 2026-08-17 over 6,357 messages in 400 real transcripts:
// p50 140 chars, p90 2,523, p99 18,929, max 632,647. The s7 corpus that scored
// 0 wrong verdicts judged against 321 chars over 5 entries.
//
// The opening message is exempt from both budgets: it is the only one that can
// GRANT anything and the first to be squeezed out. Measured 2026-08-22, all 5
// delegation briefs on this machine (4,495-5,881 chars) blew the 4,000 cap and
// were cut through the middle, which is where a brief states what is allowed.
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
   * pi announces one user message on two seams and the same sentence twice
   * reads as insistence. Deduped here because only this class knows which cap
   * the message was stored under.
   */
  record(role: TranscriptRole, text: string): void {
    const trimmed = text.trim();
    if (trimmed === "") return;
    if (role === "user") {
      // Claimed only by a message with nothing before it: the window promises
      // oldest first, and a later claim would render out of order.
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

  /** Oldest first, inside both budgets. The opening spends none of them. */
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

/** Head and tail: an ask opens a message and a boundary often closes one. */
function clampEntry(text: string, limit = transcriptEntryCharLimit): string {
  if (text.length <= limit) return text;
  const half = Math.floor((limit - 1) / 2);
  return `${text.slice(0, half)}…${text.slice(text.length - half)}`;
}
