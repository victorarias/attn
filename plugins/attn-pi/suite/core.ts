// Testable core of the pi-side attn suite: the relay client, pi event
// wiring, and assistant-text extraction. Deliberately duck-typed against
// pi's ExtensionAPI/ExtensionContext shapes (verified against pi v0.80.10
// source, packages/coding-agent/src/core/extensions/types.ts) instead of
// importing pi, so this file loads and runs under `bun test` without a pi
// runtime present. suite/index.ts wires the real pi objects in.
import { createConnection, type Socket } from "node:net";
import {
  relayMethods,
  type RelayDeliverMessageParams,
  type RelayDeliverMessageResult,
} from "../src/relay-protocol";

// ---------------------------------------------------------------------------
// pi shapes this suite depends on (subset of ExtensionAPI / ExtensionContext)
// ---------------------------------------------------------------------------

export type SessionStartReason = "startup" | "reload" | "new" | "resume" | "fork";

export type SessionStartEvent = { type: "session_start"; reason: SessionStartReason; previousSessionFile?: string };
export type AgentStartEvent = { type: "agent_start" };
export type AgentSettledEvent = { type: "agent_settled" };

// Narrowed from pi-ai's (TextContent | ThinkingContent | ToolCall)[]: only
// the "type" discriminant and (for text blocks) "text" matter here.
export type AgentMessageContentBlock = { type: string; text?: string };
export type AgentMessageLike = { role: string; content: AgentMessageContentBlock[] };
export type AgentEndEvent = { type: "agent_end"; messages: AgentMessageLike[] };

export type SessionManagerLike = { getSessionId(): string };

export type ExtensionContextLike = {
  isIdle(): boolean;
  readonly sessionManager: SessionManagerLike;
};

export type ExtensionHandler<TEvent> = (event: TEvent, ctx: ExtensionContextLike) => void | Promise<void>;

// Only the pi.on() overloads and pi.sendUserMessage() this suite calls.
export type ExtensionAPILike = {
  on(event: "session_start", handler: ExtensionHandler<SessionStartEvent>): void;
  on(event: "agent_start", handler: ExtensionHandler<AgentStartEvent>): void;
  on(event: "agent_end", handler: ExtensionHandler<AgentEndEvent>): void;
  on(event: "agent_settled", handler: ExtensionHandler<AgentSettledEvent>): void;
  sendUserMessage(content: string, options?: { deliverAs?: "steer" | "followUp" }): void;
};

// ---------------------------------------------------------------------------
// Relay client: ndjson JSON-RPC 2.0 over a unix socket, suite side. Mirrors
// the framing in ../src/attn-rpc.ts and ../src/relay.ts's RelayConnection,
// but this connection dials out (suite -> driver) instead of accepting.
// ---------------------------------------------------------------------------

type JSONRPCID = number | string;
type JSONRPCRequest = { jsonrpc: "2.0"; id: JSONRPCID; method: string; params?: unknown };
type JSONRPCResponse = { jsonrpc: "2.0"; id: JSONRPCID; result?: unknown; error?: { code: number; message: string } };
type Pending = { resolve: (result: unknown) => void; reject: (error: Error) => void };

// Generous: suite.report_stop's driver-side handler runs an LLM
// classification before answering, which can take a while.
const suiteRequestTimeoutMs = 60_000;

export class RelaySuiteClient {
  private socket: Socket | undefined;
  private connecting: Promise<Socket> | undefined;
  private buffer = "";
  private nextID = 1;
  private readonly pending = new Map<string, Pending>();

  constructor(
    private readonly socketPath: string,
    private readonly onDeliverMessage: (params: RelayDeliverMessageParams) => Promise<RelayDeliverMessageResult>,
  ) {}

  /**
   * Best-effort send: never throws. The driver's PTY-exit liveness is the
   * authoritative signal for attn; suite reports ride on top of that and are
   * dropped silently on any relay failure (no connection yet, dial refused,
   * request timeout, ...). Reconnects lazily here rather than eagerly or on
   * a retry loop.
   */
  async send(method: string, params: unknown): Promise<void> {
    try {
      const socket = await this.ensureConnected();
      await this.request(socket, method, params);
    } catch {
      // Swallowed by design; see the doc comment above.
    }
  }

  /** Test-only: release the socket so bun test doesn't hang on open handles. */
  close(): void {
    this.socket?.destroy();
    this.socket = undefined;
    this.failPending(new Error("suite relay connection closed"));
  }

  private ensureConnected(): Promise<Socket> {
    if (this.socket && !this.socket.destroyed) return Promise.resolve(this.socket);
    if (!this.connecting) {
      this.connecting = this.dial().finally(() => {
        this.connecting = undefined;
      });
    }
    return this.connecting;
  }

  private dial(): Promise<Socket> {
    return new Promise((resolve, reject) => {
      const socket = createConnection({ path: this.socketPath });
      socket.once("error", reject);
      socket.once("connect", () => {
        socket.off("error", reject);
        socket.setEncoding("utf8");
        socket.on("data", (chunk) => this.consume(chunk));
        socket.on("error", (error) => this.failPending(error));
        socket.on("close", () => {
          if (this.socket === socket) this.socket = undefined;
          this.failPending(new Error("suite relay connection closed"));
        });
        this.socket = socket;
        resolve(socket);
      });
    });
  }

  private request(socket: Socket, method: string, params: unknown): Promise<unknown> {
    const id = this.nextID++;
    const result = new Promise<unknown>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(String(id));
        reject(new Error(`relay did not respond to ${method} within ${suiteRequestTimeoutMs}ms`));
      }, suiteRequestTimeoutMs);
      this.pending.set(String(id), {
        resolve: (value) => {
          clearTimeout(timer);
          resolve(value);
        },
        reject: (error) => {
          clearTimeout(timer);
          reject(error);
        },
      });
    });
    socket.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
    return result;
  }

  private consume(chunk: string): void {
    this.buffer += chunk;
    for (;;) {
      const end = this.buffer.indexOf("\n");
      if (end < 0) return;
      const line = this.buffer.slice(0, end).trim();
      this.buffer = this.buffer.slice(end + 1);
      if (line === "") continue;
      void this.route(JSON.parse(line) as JSONRPCRequest | JSONRPCResponse);
    }
  }

  private async route(message: JSONRPCRequest | JSONRPCResponse): Promise<void> {
    if ("method" in message) {
      await this.respond(message);
      return;
    }
    const pending = this.pending.get(String(message.id));
    if (!pending) return;
    this.pending.delete(String(message.id));
    if (message.error) {
      pending.reject(new Error(message.error.message));
      return;
    }
    pending.resolve(message.result);
  }

  private async respond(request: JSONRPCRequest): Promise<void> {
    if (request.method !== relayMethods.deliverMessage) {
      this.send_(request.id, { error: { code: -32601, message: `unknown method ${request.method}` } });
      return;
    }
    try {
      const result = await this.onDeliverMessage(request.params as RelayDeliverMessageParams);
      this.send_(request.id, { result });
    } catch (error) {
      this.send_(request.id, { error: { code: -32603, message: error instanceof Error ? error.message : String(error) } });
    }
  }

  private send_(id: JSONRPCID, outcome: { result: unknown } | { error: { code: number; message: string } }): void {
    if (!this.socket) return;
    this.socket.write(`${JSON.stringify({ jsonrpc: "2.0", id, ...outcome })}\n`);
  }

  private failPending(error: Error): void {
    for (const pending of this.pending.values()) pending.reject(error);
    this.pending.clear();
  }
}

// ---------------------------------------------------------------------------
// Suite core: pi event wiring + assistant-text caching + message delivery.
// ---------------------------------------------------------------------------

export type SuiteEnv = {
  /** ATTN_PI_SUITE_SOCKET, untrimmed. Undefined/blank means "not running under attn". */
  socketPath: string | undefined;
  /** ATTN_PI_TOKEN, untrimmed. Undefined/blank means "not running under attn". */
  token: string | undefined;
  /** pi's own VERSION, resolved by the caller (index.ts imports it from pi; tests inject a fixed string). */
  piVersion: string;
};

export class AttnPiSuite {
  private readonly piVersion: string;
  // Undefined means "running outside attn" (no ATTN_PI_SUITE_SOCKET/ATTN_PI_TOKEN):
  // register() becomes a complete no-op and no dial is ever attempted.
  private readonly relay: { client: RelaySuiteClient; token: string } | undefined;

  // Rebound on every register() call (one per pi extension factory run, i.e.
  // once per session_start/resume/fork/new/reload). `relay` above is the only
  // piece of state that must outlive a single factory run.
  private currentPi: ExtensionAPILike | undefined;
  private currentContext: ExtensionContextLike | undefined;

  // agent_end caches the last assistant message's text; agent_settled has no
  // payload of its own, so this is the only way to get text to suite.report_stop.
  private cachedAssistantText = "";

  constructor(env: SuiteEnv) {
    this.piVersion = env.piVersion;
    const socketPath = env.socketPath?.trim();
    const token = env.token?.trim();
    this.relay =
      socketPath && token ? { client: new RelaySuiteClient(socketPath, this.handleDeliverMessage), token } : undefined;
  }

  /**
   * Wires this suite's event handlers onto one pi extension factory run.
   * Safe to call again after a session transition (resume/fork/new/reload):
   * pi re-runs the factory each time, and the previous ctx/pi throw on any
   * use, so this call registers fresh handlers against the new ones. The
   * relay client itself is not recreated here — it lives on `this` and
   * survives across calls.
   */
  register(pi: ExtensionAPILike): void {
    const relay = this.relay;
    if (!relay) return; // constructor already decided we're a no-op
    this.currentPi = pi;

    pi.on("session_start", (event, ctx) => {
      this.currentContext = ctx;
      void relay.client.send(relayMethods.hello, {
        token: relay.token,
        pi_session_id: ctx.sessionManager.getSessionId(),
        pi_version: this.piVersion,
        reason: event.reason,
      });
    });

    pi.on("agent_start", (_event, ctx) => {
      this.currentContext = ctx;
      void relay.client.send(relayMethods.reportState, { token: relay.token, state: "working" });
    });

    pi.on("agent_end", (event, ctx) => {
      this.currentContext = ctx;
      this.cachedAssistantText = lastAssistantText(event.messages);
    });

    pi.on("agent_settled", (_event, ctx) => {
      this.currentContext = ctx;
      const assistantText = this.cachedAssistantText;
      this.cachedAssistantText = "";
      void relay.client.send(relayMethods.reportStop, { token: relay.token, assistant_text: assistantText });
    });
  }

  /** Test-only: release the relay socket. */
  close(): void {
    this.relay?.client.close();
  }

  private readonly handleDeliverMessage = async (
    params: RelayDeliverMessageParams,
  ): Promise<RelayDeliverMessageResult> => {
    const pi = this.currentPi;
    const ctx = this.currentContext;
    if (!pi || !ctx) return { delivered: false }; // no live pi context yet
    try {
      pi.sendUserMessage(params.text, ctx.isIdle() ? undefined : { deliverAs: "steer" });
      return { delivered: true };
    } catch {
      // A stale pi/ctx from a superseded session generation throws here on
      // any use; that is an ordinary "can't deliver right now" outcome for
      // the driver, not a suite bug, so it is not rethrown across the wire.
      return { delivered: false };
    }
  };
}

function lastAssistantText(messages: AgentMessageLike[]): string {
  for (let i = messages.length - 1; i >= 0; i--) {
    const message = messages[i];
    if (message?.role !== "assistant") continue;
    return message.content
      .filter((block) => block.type === "text" && typeof block.text === "string")
      .map((block) => block.text)
      .join("\n");
  }
  return "";
}
