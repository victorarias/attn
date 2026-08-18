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

/**
 * Why this hello is being said. pi's own session-transition reasons, plus
 * `reconnect` — the relay connection was re-established under a session that
 * never transitioned, so nothing about the session changed and only the channel
 * did. The driver treats them alike; the value is here so a log of one end reads
 * against the other.
 */
export type RelayHelloReason = SessionStartReason | "reconnect";

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

// A state report waiting to be acknowledged. Only the newest one is kept: a
// report says what the session IS, so an older one that never landed has
// nothing left to add.
type RetainedReport = { method: string; params: unknown; inFlight: boolean };

// Generous: suite.report_stop's driver-side handler runs an LLM
// classification before answering, which can take a while.
const suiteRequestTimeoutMs = 60_000;

// A dropped relay connection means the driver process went away, and the
// replacement one is up about a second later (measured on attn's own daemon
// restarts: the runtime exits and re-registers inside 1s). So the first retry is
// well under that and the ceiling only bounds a driver that is not coming back —
// a dial at a missing unix socket is one ENOENT, and this loop runs solely while
// disconnected, stopping the moment it connects.
const reconnectMinDelayMs = 500;
const reconnectMaxDelayMs = 30_000;

export class RelaySuiteClient {
  private socket: Socket | undefined;
  private connecting: Promise<Socket> | undefined;
  private buffer = "";
  private nextID = 1;
  private readonly pending = new Map<string, Pending>();
  private reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  private reconnectDelayMs = reconnectMinDelayMs;
  private closed = false;
  // Whether the connection currently open has already named its run. Cleared on
  // every dial, and by announce() when the run's identity changed under a
  // connection that stayed up.
  private announced = false;
  // The newest state report the driver has not acknowledged, re-sent once the
  // channel is back. See report().
  private retained: RetainedReport | undefined;

  constructor(
    private readonly socketPath: string,
    private readonly onDeliverMessage: (params: RelayDeliverMessageParams) => Promise<RelayDeliverMessageResult>,
    /**
     * Hello params for this run as it stands right now, or undefined when no pi
     * context has been seen yet. This is the reconnect path's hello: a caller
     * announcing a transition passes its own. The driver binds a connection to a
     * run when the hello arrives, so a re-established connection that skipped it
     * would leave attn with a live pi it can report for but cannot deliver a
     * message to.
     */
    private readonly helloParams: () => unknown | undefined,
  ) {}

  /**
   * Names this run on the relay, opening a connection if there is none. Called
   * when the run's identity changes — a pi session transition mints a new native
   * session id — and by the reconnect path, which has a live pi to re-announce
   * and no report of its own to carry.
   */
  announce(params: unknown): void {
    this.ensureConnected()
      .then((socket) => {
        this.writeHello(socket, params);
        const retained = this.retained;
        if (retained && !retained.inFlight) void this.deliver(retained);
      })
      .catch(() => {
        // ensureConnected already scheduled the next attempt.
      });
  }

  /**
   * Declares this session's state, and keeps declaring it until the driver
   * takes it. A report can be lost in ways nothing here can see — the driver
   * process exits between accepting the bytes and forwarding them, which is
   * what an attn daemon restart does to whatever was in flight — and a lost one
   * leaves attn showing a state the session left minutes ago. Only the newest
   * report is retained, because that is the only one that is still true.
   */
  report(method: string, params: unknown): void {
    const entry: RetainedReport = { method, params, inFlight: false };
    this.retained = entry;
    void this.deliver(entry);
  }

  private async deliver(entry: RetainedReport): Promise<void> {
    entry.inFlight = true;
    try {
      const socket = await this.ensureConnected();
      this.sayHello(socket);
      await this.request(socket, entry.method, entry.params);
      // Only this entry is retired: a newer report may have taken the slot
      // while this one was in flight, and it has not been acknowledged.
      if (this.retained === entry) this.retained = undefined;
    } catch {
      // Stays retained. The socket's close schedules a reconnect, and the
      // hello that follows flushes it.
    } finally {
      entry.inFlight = false;
    }
  }

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
      // A report on a connection that has not named its run is answered "unknown
      // token", so the hello goes first — including on a connection this client
      // re-established under a session that never transitioned.
      this.sayHello(socket);
      await this.request(socket, method, params);
    } catch {
      // Swallowed by design; see the doc comment above.
    }
  }

  /** Test-only: release the socket so bun test doesn't hang on open handles. */
  close(): void {
    this.closed = true;
    if (this.reconnectTimer !== undefined) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = undefined;
    }
    this.socket?.destroy();
    this.socket = undefined;
    this.failPending(new Error("suite relay connection closed"));
  }

  private ensureConnected(): Promise<Socket> {
    if (this.socket && !this.socket.destroyed) return Promise.resolve(this.socket);
    if (!this.connecting) {
      this.connecting = this.dial()
        .catch((error: unknown) => {
          // A dial that never connects emits no close, so this is the only
          // place that can keep trying — and something has to: attn delivers
          // messages over this channel, so a session with nothing to report
          // still needs it back.
          this.scheduleReconnect();
          throw error;
        })
        .finally(() => {
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
          this.scheduleReconnect();
        });
        this.socket = socket;
        this.announced = false;
        this.reconnectDelayMs = reconnectMinDelayMs;
        resolve(socket);
      });
    });
  }

  /**
   * Re-dials after the driver went away. attn's own PTY-exit liveness stays the
   * authoritative signal for whether this session is alive; this only restores
   * the channel a live session declares its state on, and does nothing at all
   * while connected.
   */
  private scheduleReconnect(): void {
    if (this.closed || this.reconnectTimer !== undefined) return;
    const delay = this.reconnectDelayMs;
    this.reconnectDelayMs = Math.min(delay * 2, reconnectMaxDelayMs);
    const timer = setTimeout(() => {
      this.reconnectTimer = undefined;
      // Announce, not a bare dial: the point of reconnecting without a report to
      // send is to give the driver a connection it can deliver a message on, and
      // it only binds one when the run is named.
      const params = this.helloParams();
      if (params === undefined) return;
      this.announce(params);
    }, delay);
    // Never the reason pi stays alive.
    timer.unref?.();
    this.reconnectTimer = timer;
  }

  /**
   * The hello a report needs in front of it, said at most once per connection.
   * A caller with something new to announce goes through announce() instead;
   * this one exists only so a report never arrives at a driver that cannot
   * place it.
   */
  private sayHello(socket: Socket): void {
    if (this.announced) return;
    const params = this.helloParams();
    if (params !== undefined) this.writeHello(socket, params);
  }

  /**
   * Not awaited by its callers: the driver's answer carries nothing this client
   * needs, and waiting for one would hold every report behind a driver that is
   * slow to reply. Ordering is what matters, and writing it first gives that.
   */
  private writeHello(socket: Socket, params: unknown): void {
    this.announced = true;
    this.request(socket, relayMethods.hello, params).catch(() => {});
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

/**
 * What travels to attn when auto mode refuses a call — the subset of
 * `automode/index.ts`'s AutoModeDenial that leaves the session. The tool-call
 * id stays behind: it addresses nothing outside pi's own run.
 */
export type SuiteDenial = {
  tool: string;
  action: string;
  reason: string;
  rule: string;
  at: string;
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
      socketPath && token
        ? {
            client: new RelaySuiteClient(socketPath, this.handleDeliverMessage, () => this.helloParams("reconnect")),
            token,
          }
        : undefined;
  }

  /**
   * The hello for the run this suite currently represents, or undefined before
   * any pi context has been seen — a connection opened that early cannot name a
   * session yet, and the next dial (or the next session_start) says it instead.
   */
  private helloParams(reason: RelayHelloReason): Record<string, unknown> | undefined {
    const relay = this.relay;
    const ctx = this.currentContext;
    if (!relay || !ctx) return undefined;
    return {
      token: relay.token,
      pi_session_id: ctx.sessionManager.getSessionId(),
      pi_version: this.piVersion,
      reason,
    };
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

    // A transition mints a new pi session id on a connection that may already be
    // open, and the driver has to hear the new one: the relay client survives
    // transitions, the identity it announced does not.
    pi.on("session_start", (event, ctx) => {
      this.currentContext = ctx;
      const params = this.helloParams(event.reason);
      if (params) relay.client.announce(params);
    });

    pi.on("agent_start", (_event, ctx) => {
      this.currentContext = ctx;
      relay.client.report(relayMethods.reportState, { token: relay.token, state: "working" });
    });

    pi.on("agent_end", (event, ctx) => {
      this.currentContext = ctx;
      this.cachedAssistantText = lastAssistantText(event.messages);
    });

    pi.on("agent_settled", (_event, ctx) => {
      this.currentContext = ctx;
      const assistantText = this.cachedAssistantText;
      this.cachedAssistantText = "";
      relay.client.report(relayMethods.reportStop, { token: relay.token, assistant_text: assistantText });
    });
  }

  /**
   * Reports one refused call to attn. Like every other suite report this is
   * fire-and-forget: a bare pi (no relay) drops it, and so does a relay that
   * will not answer. Auto mode's decision has already been made and given to
   * the model; nothing here can change it.
   */
  reportDenial(denial: SuiteDenial): void {
    const relay = this.relay;
    if (!relay) return;
    void relay.client.send(relayMethods.reportDenial, {
      token: relay.token,
      tool: denial.tool,
      action: denial.action,
      reason: denial.reason,
      rule: denial.rule,
      at: denial.at,
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
