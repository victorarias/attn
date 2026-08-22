// The interface between the static tree and the judging model, plus the stub
// the tests decide with. model-classifier.ts is the implementation.
import type { ToolCall } from "./policy";
import type { TranscriptEntry } from "./transcript";

export type ClassifierRequest = {
  call: ToolCall;
  cwd: string;
  /** Why the static tree could not answer, in its own words. */
  reason: string;
  /** Config prose describing what this machine may do. */
  environment: readonly string[];
  /** What the user and the agent said, oldest first. Never tool results. */
  transcript?: readonly TranscriptEntry[];
  /** pi's turn signal, so Esc aborts a classification in flight. */
  signal?: AbortSignal;
};

/** 2a is the configured classifier, 2b the escalation model. */
export type ClassifierLayer = "2a" | "2b";

/** Exactly what the model was sent, kept on denials for the ledger. */
export type ClassifierPrompt = {
  layer: ClassifierLayer;
  system: string;
  user: string;
};

export type ClassifierVerdict =
  | { verdict: "allow"; reason?: string; layer?: ClassifierLayer }
  | {
      verdict: "deny";
      reason: string;
      layer?: ClassifierLayer;
      prompt?: ClassifierPrompt;
      /** The user's approval does not move this one. */
      boundary?: boolean;
      /** No model could be reached: this deny is auto mode failing closed. */
      unavailable?: boolean;
      /** A model answered and the answer was not a verdict. */
      unreadable?: boolean;
    }
  | { verdict: "uncertain"; reason?: string; layer?: ClassifierLayer };

export interface Classifier {
  classify(request: ClassifierRequest): Promise<ClassifierVerdict>;
}

/** A classifier that answers what it was told to, and records what it saw. */
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
