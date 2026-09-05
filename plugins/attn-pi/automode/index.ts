import type { Classifier, ClassifierPrompt } from "./classifier";
import type { AutoModeConfig } from "./config";
import { denialToolResult, sandboxDenialToolResult } from "./denial";
import type { DenialLedgerLike } from "./ledger";
import { describeCall, isSandboxRequest, type ToolCall } from "./policy";
import { AutoModeSession, type SessionDecision } from "./session";
import {
  breakerQuestion,
  classifyingWorkingMessage,
  denialKey,
  dimmed,
  heldCount,
  heldStatusText,
  autoModeStatusKey,
  tooLongQuestion,
  denialNotice,
  type AutoModeUILike,
} from "./ui";
import { mergeUsage, UsageLedger, type UsageLike } from "./usage";
import { credentials } from "../security/filter";
import { actionEvidence, inputFingerprint, snapshotCall, toolEvidenceLimits } from "./evidence";
import { renderPrompt } from "./prompt-catalog";

export type ToolCallEventLike = {
  type: "tool_call";
  toolCallId: string;
  toolName: string;
  input: Record<string, unknown>;
};

export type ToolCallEventResultLike = { block?: boolean; reason?: string };
export type ToolCallReview = (event: ToolCallEventLike, ctx: AutoModeContextLike) => Promise<ToolCallEventResultLike | undefined>;
export type ToolExecutionCheck = (event: ToolCallEventLike, ctx: AutoModeContextLike) => void;

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
  isError?: boolean;
};

export type ToolResultEventResultLike = { usage?: UsageLike };

export type AutoModeContextLike = {
  cwd: string;
  signal?: AbortSignal;
  mode?: string;

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
  /** RFC 3339. */
  at: string;

  clearable?: boolean;

  prompt?: ClassifierPrompt;
};

export type AutoModeOptions = {
  config: AutoModeConfig;
  classifier: Classifier;
  /** Asked per call, because `/auto` can turn auto mode off mid-session. Off still
   * keeps the handlers listening, so turning it back on has the context it needs. */
  isEnabled?: () => boolean;
  /** The durable local record, written before anything is told about the denial.
   * Reporting to attn is a mirror a bare pi lacks and a dead relay drops. */
  ledger?: DenialLedgerLike;
  onDenial?: (denial: AutoModeDenial) => void;
  /** True while the breaker's question is on screen, the one window where pi blocks on
   * the user. A reporter that throws must not swallow the answer, so the caller catches. */
  onWaitingForUser?: (waiting: boolean) => void;
  /** Where the classifier's usage waits for a tool result to ride into the totals. */
  usageLedger?: UsageLedger;
  onReady?: (review: ToolCallReview, checkExecution: ToolExecutionCheck, standing: () => readonly AutoModeDenial[]) => void;
  sandboxReviewInExecutor?: boolean;
  cacheWritePaths?: () => readonly string[];
};

// pi re-runs the factory on every session transition, so a new, forked, or resumed
// session starts with an empty verdict cache and a clear breaker.
export function createAutoMode(options: AutoModeOptions): (pi: AutoModeExtensionAPILike) => void {
  return function autoMode(pi: AutoModeExtensionAPILike): void {
    // pi runs tool calls in parallel, so these are counted rather than flagged: the
    // last classification to finish restores the working message.
    let judging: AutoModeContextLike | undefined;
    let deciding = 0;
    let checking = 0;
    const classifier: Classifier = {
      evidenceLimits: () => options.classifier.evidenceLimits?.() ?? toolEvidenceLimits(),
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
    // The agent retries blocked calls; a repeat restates the count, not the warning.
    let lastDeniedKey: string | undefined;
    let breakerAsked = false;
    const paintHeld = (ctx: AutoModeContextLike, denials: readonly AutoModeDenial[]): void => {
      const ui = uiOf(ctx);
      if (!ui) return;
      // Only the TUI draws the footer; RPC relays status text verbatim, so it stays plain.
      const theme = ctx.mode === "tui" ? ui.theme : undefined;
      const enabled = options.isEnabled?.() !== false;
      ui.setStatus(autoModeStatusKey, dimmed(theme, heldStatusText(enabled, heldCount(denials))));
    };
    const approvals = new Map<string, string>();
    const executionFingerprint = (call: ToolCall, cwd: string) => inputFingerprint({ toolName: call.toolName, input: call.input, cwd });

    const review: ToolCallReview = async (event, ctx) => {
      approvals.delete(event.toolCallId);
      const fingerprint = inputFingerprint(event.input);
      const call = snapshotCall({ toolCallId: event.toolCallId, toolName: event.toolName, input: event.input });
      const approved = () => { approvals.set(event.toolCallId, executionFingerprint(call, ctx.cwd)); };
      if (options.isEnabled?.() === false) {
        session.noteApprovedCall(call, ctx.cwd);
        return undefined;
      }
      const assertCurrent = () => {
        ctx.signal?.throwIfAborted();
        if (inputFingerprint(event.input) !== fingerprint) throw new Error("Tool arguments changed during approval. Submit the updated call for a fresh review.");
      };
      const decideOptions = { cwd: ctx.cwd, signal: ctx.signal, cacheWritePaths: options.cacheWritePaths?.() };
      let decision: SessionDecision;
      judging = ctx;
      deciding += 1;
      try {
        decision = await session.decide(call, decideOptions);
        assertCurrent();
        if (decision.outcome === "block" && decision.rule === "circuit-breaker" && !breakerAsked) {
          breakerAsked = true;
          if (await askToResume(session, ctx, options.onWaitingForUser)) {
            breakerAsked = false;
            session.resumeAfterBreaker();
            decision = await session.decide(call, decideOptions);
            assertCurrent();
          }
        }
        if (decision.outcome === "block" && decision.rule === "classifier-too-long") {
          if (await askToRun(call, ctx, options.onWaitingForUser)) {
            assertCurrent();
            session.noteApprovedCall(call, ctx.cwd);
            approved();
            return undefined;
          }
        }
      } catch (error) {
        session.noteBlockedCall(call, ctx.cwd);
        // pi blocks a tool whose tool_call handler throws, but the model would get pi's error text instead of the denial contract.
        const render = isSandboxRequest(call) ? sandboxDenialToolResult : denialToolResult;
        return { block: true, reason: credentials.text(render({ action: describeCall(call), reason: failureReason(error), judged: false })) };
      } finally {
        deciding -= 1;
        // Held only while a call is in flight: a ctx from a superseded session generation throws on any use.
        if (deciding === 0) judging = undefined;
      }
      if (decision.outcome === "run") { approved(); return undefined; }
      const denial: AutoModeDenial = credentials.value({
        toolCallId: event.toolCallId,
        tool: call.toolName,
        action: decision.action,
        reason: decision.reason,
        rule: decision.rule,
        at: new Date().toISOString(),
        ...(decision.clearable === false ? { clearable: false } : {}),
        ...(decision.prompt ? { prompt: decision.prompt } : {}),
      });
      // The record first, the report second: the relay may lose a denial, the file may not.
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
      const repeated = denialKey(denial) === lastDeniedKey;
      lastDeniedKey = denialKey(denial);
      showDenial(ctx, denial, repeated);
      paintHeld(ctx, standing);
      return { block: true, reason: credentials.text(decision.toolResult) };
    };
    const checkExecution: ToolExecutionCheck = (event, ctx) => {
      const approval = approvals.get(event.toolCallId);
      approvals.delete(event.toolCallId);
      const call = snapshotCall({ toolCallId: event.toolCallId, toolName: event.toolName, input: event.input });
      if (options.sandboxReviewInExecutor && isSandboxRequest(call)) return;
      if (options.isEnabled?.() === false) {
        session.noteApprovedCall(call, ctx.cwd);
        return;
      }
      if (approval !== executionFingerprint(call, ctx.cwd)) {
        session.noteBlockedCall(call, ctx.cwd);
        throw new Error(credentials.text(denialToolResult({
          action: describeCall(call), incomplete: true,
          reason: renderPrompt("execution-changed", {}, "pi-session"),
        })));
      }
    };
    options.onReady?.(review, checkExecution, () => standing);
    pi.on("tool_call", (event, ctx) => {
      // The protected bash executor validates the scope before asking this same reviewer.
      if (options.sandboxReviewInExecutor && isSandboxRequest({ toolName: event.toolName, input: event.input })) return undefined;
      return review(event, ctx);
    });

    // before_agent_start carries no source of its own, so an extension's own prompt is remembered here for the seam that cannot see it.
    let promptIsUsers = true;
    pi.on("input", (event, ctx) => {
      promptIsUsers = event.source !== "extension";
      if (!promptIsUsers) return;
      session.noteUserInput(event.text);
      breakerAsked = false;
      if (standing.length > 0) {
        standing = [];
        lastDeniedKey = undefined;
        paintHeld(ctx, standing);
      }
    });

    // noteUserInput drops the repeat when a message arrives on both seams.
    pi.on("before_agent_start", (event) => {
      approvals.clear();
      if (promptIsUsers) session.noteUserInput(event.prompt);
      return {};
    });

    // Only assistant TEXT: a toolResult message is the injection surface the classifier's prompt stays out of.
    pi.on("message_end", (event) => {
      if (event.message.role !== "assistant") return;
      session.noteAssistantText(messageText(event.message));
    });

    pi.on("session_compact", () => {
      session.noteCompaction();
    });

    pi.on("tool_result", (event) => {
      approvals.delete(event.toolCallId);
      session.noteToolResult(event.toolCallId, event.isError);
      const held = options.usageLedger?.drain();
      return held ? { usage: mergeUsage(event.usage, held) } : undefined;
    });
  };
}

// pi's UI, when there is one to talk to. `-p` and `--mode json` say so with hasUI,
// and a duck-typed ctx may carry no ui at all.
function uiOf(ctx: AutoModeContextLike | undefined): AutoModeUILike | undefined {
  return ctx?.hasUI === false ? undefined : ctx?.ui;
}

// The denial speaks once, in full, as a chat warning; the footer only counts what is
// held — /auto blocked reads the details on demand, so nothing sits above the composer.
function showDenial(ctx: AutoModeContextLike, denial: AutoModeDenial, repeated: boolean): void {
  const ui = uiOf(ctx);
  if (!ui || repeated) return;
  ui.notify(denialNotice(denial), "warning");
}

// The breaker's one question. No UI is fail-closed on purpose: an unattended run has
// nobody to answer, and the answer is what resumes auto mode.
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
  call: ToolCall,
  ctx: AutoModeContextLike,
  onWaitingForUser?: (waiting: boolean) => void,
): Promise<boolean> {
  const ui = uiOf(ctx);
  if (!ui?.editor) return false;
  const question = tooLongQuestion(describeCall(call));
  announceWaiting(ctx, onWaitingForUser, true);
  try {
    const preview = JSON.stringify(actionEvidence(call, ctx.cwd), null, 2);
    const inspected = await ui.editor("Review pending arguments; submit unchanged to continue", preview);
    if (inspected === undefined) return false;
    if (inspected !== preview) {
      ui.notify("Changes in the preview are not applied. The call was blocked; ask the agent to submit the changed arguments.", "warning");
      return false;
    }
    ctx.signal?.throwIfAborted();
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

// The durable record failed. Said louder than a lost report: nothing else backs it up.
function recordFailure(ctx: AutoModeContextLike, error: unknown): void {
  const message = error instanceof Error ? error.message : String(error);
  uiOf(ctx)?.notify(`auto mode could not write this denial to its local record: ${message}`, "error");
}

function failureReason(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  return `auto mode failed while judging this call, so it refused it: ${message}`;
}
