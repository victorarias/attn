import { describe, expect, test } from "bun:test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { RelayServer, type RelayConnection, type RelayDelegate } from "../src/relay";
import { relayMethods, type RelayDeliverMessageParams, type RelayDeliverMessageResult } from "../src/relay-protocol";
import {
  AttnPiSuite,
  type AgentEndEvent,
  type AgentMessageLike,
  type AgentSettledEvent,
  type AgentStartEvent,
  type ExtensionContextLike,
  type ExtensionHandler,
  type SessionManagerLike,
  type SessionStartEvent,
  type SessionStartReason,
} from "../suite/core";

// Keep filenames short: macOS unix socket paths cap sun_path at 104 bytes.
const tmpRoot = mkdtempSync(join(tmpdir(), "attn-pi-suite-"));
let socketCounter = 0;

function nextSocketPath(): string {
  return join(tmpRoot, `s${socketCounter++}.sock`);
}

async function waitFor(predicate: () => boolean, timeoutMs = 2_000): Promise<void> {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) throw new Error("timed out waiting for condition");
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
}

// Fake pi ExtensionContext: mimics the real one's "stale after session
// replacement" behavior via poison(), and lets tests flip isIdle().
class FakeContext implements ExtensionContextLike {
  private idle = true;
  private poisonMessage: string | undefined;

  constructor(private readonly sessionId: string) {}

  setIdle(idle: boolean): void {
    this.idle = idle;
  }

  /** After this, isIdle()/sessionManager throw, mirroring a ctx from a superseded session generation. */
  poison(message: string): void {
    this.poisonMessage = message;
  }

  isIdle(): boolean {
    if (this.poisonMessage) throw new Error(this.poisonMessage);
    return this.idle;
  }

  get sessionManager(): SessionManagerLike {
    if (this.poisonMessage) throw new Error(this.poisonMessage);
    const sessionId = this.sessionId;
    return { getSessionId: () => sessionId };
  }
}

// Fake pi ExtensionAPI: records registered handlers and sendUserMessage
// calls, and can be poisoned the same way FakeContext can.
class FakePi {
  readonly handlers = new Map<string, ExtensionHandler<any>>();
  readonly sentMessages: Array<{ content: string; options: { deliverAs?: "steer" | "followUp" } | undefined }> = [];
  private poisonMessage: string | undefined;

  on(event: string, handler: ExtensionHandler<any>): void {
    this.handlers.set(event, handler);
  }

  sendUserMessage(content: string, options?: { deliverAs?: "steer" | "followUp" }): void {
    if (this.poisonMessage) throw new Error(this.poisonMessage);
    this.sentMessages.push({ content, options });
  }

  poison(message: string): void {
    this.poisonMessage = message;
  }

  fire(eventType: string, event: unknown, ctx: ExtensionContextLike): void {
    const handler = this.handlers.get(eventType);
    void handler?.(event as never, ctx);
  }
}

// Driver-side harness: a real RelayServer (same one PiDriver uses) with a
// delegate that just records what arrives, instead of PiDriver's actual
// session/token bookkeeping.
class RecordingDelegate implements RelayDelegate {
  readonly calls: Array<{ method: string; params: unknown }> = [];
  readonly connections: RelayConnection[] = [];

  async suiteHello(connection: RelayConnection, params: unknown): Promise<{ ok: true }> {
    this.connections.push(connection);
    this.calls.push({ method: relayMethods.hello, params });
    return { ok: true };
  }

  async suiteReportState(params: unknown): Promise<void> {
    this.calls.push({ method: relayMethods.reportState, params });
  }

  async suiteReportStop(params: unknown): Promise<void> {
    this.calls.push({ method: relayMethods.reportStop, params });
  }

  async suiteReportDenial(params: unknown): Promise<void> {
    this.calls.push({ method: relayMethods.reportDenial, params });
  }
}

// A driver that takes a report and never answers it — what a runtime being
// replaced looks like from inside pi: the bytes are accepted, the process goes
// away before it forwards them, and nothing on the wire says so.
class StallingDelegate extends RecordingDelegate {
  async suiteReportStop(params: unknown): Promise<void> {
    this.calls.push({ method: relayMethods.reportStop, params });
    await new Promise(() => {});
  }
}

async function buildHarness(): Promise<{ relay: RelayServer; delegate: RecordingDelegate; socketPath: string }> {
  const socketPath = nextSocketPath();
  const delegate = new RecordingDelegate();
  const relay = new RelayServer({ socketPath, delegate });
  await relay.listen();
  return { relay, delegate, socketPath };
}

function helloCalls(delegate: RecordingDelegate): unknown[] {
  return delegate.calls.filter((call) => call.method === relayMethods.hello).map((call) => call.params);
}

describe("AttnPiSuite: session_start -> suite.hello", () => {
  test("sends token, pi session id, version, and reason for every transition reason", async () => {
    const { relay, delegate, socketPath } = await buildHarness();
    const suite = new AttnPiSuite({ socketPath, token: "tok-1", piVersion: "0.80.10" });
    const pi = new FakePi();
    suite.register(pi);
    const ctx = new FakeContext("native-session-1");

    const reasons: SessionStartReason[] = ["startup", "reload", "new", "resume", "fork"];
    for (const reason of reasons) {
      const event: SessionStartEvent = { type: "session_start", reason };
      pi.fire("session_start", event, ctx);
    }

    await waitFor(() => helloCalls(delegate).length === reasons.length);
    expect(helloCalls(delegate)).toEqual(
      reasons.map((reason) => ({
        token: "tok-1",
        pi_session_id: "native-session-1",
        pi_version: "0.80.10",
        reason,
      })),
    );

    suite.close();
    relay.close();
  });
});

describe("AttnPiSuite: agent_start -> suite.report_state", () => {
  test("reports working", async () => {
    const { relay, delegate, socketPath } = await buildHarness();
    const suite = new AttnPiSuite({ socketPath, token: "tok-2", piVersion: "0.80.10" });
    const pi = new FakePi();
    suite.register(pi);
    const ctx = new FakeContext("native-2");

    const event: AgentStartEvent = { type: "agent_start" };
    pi.fire("agent_start", event, ctx);

    await waitFor(() => delegate.calls.some((call) => call.method === relayMethods.reportState));
    expect(delegate.calls.find((call) => call.method === relayMethods.reportState)?.params).toEqual({
      token: "tok-2",
      state: "working",
    });

    suite.close();
    relay.close();
  });
});

describe("AttnPiSuite: agent_end + agent_settled -> suite.report_stop", () => {
  test("reports the last assistant message's concatenated text, then clears the cache", async () => {
    const { relay, delegate, socketPath } = await buildHarness();
    const suite = new AttnPiSuite({ socketPath, token: "tok-3", piVersion: "0.80.10" });
    const pi = new FakePi();
    suite.register(pi);
    const ctx = new FakeContext("native-3");

    const messages: AgentMessageLike[] = [
      { role: "user", content: [{ type: "text", text: "do the thing" }] },
      {
        role: "assistant",
        content: [
          { type: "text", text: "Working on it." },
          { type: "toolCall" },
        ],
      },
      { role: "toolResult", content: [{ type: "text", text: "tool output" }] },
      {
        role: "assistant",
        content: [{ type: "thinking" }, { type: "text", text: "Done, want a review?" }],
      },
    ];
    const agentEnd: AgentEndEvent = { type: "agent_end", messages };
    const agentSettled: AgentSettledEvent = { type: "agent_settled" };

    pi.fire("agent_end", agentEnd, ctx);
    pi.fire("agent_settled", agentSettled, ctx);

    await waitFor(() => delegate.calls.filter((call) => call.method === relayMethods.reportStop).length === 1);
    expect(delegate.calls.find((call) => call.method === relayMethods.reportStop)?.params).toEqual({
      token: "tok-3",
      assistant_text: "Done, want a review?",
    });

    // A second settle without a new agent_end has nothing cached.
    pi.fire("agent_settled", agentSettled, ctx);
    await waitFor(() => delegate.calls.filter((call) => call.method === relayMethods.reportStop).length === 2);
    const stops = delegate.calls.filter((call) => call.method === relayMethods.reportStop);
    expect(stops[1]?.params).toEqual({ token: "tok-3", assistant_text: "" });

    suite.close();
    relay.close();
  });

  test("agent_settled with no prior agent_end reports empty assistant_text", async () => {
    const { relay, delegate, socketPath } = await buildHarness();
    const suite = new AttnPiSuite({ socketPath, token: "tok-4", piVersion: "0.80.10" });
    const pi = new FakePi();
    suite.register(pi);
    const ctx = new FakeContext("native-4");

    pi.fire("agent_settled", { type: "agent_settled" }, ctx);

    await waitFor(() => delegate.calls.some((call) => call.method === relayMethods.reportStop));
    expect(delegate.calls.find((call) => call.method === relayMethods.reportStop)?.params).toEqual({
      token: "tok-4",
      assistant_text: "",
    });

    suite.close();
    relay.close();
  });
});

describe("AttnPiSuite: driver.deliver_message", () => {
  test("steers while streaming, delivers plainly while idle, and follows the current context after a session transition", async () => {
    const { relay, delegate, socketPath } = await buildHarness();
    const suite = new AttnPiSuite({ socketPath, token: "tok-5", piVersion: "0.80.10" });

    const pi1 = new FakePi();
    suite.register(pi1);
    const ctx1 = new FakeContext("native-5a");
    pi1.fire("session_start", { type: "session_start", reason: "startup" }, ctx1);
    await waitFor(() => delegate.connections.length >= 1);
    const connection = delegate.connections[0]!;

    ctx1.setIdle(false);
    const streaming = await relay.deliverMessage<RelayDeliverMessageParams, RelayDeliverMessageResult>(
      connection,
      { text: "hey, still there?" },
      2_000,
    );
    expect(streaming).toEqual({ delivered: true });
    expect(pi1.sentMessages).toEqual([{ content: "hey, still there?", options: { deliverAs: "steer" } }]);

    ctx1.setIdle(true);
    const idle = await relay.deliverMessage<RelayDeliverMessageParams, RelayDeliverMessageResult>(
      connection,
      { text: "you around?" },
      2_000,
    );
    expect(idle).toEqual({ delivered: true });
    expect(pi1.sentMessages[1]).toEqual({ content: "you around?", options: undefined });

    // Simulate a session transition: pi re-runs the factory with a fresh
    // pi/ctx pair; the old ones must throw on any further use, same as real
    // pi after resume/fork/new/reload.
    pi1.poison("stale extension ctx after session replacement");
    ctx1.poison("stale extension ctx after session replacement");

    const pi2 = new FakePi();
    suite.register(pi2);
    const ctx2 = new FakeContext("native-5b");
    pi2.fire("session_start", { type: "session_start", reason: "resume" }, ctx2);
    await waitFor(() => helloCalls(delegate).length === 2);

    const afterTransition = await relay.deliverMessage<RelayDeliverMessageParams, RelayDeliverMessageResult>(
      connection,
      { text: "after resume" },
      2_000,
    );
    expect(afterTransition).toEqual({ delivered: true });
    expect(pi2.sentMessages).toEqual([{ content: "after resume", options: undefined }]);
    expect(pi1.sentMessages).toHaveLength(2); // stale pi was never touched again

    suite.close();
    relay.close();
  });
});

describe("AttnPiSuite: auto mode denials -> suite.report_denial", () => {
  const denial = {
    tool: "bash",
    action: "bash: curl https://example.com",
    reason: "the user never asked to reach that host",
    rule: "classifier-2a",
    at: "2026-08-17T10:00:00.000Z",
  };

  test("carries the token, the blocked call, the reason and who decided", async () => {
    const { relay, delegate, socketPath } = await buildHarness();
    const suite = new AttnPiSuite({ socketPath, token: "tok-8", piVersion: "0.80.10" });

    suite.reportDenial(denial);

    await waitFor(() => delegate.calls.some((call) => call.method === relayMethods.reportDenial));
    expect(delegate.calls.find((call) => call.method === relayMethods.reportDenial)?.params).toEqual({
      token: "tok-8",
      ...denial,
    });

    suite.close();
    relay.close();
  });

  test("outside attn it is a no-op, and a dead relay never crashes pi", async () => {
    const bare = new AttnPiSuite({ socketPath: undefined, token: undefined, piVersion: "0.80.10" });
    expect(() => bare.reportDenial(denial)).not.toThrow();
    bare.close();

    const suite = new AttnPiSuite({ socketPath: nextSocketPath(), token: "tok-9", piVersion: "0.80.10" });
    let unhandled: unknown;
    const onUnhandledRejection = (reason: unknown) => {
      unhandled = reason;
    };
    process.on("unhandledRejection", onUnhandledRejection);
    try {
      suite.reportDenial(denial);
      await new Promise((resolve) => setTimeout(resolve, 50));
    } finally {
      process.off("unhandledRejection", onUnhandledRejection);
    }
    expect(unhandled).toBeUndefined();
    suite.close();
  });
});

// The driver process is replaced whenever attn's daemon restarts, and the pi it
// launched keeps running. The relay path is stable per profile precisely so the
// suite can find the replacement; these cover it finding one.
describe("AttnPiSuite: the driver was replaced under a live session", () => {
  test("re-dials the stable path, names its run again, and reports land on the new driver", async () => {
    const socketPath = nextSocketPath();
    const first = new RelayServer({ socketPath, delegate: new RecordingDelegate() });
    await first.listen();

    const suite = new AttnPiSuite({ socketPath, token: "tok-r", piVersion: "0.80.10" });
    const pi = new FakePi();
    suite.register(pi);
    const ctx = new FakeContext("native-r");
    pi.fire("session_start", { type: "session_start", reason: "startup" }, ctx);
    pi.fire("agent_start", { type: "agent_start" }, ctx);
    await new Promise((resolve) => setTimeout(resolve, 50));

    // The replacement driver: same path, new listener, and no memory of the
    // connection the first one held.
    first.close();
    const replacement = new RecordingDelegate();
    const second = new RelayServer({ socketPath, delegate: replacement });
    await second.listen();

    pi.fire("agent_start", { type: "agent_start" }, ctx);

    await waitFor(() => helloCalls(replacement).length >= 1);
    await waitFor(() => replacement.calls.some((call) => call.method === relayMethods.reportState));

    // The hello comes first: a report that overtook it would be answered
    // "unknown pi suite token" and dropped.
    expect(replacement.calls[0]?.method).toBe(relayMethods.hello);
    expect(helloCalls(replacement)[0]).toEqual({
      token: "tok-r",
      pi_session_id: "native-r",
      pi_version: "0.80.10",
      reason: "reconnect",
    });

    suite.close();
    second.close();
  });

  test("reconnects without a report to send, so attn can deliver a message to a quiet session", async () => {
    const socketPath = nextSocketPath();
    const first = new RelayServer({ socketPath, delegate: new RecordingDelegate() });
    await first.listen();

    const suite = new AttnPiSuite({ socketPath, token: "tok-q", piVersion: "0.80.10" });
    const pi = new FakePi();
    suite.register(pi);
    const ctx = new FakeContext("native-q");
    pi.fire("session_start", { type: "session_start", reason: "startup" }, ctx);
    await new Promise((resolve) => setTimeout(resolve, 50));

    first.close();
    const replacement = new RecordingDelegate();
    const second = new RelayServer({ socketPath, delegate: replacement });
    await second.listen();

    // Nothing fires on pi's side: the session is sitting at its prompt. The
    // reconnect is what gives the replacement driver a connection at all.
    await waitFor(() => helloCalls(replacement).length >= 1, 5_000);
    expect(helloCalls(replacement)[0]).toEqual({
      token: "tok-q",
      pi_session_id: "native-q",
      pi_version: "0.80.10",
      reason: "reconnect",
    });

    suite.close();
    second.close();
  });
});

describe("AttnPiSuite: a report the driver never took", () => {
  test("re-declares the settle to the replacement driver", async () => {
    const socketPath = nextSocketPath();
    const stalling = new StallingDelegate();
    const first = new RelayServer({ socketPath, delegate: stalling });
    await first.listen();

    const suite = new AttnPiSuite({ socketPath, token: "tok-x", piVersion: "0.80.10" });
    const pi = new FakePi();
    suite.register(pi);
    const ctx = new FakeContext("native-x");
    pi.fire("session_start", { type: "session_start", reason: "startup" }, ctx);

    const messages: AgentMessageLike[] = [{ role: "assistant", content: [{ type: "text", text: "essay done" }] }];
    pi.fire("agent_end", { type: "agent_end", messages }, ctx);
    pi.fire("agent_settled", { type: "agent_settled" }, ctx);
    await waitFor(() => stalling.calls.some((call) => call.method === relayMethods.reportStop));

    // The driver dies holding the settle. Without the re-declaration attn would
    // show this session working until its next run, minutes or hours later.
    first.close();
    const replacement = new RecordingDelegate();
    const second = new RelayServer({ socketPath, delegate: replacement });
    await second.listen();

    await waitFor(() => replacement.calls.some((call) => call.method === relayMethods.reportStop), 5_000);
    expect(replacement.calls[0]?.method).toBe(relayMethods.hello);
    expect(replacement.calls.find((call) => call.method === relayMethods.reportStop)?.params).toEqual({
      token: "tok-x",
      assistant_text: "essay done",
    });

    suite.close();
    second.close();
  });

  test("keeps trying a socket nothing is listening on yet, and lands the report when a driver appears", async () => {
    const socketPath = nextSocketPath(); // nothing listening
    const suite = new AttnPiSuite({ socketPath, token: "tok-y", piVersion: "0.80.10" });
    const pi = new FakePi();
    suite.register(pi);
    const ctx = new FakeContext("native-y");
    pi.fire("session_start", { type: "session_start", reason: "startup" }, ctx);
    pi.fire("agent_start", { type: "agent_start" }, ctx);
    await new Promise((resolve) => setTimeout(resolve, 50));

    const delegate = new RecordingDelegate();
    const relay = new RelayServer({ socketPath, delegate });
    await relay.listen();

    await waitFor(() => delegate.calls.some((call) => call.method === relayMethods.reportState), 5_000);
    expect(delegate.calls[0]?.method).toBe(relayMethods.hello);
    expect(delegate.calls.find((call) => call.method === relayMethods.reportState)?.params).toEqual({
      token: "tok-y",
      state: "working",
    });

    suite.close();
    relay.close();
  });
});

describe("AttnPiSuite: running outside attn or without a live relay", () => {
  test("missing env registers nothing and makes no connection attempt", () => {
    const suite = new AttnPiSuite({ socketPath: undefined, token: undefined, piVersion: "0.80.10" });
    const pi = new FakePi();
    suite.register(pi);

    expect(pi.handlers.size).toBe(0);

    // Firing events pi never subscribed to must be a pure no-op, not a crash.
    const ctx = new FakeContext("native-6");
    pi.fire("session_start", { type: "session_start", reason: "startup" }, ctx);
    pi.fire("agent_start", { type: "agent_start" }, ctx);

    expect(() => suite.close()).not.toThrow();
  });

  test("a socket with nothing listening never crashes or produces an unhandled rejection", async () => {
    const socketPath = nextSocketPath(); // never listened on
    const suite = new AttnPiSuite({ socketPath, token: "tok-7", piVersion: "0.80.10" });
    const pi = new FakePi();
    suite.register(pi);
    const ctx = new FakeContext("native-7");

    let unhandled: unknown;
    const onUnhandledRejection = (reason: unknown) => {
      unhandled = reason;
    };
    process.on("unhandledRejection", onUnhandledRejection);

    try {
      pi.fire("session_start", { type: "session_start", reason: "startup" }, ctx);
      pi.fire("agent_start", { type: "agent_start" }, ctx);
      pi.fire("agent_end", { type: "agent_end", messages: [] }, ctx);
      pi.fire("agent_settled", { type: "agent_settled" }, ctx);

      // Give the failed dial attempts a tick to settle.
      await new Promise((resolve) => setTimeout(resolve, 50));
    } finally {
      process.off("unhandledRejection", onUnhandledRejection);
    }

    expect(unhandled).toBeUndefined();

    suite.close();
  });
});
