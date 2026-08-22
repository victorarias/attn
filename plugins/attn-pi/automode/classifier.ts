import type { ToolCall } from "./policy";
import type { TranscriptEntry } from "./transcript";

export type ClassifierRequest = {
  call: ToolCall;
  cwd: string;

  reason: string;

  environment: readonly string[];

  transcript?: readonly TranscriptEntry[];

  grant?: string;

  signal?: AbortSignal;
};

export type ClassifierLayer = "harm" | "intent";

export type ClassifierPrompt = {
  layer: ClassifierLayer;
  system: string;
  user: string;
};

export type ClassifierVerdict =
  | { verdict: "allow"; reason?: string; layer?: ClassifierLayer; severity?: number }
  | {
      verdict: "deny";
      reason: string;
      layer?: ClassifierLayer;
      prompt?: ClassifierPrompt;

      severity?: number;

      category?: string;

      boundary?: boolean;

      unavailable?: boolean;

      unreadable?: boolean;

      tooLong?: boolean;
    };

export interface Classifier {
  classify(request: ClassifierRequest): Promise<ClassifierVerdict>;
}

export class StubClassifier implements Classifier {
  readonly requests: ClassifierRequest[] = [];

  constructor(private verdict: ClassifierVerdict = { verdict: "deny", reason: "stub classifier denies" }) {}

  answerWith(verdict: ClassifierVerdict): void {
    this.verdict = verdict;
  }

  async classify(request: ClassifierRequest): Promise<ClassifierVerdict> {
    this.requests.push(request);
    return this.verdict;
  }
}
