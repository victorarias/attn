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
import {
  autoModeDenialWidgetKey,
  breakerQuestion,
  classifyingWorkingMessage,
  denialNotice,
  denialWidgetLines,
  type AutoModeUILike,
} from "./ui";
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
  /** False in `-p` and `--mode json`: nothing can be asked there. */
  hasUI?: boolean;
  ui?: AutoModeUILike;
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
  /** The tool pi was about to run. */
  tool: string;
  /** The blocked call in one line, the same text the model is given. */
  action: string;
  reason: string;
  /** Who decided: a static rule name, `classifier-2a`/`-2b`, or the breaker. */
  rule: string;
  /** When the call was refused, RFC 3339. */
  at: string;
};

export type AutoModeOptions = {
  config: AutoModeConfig;
  classifier: Classifier;
  /**
   * Whether auto mode judges this call, asked per call because `/auto` can
   * turn it off mid-session. Off means the handlers stay registered and do
   * nothing but keep listening to the conversation, so turning it back on has
   * the context it needs.
   */
  isEnabled?: () => boolean;
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
    // The ctx of the call being judged, so the classification's "checking…"
    // feedback appears for exactly as long as a classification runs — and only
    // when one runs at all, which is the envelope's whole point. pi runs tool
    // calls in parallel, so both are counted rather than flagged: the last one
    // to finish is what puts the working message back.
    let judging: AutoModeContextLike | undefined;
    let deciding = 0;
    let checking = 0;
    const classifier: Classifier = {
      classify: async (request) => {
        const ui = uiOf(judging);
        checking += 1;
        ui?.setWorkingMessage(classifyingWorkingMessage);
        try {
          return await options.classifier.classify(request);
        } finally {
          checking -= 1;
          if (checking === 0) ui?.setWorkingMessage();
        }
      },
    };
    const session = new AutoModeSession(options.config, classifier);
    // Denials since the user last spoke, which is also when the widget listing
    // them is cleared: speaking is what drops them, so it is what unshows them.
    let standing: AutoModeDenial[] = [];
    // The breaker asks once per episode. Answering it, or speaking, ends one.
    let breakerAsked = false;

    pi.on("tool_call", async (event, ctx) => {
      if (options.isEnabled?.() === false) return undefined;
      const call: ToolCall = { toolName: event.toolName, input: event.input };
      const decideOptions = { cwd: ctx.cwd, signal: ctx.signal };
      let decision: SessionDecision;
      judging = ctx;
      deciding += 1;
      try {
        decision = await session.decide(call, decideOptions);
        if (decision.outcome === "block" && decision.rule === "circuit-breaker" && !breakerAsked) {
          breakerAsked = true;
          if (await askToResume(session, ctx)) {
            breakerAsked = false;
            session.resumeAfterBreaker();
            decision = await session.decide(call, decideOptions);
          }
        }
      } catch (error) {
        // pi already blocks a tool whose tool_call handler throws, but the
        // model would get pi's error text instead of the denial contract.
        return { block: true, reason: denialToolResult({ action: describeCall(call), reason: failureReason(error) }) };
      } finally {
        deciding -= 1;
        // Held only while a call is in flight: a ctx from a superseded session
        // generation throws on any use.
        if (deciding === 0) judging = undefined;
      }
      if (decision.outcome === "run") return undefined;
      const denial: AutoModeDenial = {
        toolCallId: event.toolCallId,
        tool: call.toolName,
        action: decision.action,
        reason: decision.reason,
        rule: decision.rule,
        at: new Date().toISOString(),
      };
      try {
        options.onDenial?.(denial);
      } catch (error) {
        // Reporting is fire-and-forget: whoever listens must not be able to turn
        // a denial into something else by failing.
        reportFailure(ctx, error);
      }
      standing = [...standing, denial];
      showDenial(ctx, denial, standing);
      return { block: true, reason: decision.toolResult };
    });

    // An extension's own prompt is not the user speaking, so it grants nothing:
    // it must not clear a deny or reset the breaker. before_agent_start carries
    // no source of its own, and pi emits it from the same prompt() call that
    // emitted this event, so the source is remembered here for the seam that
    // cannot see it.
    let promptIsUsers = true;
    pi.on("input", (event, ctx) => {
      promptIsUsers = event.source !== "extension";
      if (!promptIsUsers) return;
      session.noteUserInput(event.text);
      breakerAsked = false;
      if (standing.length > 0) {
        standing = [];
        uiOf(ctx)?.setWidget(autoModeDenialWidgetKey, undefined);
      }
    });

    // The turn's prompt, for the deliveries that never surface as an input
    // event (an SDK `session.prompt`, a launch brief). noteUserInput drops the
    // repeat when a message arrives on both seams. The addendum is appended
    // whoever prompted — the agent is under auto mode either way — but only
    // while auto mode is on: telling an agent about a permission system that
    // is off would have it asking for approvals nothing is going to withhold.
    pi.on("before_agent_start", (event) => {
      if (promptIsUsers) session.noteUserInput(event.prompt);
      if (options.isEnabled?.() === false) return {};
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

/**
 * pi's UI, when there is one to talk to. `-p` and `--mode json` say so with
 * hasUI, and a duck-typed ctx (a test's, an older pi's) may carry no ui at all.
 */
function uiOf(ctx: AutoModeContextLike | undefined): AutoModeUILike | undefined {
  return ctx?.hasUI === false ? undefined : ctx?.ui;
}

/** The denial the user sees: a line as it happens, and a list that stays. */
function showDenial(ctx: AutoModeContextLike, denial: AutoModeDenial, standing: readonly AutoModeDenial[]): void {
  const ui = uiOf(ctx);
  if (!ui) return;
  ui.notify(denialNotice(denial), "warning");
  ui.setWidget(autoModeDenialWidgetKey, denialWidgetLines(standing));
}

/**
 * The breaker's one question. No UI is fail-closed on purpose: an unattended
 * run has nobody to answer, and the answer is what resumes auto mode.
 */
async function askToResume(session: AutoModeSession, ctx: AutoModeContextLike): Promise<boolean> {
  const ui = uiOf(ctx);
  if (!ui) return false;
  const breaker = session.breaker();
  const question = breakerQuestion(breaker.consecutive, breaker.total);
  try {
    return await ui.confirm(question.title, question.message);
  } catch {
    // A dialog that could not be shown is not an approval.
    return false;
  }
}

function messageText(message: MessageLike): string {
  return message.content
    .filter((block) => block.type === "text" && typeof block.text === "string")
    .map((block) => block.text)
    .join("\n");
}

/** A reporter that threw. The denial itself stands; only the report is lost. */
function reportFailure(ctx: AutoModeContextLike, error: unknown): void {
  const message = error instanceof Error ? error.message : String(error);
  uiOf(ctx)?.notify(`auto mode could not report this denial to attn: ${message}`, "warning");
}

function failureReason(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  return `auto mode failed while judging this call, so it refused it: ${message}`;
}
