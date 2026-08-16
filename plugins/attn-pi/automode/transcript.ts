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

export const transcriptEntryLimit = 12;
export const transcriptEntryCharLimit = 4_000;
export const transcriptCharLimit = 8_000;

export type TranscriptRole = "user" | "assistant";

export type TranscriptEntry = {
  role: TranscriptRole;
  text: string;
};

export class TranscriptWindow {
  private readonly entries: TranscriptEntry[] = [];

  record(role: TranscriptRole, text: string): void {
    const trimmed = text.trim();
    if (trimmed === "") return;
    this.entries.push({ role, text: clampEntry(trimmed) });
    while (this.entries.length > transcriptEntryLimit) this.entries.shift();
  }

  /** The newest entry of a role, for callers deduplicating two seams. */
  latest(role: TranscriptRole): string | undefined {
    for (let i = this.entries.length - 1; i >= 0; i--) {
      const entry = this.entries[i];
      if (entry?.role === role) return entry.text;
    }
    return undefined;
  }

  /** Oldest first, already inside both budgets. */
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
    return kept;
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
function clampEntry(text: string): string {
  if (text.length <= transcriptEntryCharLimit) return text;
  const half = Math.floor((transcriptEntryCharLimit - 1) / 2);
  return `${text.slice(0, half)}…${text.slice(text.length - half)}`;
}
