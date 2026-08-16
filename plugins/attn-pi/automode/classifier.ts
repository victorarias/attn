// The seam between the static tree and the model that judges what the tree
// routes on. This file is the interface both sides agree on, plus the stub the
// tests decide with; model-classifier.ts is the implementation.
import type { ToolCall } from "./policy";
import type { TranscriptEntry } from "./transcript";

export type ClassifierRequest = {
  call: ToolCall;
  /** The session's working directory the call was placed against. */
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

export type ClassifierVerdict =
  | { verdict: "allow"; reason?: string }
  | { verdict: "deny"; reason: string }
  | { verdict: "uncertain"; reason?: string };

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
