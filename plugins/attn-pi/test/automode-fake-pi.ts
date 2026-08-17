// Fake pi ExtensionAPI: records the handlers auto mode's factory registers so
// a test can fire the events pi would, and nothing else.
import type {
  AutoModeContextLike,
  AutoModeExtensionAPILike,
  BeforeAgentStartEventLike,
  BeforeAgentStartResultLike,
  InputEventLike,
  MessageEndEventLike,
  MessageLike,
  ToolCallEventLike,
  ToolCallEventResultLike,
  ToolResultEventLike,
  ToolResultEventResultLike,
} from "../automode/index";
import type { AutoModePiLike, AutoModeSessionContextLike, SessionStartEventLike } from "../automode/mode";
import type { AutoModeUILike } from "../automode/ui";

type ToolCallHandler = (
  event: ToolCallEventLike,
  ctx: AutoModeContextLike,
) => ToolCallEventResultLike | undefined | Promise<ToolCallEventResultLike | undefined>;

export class FakePi implements AutoModeExtensionAPILike, AutoModePiLike {
  toolCall: ToolCallHandler | undefined;
  sessionStart: ((event: SessionStartEventLike, ctx: AutoModeSessionContextLike) => void) | undefined;
  /** name -> handler, as pi's command registry holds them. */
  readonly commands = new Map<string, (args: string, ctx: AutoModeContextLike) => Promise<void> | void>();
  /** Registered flags, and the values a launch passed. */
  readonly registeredFlags = new Set<string>();
  readonly flagValues = new Map<string, boolean | string>();
  input: ((event: InputEventLike, ctx: AutoModeContextLike) => void) | undefined;
  beforeAgentStart:
    | ((event: BeforeAgentStartEventLike, ctx: AutoModeContextLike) => BeforeAgentStartResultLike)
    | undefined;
  messageEnd: ((event: MessageEndEventLike, ctx: AutoModeContextLike) => void) | undefined;
  toolResult:
    | ((event: ToolResultEventLike, ctx: AutoModeContextLike) => ToolResultEventResultLike | undefined)
    | undefined;

  on(event: "tool_call", handler: ToolCallHandler): void;
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
  on(
    event: "session_start",
    handler: (event: SessionStartEventLike, ctx: AutoModeSessionContextLike) => void,
  ): void;
  on(event: string, handler: unknown): void {
    if (event === "tool_call") this.toolCall = handler as ToolCallHandler;
    if (event === "input") this.input = handler as FakePi["input"];
    if (event === "before_agent_start") this.beforeAgentStart = handler as FakePi["beforeAgentStart"];
    if (event === "message_end") this.messageEnd = handler as FakePi["messageEnd"];
    if (event === "tool_result") this.toolResult = handler as FakePi["toolResult"];
    if (event === "session_start") this.sessionStart = handler as FakePi["sessionStart"];
  }

  registerCommand(
    name: string,
    options: { description?: string; handler: (args: string, ctx: AutoModeContextLike) => Promise<void> | void },
  ): void {
    this.commands.set(name, options.handler);
  }

  registerFlag(name: string): void {
    this.registeredFlags.add(name);
  }

  getFlag(name: string): boolean | string | undefined {
    return this.registeredFlags.has(name) ? this.flagValues.get(name) : undefined;
  }

  /** What pi does with `--<name>` on a boolean flag: sets it true. */
  pass(flag: string): this {
    this.flagValues.set(flag, true);
    return this;
  }

  start(ctx: AutoModeSessionContextLike): void {
    this.sessionStart?.({ type: "session_start" }, ctx);
  }

  async run(command: string, args: string, ctx: AutoModeContextLike): Promise<void> {
    await this.commands.get(command)?.(args, ctx);
  }

  /**
   * One turn of the conversation reaching the extension the way pi delivers
   * it: a prompt rides the input seam and the before_agent_start seam, in that
   * order, from the same prompt() call.
   */
  say(user: string, assistant?: string, source: InputEventLike["source"] = "interactive"): void {
    this.input?.({ type: "input", text: user, source }, ctx);
    this.beforeAgentStart?.({ type: "before_agent_start", prompt: user, systemPrompt: "base" }, ctx);
    if (assistant !== undefined) {
      this.messageEnd?.({ type: "message_end", message: assistantMessage(assistant) }, ctx);
    }
  }
}

export const ctx: AutoModeContextLike = { cwd: "/work/repo" };

/** pi's ExtensionUIContext, recorded rather than drawn. */
export class FakeUI implements AutoModeUILike {
  readonly statuses = new Map<string, string | undefined>();
  readonly widgets = new Map<string, string[] | undefined>();
  readonly notices: { message: string; type?: string }[] = [];
  /** Every setWorkingMessage call in order; undefined is "back to pi's own". */
  readonly workingMessages: (string | undefined)[] = [];
  readonly questions: { title: string; message: string }[] = [];
  answer = false;

  setStatus(key: string, text: string | undefined): void {
    this.statuses.set(key, text);
  }

  setWorkingMessage(message?: string): void {
    this.workingMessages.push(message);
  }

  notify(message: string, type?: "info" | "warning" | "error"): void {
    this.notices.push({ message, type });
  }

  setWidget(key: string, content: string[] | undefined): void {
    this.widgets.set(key, content);
  }

  async confirm(title: string, message: string): Promise<boolean> {
    this.questions.push({ title, message });
    return this.answer;
  }
}

export function uiContext(
  ui: AutoModeUILike,
  overrides: Partial<AutoModeSessionContextLike> = {},
): AutoModeSessionContextLike {
  return { cwd: "/work/repo", hasUI: true, ui, ...overrides };
}

export function toolCall(
  toolName: string,
  input: Record<string, unknown>,
  toolCallId = "call-1",
): ToolCallEventLike {
  return { type: "tool_call", toolCallId, toolName, input };
}

export function userInput(text: string): InputEventLike {
  return { type: "input", text, source: "interactive" };
}

export function assistantMessage(text: string): MessageLike {
  return { role: "assistant", content: [{ type: "text", text }] };
}
