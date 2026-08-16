// Auto mode's per-session state: the verdict cache that keeps the hot path
// free of classifier calls, and the circuit breaker that stops an agent from
// grinding against the same refusal while nobody is watching.
//
// Both are reset by the user speaking, because that is auto mode's approval
// channel: the next message may be the grant, so a cached deny must not eat
// it, and a breaker must not outlive the answer that resolves it.
import type { Classifier } from "./classifier";
import type { AutoModeConfig } from "./config";
import { denialToolResult } from "./denial";
import { decideStatically, describeCall, normalizedIntent, type StaticRule, type ToolCall } from "./policy";

/** Denials in a row, without an allowed call between them. */
export const consecutiveDenialLimit = 3;

/** Denials since the user last said anything. */
export const totalDenialLimit = 20;

export type DecisionRule = StaticRule | "cached-allow" | "cached-deny" | "classifier" | "circuit-breaker";

export type SessionDecision =
  | { outcome: "run"; rule: DecisionRule }
  | { outcome: "block"; rule: DecisionRule; action: string; reason: string; toolResult: string };

export type BreakerState = {
  consecutive: number;
  total: number;
  tripped: boolean;
};

export type DecideOptions = {
  cwd: string;
  signal?: AbortSignal;
};

export class AutoModeSession {
  // Allow verdicts live for the session; deny verdicts are dropped the
  // moment the user speaks.
  private readonly cache = new Map<string, { verdict: "allow" | "deny"; reason: string }>();
  private consecutiveDenials = 0;
  private totalDenials = 0;

  constructor(
    private readonly config: AutoModeConfig,
    private readonly classifier: Classifier,
  ) {}

  breaker(): BreakerState {
    return {
      consecutive: this.consecutiveDenials,
      total: this.totalDenials,
      tripped: this.consecutiveDenials >= consecutiveDenialLimit || this.totalDenials >= totalDenialLimit,
    };
  }

  /** The user said something: their message may be the approval. */
  noteUserInput(): void {
    for (const [key, entry] of this.cache) if (entry.verdict === "deny") this.cache.delete(key);
    this.consecutiveDenials = 0;
    this.totalDenials = 0;
  }

  async decide(call: ToolCall, options: DecideOptions): Promise<SessionDecision> {
    const staticDecision = decideStatically(call, this.config, options.cwd);
    if (staticDecision.outcome === "run") return this.allowed(staticDecision.rule);
    if (staticDecision.outcome === "block") {
      return this.denied(call, staticDecision.rule, staticDecision.reason);
    }

    const intent = normalizedIntent(call);
    const cached = this.cache.get(intent);
    if (cached?.verdict === "allow") return this.allowed("cached-allow");
    if (cached?.verdict === "deny") return this.denied(call, "cached-deny", cached.reason);

    const breaker = this.breaker();
    if (breaker.tripped) {
      return this.denied(
        call,
        "circuit-breaker",
        `auto mode has refused ${breaker.consecutive} calls in a row and ${breaker.total} since the ` +
          `user last spoke (limits: ${consecutiveDenialLimit} consecutive, ${totalDenialLimit} total), ` +
          `so it stopped judging further calls. The user has to answer before anything else runs.`,
      );
    }

    const judged = await this.classifier.classify({
      call,
      cwd: options.cwd,
      reason: staticDecision.reason,
      environment: this.config.environment,
      signal: options.signal,
    });
    if (judged.verdict === "allow") {
      this.cache.set(intent, { verdict: "allow", reason: judged.reason ?? "" });
      return this.allowed("classifier");
    }
    const reason =
      judged.verdict === "deny"
        ? judged.reason
        : `auto mode could not judge this call confidently${judged.reason ? `: ${judged.reason}` : ""}`;
    this.cache.set(intent, { verdict: "deny", reason });
    return this.denied(call, "classifier", reason);
  }

  private allowed(rule: DecisionRule): SessionDecision {
    this.consecutiveDenials = 0;
    return { outcome: "run", rule };
  }

  private denied(call: ToolCall, rule: DecisionRule, reason: string): SessionDecision {
    this.consecutiveDenials += 1;
    this.totalDenials += 1;
    const action = describeCall(call);
    return { outcome: "block", rule, action, reason, toolResult: denialToolResult({ action, reason }) };
  }
}
