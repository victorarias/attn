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
  tooLongQuestion,
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

export type SessionCompactEventLike = { type: "session_compact" };

export type ToolResultEventLike = {
  type: "tool_result";
  toolCallId: string;
  usage?: UsageLike;
};

export type ToolResultEventResultLike = { usage?: UsageLike };

export type AutoModeContextLike = {
  cwd: string;
  signal?: AbortSignal;

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
  on(event: "session_compact", handler: (event: SessionCompactEventLike, ctx: AutoModeContextLike) => void): void;
  on(
    event: "tool_result",
    handler: (event: ToolResultEventLike, ctx: AutoModeContextLike) => ToolResultEventResultLike | undefined,
  ): void;
};

export type AutoModeDenial = {
  toolCallId: string;

  tool: string;

  action: string;
  reason: string;

  rule: string;

  at: string;

  clearable?: boolean;

  prompt?: ClassifierPrompt;
};

export type AutoModeOptions = {
  config: AutoModeConfig;
  classifier: Classifier;

  isEnabled?: () => boolean;

  ledger?: DenialLedgerLike;

  onDenial?: (denial: AutoModeDenial) => void;

  onWaitingForUser?: (waiting: boolean) => void;

  usageLedger?: UsageLedger;
};

export function createAutoMode(options: AutoModeOptions): (pi: AutoModeExtensionAPILike) => void {
  return function autoMode(pi: AutoModeExtensionAPILike): void {
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

    let standing: AutoModeDenial[] = [];

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
        if (decision.outcome === "block" && decision.rule === "classifier-too-long") {
          if (await askToRun(decision.action, ctx, options.onWaitingForUser)) {
            session.noteApprovedCall(call);
            return undefined;
          }
        }
      } catch (error) {
        return { block: true, reason: denialToolResult({ action: describeCall(call), reason: failureReason(error) }) };
      } finally {
        deciding -= 1;

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

    pi.on("before_agent_start", (event) => {
      if (promptIsUsers) session.noteUserInput(event.prompt);
      if (options.isEnabled?.() === false) return {};
      return { systemPrompt: `${event.systemPrompt}\n\n${autoModeSystemPromptAddendum()}` };
    });

    pi.on("message_end", (event) => {
      if (event.message.role !== "assistant") return;
      session.noteAssistantText(messageText(event.message));
    });

    pi.on("session_compact", () => {
      session.noteCompaction();
    });

    pi.on("tool_result", (event) => {
      const held = options.usageLedger?.drain();
      return held ? { usage: mergeUsage(event.usage, held) } : undefined;
    });
  };
}

function uiOf(ctx: AutoModeContextLike | undefined): AutoModeUILike | undefined {
  return ctx?.hasUI === false ? undefined : ctx?.ui;
}

function showDenial(ctx: AutoModeContextLike, denial: AutoModeDenial, standing: readonly AutoModeDenial[]): void {
  const ui = uiOf(ctx);
  if (!ui) return;
  ui.notify(denialNotice(denial), "warning");
  ui.setWidget(autoModeDenialWidgetKey, denialWidgetLines(standing));
}

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
    return false;
  } finally {
    announceWaiting(ctx, onWaitingForUser, false);
  }
}

async function askToRun(
  action: string,
  ctx: AutoModeContextLike,
  onWaitingForUser?: (waiting: boolean) => void,
): Promise<boolean> {
  const ui = uiOf(ctx);
  if (!ui) return false;
  const question = tooLongQuestion(action);
  announceWaiting(ctx, onWaitingForUser, true);
  try {
    return await ui.confirm(question.title, question.message);
  } catch {
    return false;
  } finally {
    announceWaiting(ctx, onWaitingForUser, false);
  }
}

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

function reportFailure(ctx: AutoModeContextLike, error: unknown): void {
  const message = error instanceof Error ? error.message : String(error);
  uiOf(ctx)?.notify(`auto mode could not report this denial to attn: ${message}`, "warning");
}

function recordFailure(ctx: AutoModeContextLike, error: unknown): void {
  const message = error instanceof Error ? error.message : String(error);
  uiOf(ctx)?.notify(`auto mode could not write this denial to its local record: ${message}`, "error");
}

function failureReason(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  return `auto mode failed while judging this call, so it refused it: ${message}`;
}
