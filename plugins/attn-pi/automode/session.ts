import type { Classifier, ClassifierPrompt } from "./classifier";
import type { AutoModeConfig } from "./config";
import { denialToolResult } from "./denial";
import { decideStatically, describeCall, normalizedIntent, type StaticRule, type ToolCall } from "./policy";
import { TranscriptWindow } from "./transcript";

export const consecutiveDenialLimit = 3;

export const totalDenialLimit = 20;

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
  | {
      outcome: "block";
      rule: DecisionRule;
      action: string;
      reason: string;
      toolResult: string;

      prompt?: ClassifierPrompt;

      clearable?: boolean;
    };

export type BreakerState = {
  consecutive: number;
  total: number;
  tripped: boolean;

  outage: boolean;
};

export type DecideOptions = {
  cwd: string;
  signal?: AbortSignal;
};

export class AutoModeSession {
  private readonly cache = new Map<string, { verdict: "allow" | "deny"; reason: string; boundary?: boolean }>();
  private readonly transcript = new TranscriptWindow();
  private consecutiveDenials = 0;
  private totalDenials = 0;
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

  noteUserInput(text = ""): void {
    this.transcript.record("user", text);
    for (const [key, entry] of this.cache) if (entry.verdict === "deny") this.cache.delete(key);
    this.clearCounters();
  }

  resumeAfterBreaker(): void {
    this.clearCounters();
  }

  private clearCounters(): void {
    this.consecutiveDenials = 0;
    this.totalDenials = 0;
    this.totalOutages = 0;
  }

  noteAssistantText(text: string): void {
    this.transcript.record("assistant", text);
  }

  async decide(call: ToolCall, options: DecideOptions): Promise<SessionDecision> {
    const staticDecision = decideStatically(call, this.config, options.cwd);
    if (staticDecision.outcome === "run") return this.allowed(staticDecision.rule);
    if (staticDecision.outcome === "block") {
      return this.denied(call, staticDecision.rule, staticDecision.reason, { outage: false, clearable: false });
    }

    const intent = normalizedIntent(call);
    const cached = this.cache.get(intent);
    if (cached?.verdict === "allow") return this.allowed("cached-allow");
    if (cached?.verdict === "deny") {
      return this.denied(call, "cached-deny", cached.reason, { outage: false, clearable: cached.boundary !== true });
    }

    const breaker = this.breaker();
    if (breaker.tripped) {
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
    const prompt = judged.verdict === "deny" ? judged.prompt : undefined;
    if (judged.verdict === "deny" && judged.unavailable === true) {
      return this.denied(call, "classifier-unavailable", judged.reason, { outage: true, judged: false, prompt });
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
    const boundary = judged.verdict === "deny" && judged.boundary === true;
    this.cache.set(intent, { verdict: "deny", reason, ...(boundary ? { boundary: true } : {}) });
    return this.denied(call, rule, reason, {
      outage: false,
      judged: judged.verdict !== "deny" || judged.unreadable !== true,
      clearable: !boundary,
      prompt,
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
    kind: { outage: boolean; judged?: boolean; clearable?: boolean; prompt?: ClassifierPrompt } = { outage: false },
  ): SessionDecision {
    this.consecutiveDenials += 1;
    this.totalDenials += 1;
    if (kind.outage) this.totalOutages += 1;
    const action = describeCall(call);
    return {
      outcome: "block",
      rule,
      action,
      reason,
      toolResult: denialToolResult({ action, reason, judged: kind.judged ?? true, clearable: kind.clearable ?? true }),
      ...(kind.clearable === false ? { clearable: false } : {}),
      ...(kind.prompt ? { prompt: kind.prompt } : {}),
    };
  }
}

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
