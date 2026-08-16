// pi wiring for auto mode. Like suite/core.ts this file is duck-typed
// against pi's ExtensionAPI/ExtensionContext shapes (verified against pi
// 0.84.2, packages/coding-agent/src/core/extensions/types.ts) rather than
// importing pi, so the whole extension runs under `bun test`.
//
// Everything decided lives in ./policy and ./session; this file only turns
// their answer into pi's `{ block, reason }` and keeps the fail-safe
// posture: whatever goes wrong here, the tool does not run.
import { autoModeSystemPromptAddendum } from "./addendum";
import type { Classifier } from "./classifier";
import type { AutoModeConfig } from "./config";
import { denialToolResult } from "./denial";
import { describeCall, type ToolCall } from "./policy";
import { AutoModeSession, type SessionDecision } from "./session";
import { mergeUsage, UsageLedger, type UsageLike } from "./usage";

export type ToolCallEventLike = {
  type: "tool_call";
  toolCallId: string;
  toolName: string;
  input: Record<string, unknown>;
};

export type ToolCallEventResultLike = { block?: boolean; reason?: string };

export type InputEventLike = {
  type: "input";
  text: string;
  source: "interactive" | "rpc" | "extension";
};

export type BeforeAgentStartEventLike = {
  type: "before_agent_start";
  prompt: string;
  systemPrompt: string;
};

export type BeforeAgentStartResultLike = { systemPrompt?: string };

export type MessageLike = { role: string; content: { type: string; text?: string }[] };

export type MessageEndEventLike = { type: "message_end"; message: MessageLike };

export type ToolResultEventLike = {
  type: "tool_result";
  toolCallId: string;
  usage?: UsageLike;
};

export type ToolResultEventResultLike = { usage?: UsageLike };

export type AutoModeContextLike = {
  cwd: string;
  signal?: AbortSignal;
};

export type AutoModeExtensionAPILike = {
  on(
    event: "tool_call",
    handler: (
      event: ToolCallEventLike,
      ctx: AutoModeContextLike,
    ) => ToolCallEventResultLike | undefined | Promise<ToolCallEventResultLike | undefined>,
  ): void;
  on(event: "input", handler: (event: InputEventLike, ctx: AutoModeContextLike) => void): void;
  on(
    event: "before_agent_start",
    handler: (event: BeforeAgentStartEventLike, ctx: AutoModeContextLike) => BeforeAgentStartResultLike,
  ): void;
  on(event: "message_end", handler: (event: MessageEndEventLike, ctx: AutoModeContextLike) => void): void;
  on(
    event: "tool_result",
    handler: (event: ToolResultEventLike, ctx: AutoModeContextLike) => ToolResultEventResultLike | undefined,
  ): void;
};

export type AutoModeDenial = {
  toolCallId: string;
  action: string;
  reason: string;
  rule: string;
};

export type AutoModeOptions = {
  config: AutoModeConfig;
  classifier: Classifier;
  /** Auto mode off registers nothing: pi behaves exactly as it does without this extension. */
  enabled?: boolean;
  /** Called for every blocked call, for the surfaces that report denials. */
  onDenial?: (denial: AutoModeDenial) => void;
  /**
   * Where the classifier's usage waits for a tool result to ride into the
   * session's totals. The same ledger the classifier reports into.
   */
  usageLedger?: UsageLedger;
};

/**
 * Builds the pi extension factory. pi re-runs a factory on every session
 * transition, so each run gets its own AutoModeSession — a new, forked, or
 * resumed session starts with an empty verdict cache and a clear breaker.
 */
export function createAutoMode(options: AutoModeOptions): (pi: AutoModeExtensionAPILike) => void {
  return function autoMode(pi: AutoModeExtensionAPILike): void {
    if (options.enabled === false) return;
    const session = new AutoModeSession(options.config, options.classifier);

    pi.on("tool_call", async (event, ctx) => {
      const call: ToolCall = { toolName: event.toolName, input: event.input };
      let decision: SessionDecision;
      try {
        decision = await session.decide(call, { cwd: ctx.cwd, signal: ctx.signal });
      } catch (error) {
        // pi already blocks a tool whose tool_call handler throws, but the
        // model would get pi's error text instead of the denial contract.
        return { block: true, reason: denialToolResult({ action: describeCall(call), reason: failureReason(error) }) };
      }
      if (decision.outcome === "run") return undefined;
      options.onDenial?.({
        toolCallId: event.toolCallId,
        action: decision.action,
        reason: decision.reason,
        rule: decision.rule,
      });
      return { block: true, reason: decision.toolResult };
    });

    pi.on("input", (event) => {
      if (event.source !== "extension") session.noteUserInput(event.text);
    });

    // The turn's prompt, for the deliveries that never surface as an input
    // event (an SDK `session.prompt`, a launch brief). noteUserInput drops the
    // repeat when a message arrives on both seams.
    pi.on("before_agent_start", (event) => {
      session.noteUserInput(event.prompt);
      return { systemPrompt: `${event.systemPrompt}\n\n${autoModeSystemPromptAddendum()}` };
    });

    // Only assistant TEXT. A toolResult message is the injection surface the
    // classifier's prompt exists to stay out of.
    pi.on("message_end", (event) => {
      if (event.message.role !== "assistant") return;
      session.noteAssistantText(messageText(event.message));
    });

    // pi takes the returned usage INSTEAD of the tool's own, so what the tool
    // reported has to come back with it.
    pi.on("tool_result", (event) => {
      const held = options.usageLedger?.drain();
      return held ? { usage: mergeUsage(event.usage, held) } : undefined;
    });
  };
}

function messageText(message: MessageLike): string {
  return message.content
    .filter((block) => block.type === "text" && typeof block.text === "string")
    .map((block) => block.text)
    .join("\n");
}

function failureReason(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  return `auto mode failed while judging this call, so it refused it: ${message}`;
}
