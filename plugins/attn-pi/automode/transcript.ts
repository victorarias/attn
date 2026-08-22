export type TranscriptRole = "user" | "assistant" | "tool";

export type TranscriptEntry = {
  role: TranscriptRole;
  text: string;

  tool?: string;
};

export class TranscriptWindow {
  private entries: TranscriptEntry[] = [];

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
  }

  recordToolCall(tool: string, action: string): void {
    this.record("tool", action, tool);
  }

  compacted(): void {
    this.entries = [];
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

  snapshot(): TranscriptEntry[] {
    return [...this.entries];
  }
}

export function renderTranscript(entries: readonly TranscriptEntry[]): string {
  return entries.map(projectEntry).join("\n");
}

function projectEntry(entry: TranscriptEntry): string {
  const key = entry.role === "tool" ? (entry.tool ?? "tool") : entry.role;
  return JSON.stringify({ [key]: entry.text });
}
