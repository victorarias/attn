// Auto mode's per-session state: the conversation window the classifier
// judges against, the verdict cache that keeps the hot path free of classifier
// calls, and the circuit breaker that stops an agent from grinding against the
// same refusal while nobody is watching.
//
// The cache and the breaker are reset by the user speaking, because that is
// auto mode's approval channel: the next message may be the grant, so a cached
// deny must not eat it, and a breaker must not outlive the answer that
// resolves it. The window is not reset — what was said is still what was said.
import type { Classifier } from "./classifier";
import type { AutoModeConfig } from "./config";
import { denialToolResult } from "./denial";
import { decideStatically, describeCall, normalizedIntent, type StaticRule, type ToolCall } from "./policy";
import { TranscriptWindow } from "./transcript";

/** Denials in a row, without an allowed call between them. */
export const consecutiveDenialLimit = 3;

/** Denials since the user last said anything. */
export const totalDenialLimit = 20;

// The rule names a denial reports as "who decided this". A classified call
// names the layer that answered — 2a is the configured classifier, 2b the
// escalation model — and falls back to the bare name when the classifier does
// not say. `classifier-unavailable` is the one that is not a judgment: every
// model a layer could reach failed to answer and auto mode failed closed.
export type DecisionRule =
  | StaticRule
  | "cached-allow"
  | "cached-deny"
  | "classifier"
  | "classifier-2a"
  | "classifier-2b"
  | "classifier-unavailable"
  | "circuit-breaker";

export type SessionDecision =
  | { outcome: "run"; rule: DecisionRule }
  | { outcome: "block"; rule: DecisionRule; action: string; reason: string; toolResult: string };

export type BreakerState = {
  consecutive: number;
  total: number;
  tripped: boolean;
  /**
   * Every block since the counters were cleared was an outage — no model
   * judged any of them. The breaker still stops the session, but what it says
   * about why must not read as a refusal.
   */
  outage: boolean;
};

export type DecideOptions = {
  cwd: string;
  signal?: AbortSignal;
};

export class AutoModeSession {
  // Allow verdicts live for the session; deny verdicts are dropped the
  // moment the user speaks.
  private readonly cache = new Map<string, { verdict: "allow" | "deny"; reason: string }>();
  private readonly transcript = new TranscriptWindow();
  private consecutiveDenials = 0;
  private totalDenials = 0;
  // How many of the blocks counted above were outages rather than verdicts.
  private totalOutages = 0;

  constructor(
    private readonly config: AutoModeConfig,
    private readonly classifier: Classifier,
  ) {}

  breaker(): BreakerState {
    return {
      consecutive: this.consecutiveDenials,
      total: this.totalDenials,
      tripped: this.consecutiveDenials >= consecutiveDenialLimit || this.totalDenials >= totalDenialLimit,
      outage: this.totalDenials > 0 && this.totalOutages === this.totalDenials,
    };
  }

  /** The user said something: their message may be the approval. */
  noteUserInput(text = ""): void {
    this.transcript.record("user", text);
    for (const [key, entry] of this.cache) if (entry.verdict === "deny") this.cache.delete(key);
    this.clearCounters();
  }

  /**
   * The user answered the breaker's question. It clears the counters and
   * nothing else: resuming means calls get judged again, not that the call
   * that tripped the breaker is approved. The deny cache stays, so a refusal
   * the user has not spoken to still stands.
   */
  resumeAfterBreaker(): void {
    this.clearCounters();
  }

  private clearCounters(): void {
    this.consecutiveDenials = 0;
    this.totalDenials = 0;
    this.totalOutages = 0;
  }

  /** The agent said something. Only what it SAID: never a tool result. */
  noteAssistantText(text: string): void {
    this.transcript.record("assistant", text);
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
      // The breaker's own block is of whatever kind the episode is: counting it
      // as a judgment would turn a run of pure outages into a mixed episode on
      // the very call that reports it.
      return this.denied(call, "circuit-breaker", breakerReason(breaker), { outage: breaker.outage });
    }

    const judged = await this.classifier.classify({
      call,
      cwd: options.cwd,
      reason: staticDecision.reason,
      environment: this.config.environment,
      transcript: this.transcript.snapshot(),
      signal: options.signal,
    });
    if (judged.verdict === "deny" && judged.unavailable === true) {
      // Nobody judged this call, so there is no verdict to remember: caching
      // one would keep blocking the call after the endpoint is back, until the
      // user happened to speak.
      return this.denied(call, "classifier-unavailable", judged.reason, { outage: true, judged: false });
    }
    const rule: DecisionRule = judged.layer ? `classifier-${judged.layer}` : "classifier";
    if (judged.verdict === "allow") {
      this.cache.set(intent, { verdict: "allow", reason: judged.reason ?? "" });
      return this.allowed(rule);
    }
    const reason =
      judged.verdict === "deny"
        ? judged.reason
        : `auto mode could not judge this call confidently${judged.reason ? `: ${judged.reason}` : ""}`;
    this.cache.set(intent, { verdict: "deny", reason });
    // An unreadable answer ended the walk, so a model was reached — the rule
    // still names the layer that answered. What it did not do is judge.
    return this.denied(call, rule, reason, {
      outage: false,
      judged: judged.verdict !== "deny" || judged.unreadable !== true,
    });
  }

  private allowed(rule: DecisionRule): SessionDecision {
    this.consecutiveDenials = 0;
    return { outcome: "run", rule };
  }

  private denied(
    call: ToolCall,
    rule: DecisionRule,
    reason: string,
    kind: { outage: boolean; judged?: boolean } = { outage: false },
  ): SessionDecision {
    this.consecutiveDenials += 1;
    this.totalDenials += 1;
    // An outage still counts: an agent grinding against an unreachable
    // classifier is exactly what the breaker exists to stop. What changes is
    // what the breaker then says, not whether it trips.
    if (kind.outage) this.totalOutages += 1;
    const action = describeCall(call);
    return {
      outcome: "block",
      rule,
      action,
      reason,
      toolResult: denialToolResult({ action, reason, judged: kind.judged ?? true }),
    };
  }
}

/**
 * Why the breaker blocked this call, in the model's terms. An episode of pure
 * outages is not the session having been refused twenty times: saying so would
 * put the agent — and then the user it apologizes to — on the wrong problem.
 */
function breakerReason(breaker: BreakerState): string {
  if (breaker.outage) {
    return (
      `auto mode blocked ${breaker.consecutive} calls in a row and ${breaker.total} since the user last ` +
      `spoke, every one of them because its classifier could not be reached (limits: ` +
      `${consecutiveDenialLimit} consecutive, ${totalDenialLimit} total), so it stopped trying. ` +
      `Nothing judged any of those calls dangerous. Tell the user their classifier model looks to be ` +
      `down; they have to answer before anything else runs.`
    );
  }
  return (
    `auto mode has refused ${breaker.consecutive} calls in a row and ${breaker.total} since the ` +
    `user last spoke (limits: ${consecutiveDenialLimit} consecutive, ${totalDenialLimit} total), ` +
    `so it stopped judging further calls. The user has to answer before anything else runs.`
  );
}
