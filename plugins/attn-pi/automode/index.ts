// pi wiring for auto mode. Duck-typed against pi's ExtensionAPI shapes (pi
// 0.84.2) rather than importing pi, so the whole extension runs under
// `bun test`. Deciding lives in ./policy and ./session; this turns their answer
// into pi's `{ block, reason }`, and fails closed whatever goes wrong.
import { autoModeSystemPromptAddendum } from "./addendum";
import type { Classifier, ClassifierPrompt } from "./classifier";
import type { AutoModeConfig } from "./config";
import { denialToolResult } from "./denial";
import type { DenialLedgerLike } from "./ledger";
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
  /** RFC 3339. */
  at: string;
  /** False when the user's approval cannot lift this one. */
  clearable?: boolean;
  /** Kept by the ledger; never sent over the relay. */
  prompt?: ClassifierPrompt;
};

export type AutoModeOptions = {
  config: AutoModeConfig;
  classifier: Classifier;
  /**
   * Asked per call, because `/auto` can turn it off mid-session. Off still
   * listens to the conversation, so turning it back on has its context.
   */
  isEnabled?: () => boolean;
  /** The durable local record, written before anything is told about the denial. */
  ledger?: DenialLedgerLike;
  /** Called for every blocked call, for the surfaces that report denials. */
  onDenial?: (denial: AutoModeDenial) => void;
  /**
   * True while the breaker's question is on screen: attn declares
   * `pending_approval` from it. A listener that throws must not swallow the
   * answer, so the caller catches for it.
   */
  onWaitingForUser?: (waiting: boolean) => void;
  /** Where classifier usage waits to ride out on a tool result. */
  usageLedger?: UsageLedger;
};

/** pi re-runs this factory per session transition, so each run gets its own state. */
export function createAutoMode(options: AutoModeOptions): (pi: AutoModeExtensionAPILike) => void {
  return function autoMode(pi: AutoModeExtensionAPILike): void {
    // Counted, not flagged: pi runs tool calls in parallel and the last one to
    // finish is what puts the working message back.
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
    // Denials since the user last spoke.
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
          if (await askToResume(session, ctx, options.onWaitingForUser)) {
            breakerAsked = false;
            session.resumeAfterBreaker();
            decision = await session.decide(call, decideOptions);
          }
        }
      } catch (error) {
        // pi blocks a throwing handler anyway, but the model would get pi's
        // error text instead of the denial contract.
        return { block: true, reason: denialToolResult({ action: describeCall(call), reason: failureReason(error) }) };
      } finally {
        deciding -= 1;
        // A ctx from a superseded session generation throws on any use.
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
        ...(decision.clearable === false ? { clearable: false } : {}),
        ...(decision.prompt ? { prompt: decision.prompt } : {}),
      };
      // The record first, the report second: the relay may lose a denial, the
      // file may not. Neither can change the block by failing.
      try {
        options.ledger?.record(denial);
      } catch (error) {
        recordFailure(ctx, error);
      }
      try {
        options.onDenial?.(denial);
      } catch (error) {
        reportFailure(ctx, error);
      }
      standing = [...standing, denial];
      showDenial(ctx, denial, standing);
      return { block: true, reason: decision.toolResult };
    });

    // An extension's own prompt grants nothing. before_agent_start carries no
    // source, so it is remembered here for that seam.
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

    // Catches the prompts that never surface as an input event (an SDK
    // `session.prompt`, a launch brief); noteUserInput drops the repeat.
    pi.on("before_agent_start", (event) => {
      if (promptIsUsers) session.noteUserInput(event.prompt);
      if (options.isEnabled?.() === false) return {};
      return { systemPrompt: `${event.systemPrompt}\n\n${autoModeSystemPromptAddendum()}` };
    });

    // Only assistant TEXT: a toolResult message is an injection surface.
    pi.on("message_end", (event) => {
      if (event.message.role !== "assistant") return;
      session.noteAssistantText(messageText(event.message));
    });

    // pi takes the returned usage INSTEAD of the tool's own.
    pi.on("tool_result", (event) => {
      const held = options.usageLedger?.drain();
      return held ? { usage: mergeUsage(event.usage, held) } : undefined;
    });
  };
}

/** pi's UI, when there is one: `-p` and `--mode json` have none. */
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

/** No UI is fail-closed: an unattended run has nobody to answer. */
async function askToResume(
  session: AutoModeSession,
  ctx: AutoModeContextLike,
  onWaitingForUser?: (waiting: boolean) => void,
): Promise<boolean> {
  const ui = uiOf(ctx);
  if (!ui) return false;
  const question = breakerQuestion(session.breaker());
  announceWaiting(ctx, onWaitingForUser, true);
  try {
    return await ui.confirm(question.title, question.message);
  } catch {
    // A dialog that could not be shown is not an approval.
    return false;
  } finally {
    announceWaiting(ctx, onWaitingForUser, false);
  }
}

/** Announcing must never decide the answer, so a listener that throws is noted and dropped. */
function announceWaiting(
  ctx: AutoModeContextLike,
  onWaitingForUser: ((waiting: boolean) => void) | undefined,
  waiting: boolean,
): void {
  try {
    onWaitingForUser?.(waiting);
  } catch (error) {
    reportFailure(ctx, error);
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

/** Louder than a lost report: nothing else backs this leg up. */
function recordFailure(ctx: AutoModeContextLike, error: unknown): void {
  const message = error instanceof Error ? error.message : String(error);
  uiOf(ctx)?.notify(`auto mode could not write this denial to its local record: ${message}`, "error");
}

function failureReason(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  return `auto mode failed while judging this call, so it refused it: ${message}`;
}
