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

type ToolCallHandler = (
  event: ToolCallEventLike,
  ctx: AutoModeContextLike,
) => ToolCallEventResultLike | undefined | Promise<ToolCallEventResultLike | undefined>;

export class FakePi implements AutoModeExtensionAPILike {
  toolCall: ToolCallHandler | undefined;
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
  on(event: string, handler: unknown): void {
    if (event === "tool_call") this.toolCall = handler as ToolCallHandler;
    if (event === "input") this.input = handler as FakePi["input"];
    if (event === "before_agent_start") this.beforeAgentStart = handler as FakePi["beforeAgentStart"];
    if (event === "message_end") this.messageEnd = handler as FakePi["messageEnd"];
    if (event === "tool_result") this.toolResult = handler as FakePi["toolResult"];
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
