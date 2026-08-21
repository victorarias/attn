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

/**
 * Which pass answered: 2a is the configured classifier, 2b the escalation
 * model. Carried so a denial can say who decided it; a classifier that does not
 * name a layer (the stub, a future one-pass judge) leaves it unset.
 */
export type ClassifierLayer = "2a" | "2b";

export type ClassifierVerdict =
  | { verdict: "allow"; reason?: string; layer?: ClassifierLayer }
  | {
      verdict: "deny";
      reason: string;
      layer?: ClassifierLayer;
      /**
       * Nothing judged this call: every model the layer could reach failed to
       * answer. The deny is auto mode failing closed, so the surfaces name it
       * apart from a model that looked and refused.
       */
      unavailable?: boolean;
      /**
       * A model answered and the answer was not a verdict. It ends the layer's
       * walk like any other answer (shopping for a readable one is shopping
       * for a different verdict), but no more judged this call than an outage
       * did, so the model-facing text says so.
       */
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
