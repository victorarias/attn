export const transcriptEntryLimit = 24;
export const transcriptCharLimit = 16_000;

export type TranscriptRole = "user" | "assistant" | "tool";

export type TranscriptEntry = {
  role: TranscriptRole;
  text: string;

  tool?: string;
};

export class TranscriptWindow {
  private readonly entries: TranscriptEntry[] = [];

  private opening: string | undefined;

  record(role: TranscriptRole, text: string, tool?: string): void {
    const trimmed = text.trim();
    if (trimmed === "") return;
    if (role === "user") {
      if (this.opening === undefined && this.entries.length === 0) {
        this.opening = trimmed;
        return;
      }
      if (trimmed === this.opening) return;
      if (this.latest("user") === trimmed) return;
    }
    this.entries.push({ role, text: trimmed, ...(tool === undefined ? {} : { tool }) });
    while (this.entries.length > transcriptEntryLimit) this.entries.shift();
  }

  recordToolCall(tool: string, action: string): void {
    this.record("tool", action, tool);
  }

  latest(role: TranscriptRole): string | undefined {
    for (let i = this.entries.length - 1; i >= 0; i--) {
      const entry = this.entries[i];
      if (entry?.role === role) return entry.text;
    }
    return role === "user" ? this.opening : undefined;
  }

  grant(): string | undefined {
    return this.opening;
  }

  oversized(): number | undefined {
    const newest = this.entries[this.entries.length - 1];
    if (newest === undefined) return undefined;
    return newest.text.length > transcriptCharLimit ? newest.text.length : undefined;
  }

  snapshot(): TranscriptEntry[] {
    const kept: TranscriptEntry[] = [];
    let budget = transcriptCharLimit;
    for (let i = this.entries.length - 1; i >= 0; i--) {
      const entry = this.entries[i];
      if (!entry) continue;
      const cost = entry.text.length;
      if (cost > budget) break;
      budget -= cost;
      kept.unshift(entry);
    }
    return kept;
  }
}

export function renderTranscript(entries: readonly TranscriptEntry[]): string {
  return entries.map(projectEntry).join("\n");
}

function projectEntry(entry: TranscriptEntry): string {
  const key = entry.role === "tool" ? (entry.tool ?? "tool") : entry.role;
  return JSON.stringify({ [key]: entry.text });
}
