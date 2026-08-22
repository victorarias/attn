export const transcriptEntryLimit = 12;
export const transcriptEntryCharLimit = 4_000;
export const transcriptCharLimit = 8_000;

export const openingCharLimit = 12_000;

export type TranscriptRole = "user" | "assistant";

export type TranscriptEntry = {
  role: TranscriptRole;
  text: string;
};

export class TranscriptWindow {
  private readonly entries: TranscriptEntry[] = [];

  private opening: TranscriptEntry | undefined;

  record(role: TranscriptRole, text: string): void {
    const trimmed = text.trim();
    if (trimmed === "") return;
    if (role === "user") {
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

  latest(role: TranscriptRole): string | undefined {
    for (let i = this.entries.length - 1; i >= 0; i--) {
      const entry = this.entries[i];
      if (entry?.role === role) return entry.text;
    }
    return this.opening?.role === role ? this.opening.text : undefined;
  }

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

export function renderTranscript(entries: readonly TranscriptEntry[]): string {
  return entries.map((entry) => `[${entry.role}] ${entry.text}`).join("\n");
}

function clampEntry(text: string, limit = transcriptEntryCharLimit): string {
  if (text.length <= limit) return text;
  const half = Math.floor((limit - 1) / 2);
  return `${text.slice(0, half)}…${text.slice(text.length - half)}`;
}
