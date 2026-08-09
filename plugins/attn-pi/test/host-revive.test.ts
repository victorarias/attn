import { describe, expect, test } from "bun:test";
import {
  TranscriptStore,
  conversationInterrupted,
  launchPromptIsUndelivered,
  parseVerb,
  reconstructTranscript,
  type SessionEntryLike,
  type SnapshotItem,
  type SnapshotToolItem,
} from "../host/envelope";

/**
 * Slice 4: dying and coming back.
 *
 * Two halves are tested here, and they are the two halves the acceptance rests
 * on. `reconstructTranscript` is what makes a revived conversation show its
 * history — including the tool cards and the output behind them — and
 * `TranscriptStore` is what makes a snapshot served to an attaching client agree
 * with what a client that watched the whole stream is holding.
 */

function userEntry(id: string, text: string): SessionEntryLike {
  return { type: "message", id, message: { role: "user", content: [{ type: "text", text }] } };
}

function assistantEntry(id: string, text: string, calls: Array<{ id: string; name: string; args: unknown }> = []): SessionEntryLike {
  return {
    type: "message",
    id,
    message: {
      role: "assistant",
      content: [
        ...(text === "" ? [] : [{ type: "text", text }]),
        ...calls.map((call) => ({ type: "toolCall", id: call.id, name: call.name, arguments: call.args })),
      ],
    },
  };
}

function toolResultEntry(id: string, callID: string, text: string, extra: Record<string, unknown> = {}): SessionEntryLike {
  return {
    type: "message",
    id,
    message: {
      role: "toolResult",
      toolCallId: callID,
      toolName: "bash",
      content: [{ type: "text", text }],
      isError: false,
      ...extra,
    },
  };
}

const tools = (items: SnapshotItem[]): SnapshotToolItem[] =>
  items.filter((item): item is SnapshotToolItem => item.kind === "tool");

describe("reconstructTranscript", () => {
  test("rebuilds messages and tool cards in the order they happened", () => {
    const { items } = reconstructTranscript([
      { type: "thinking_level_change", id: "e0" },
      userEntry("e1", "read the file"),
      assistantEntry("e2", "Looking now.", [{ id: "c1", name: "read", args: { path: "/tmp/a.go" } }]),
      toolResultEntry("e3", "c1", "package main"),
      assistantEntry("e4", "It is a main package."),
    ]);

    expect(items.map((item) => (item.kind === "message" ? `${item.role}:${item.text}` : `tool:${item.name}`))).toEqual([
      "user:read the file",
      "assistant:Looking now.",
      "tool:read",
      "assistant:It is a main package.",
    ]);
    // Namespaced so a revived host minting m1 for its next reply cannot collide
    // with a message that came off disk.
    expect(items[0]!.kind === "message" && items[0]!.id).toBe("h:e1");
  });

  test("a finished call carries its status, files and held output", () => {
    const { items, details } = reconstructTranscript([
      assistantEntry("e1", "", [{ id: "c1", name: "edit", args: { path: "/tmp/a.go" } }]),
      toolResultEntry("e2", "c1", "applied", {
        details: { patch: "--- a\n+++ b\n", fullOutputPath: "/tmp/full.txt", truncation: { truncated: true } },
      }),
    ]);

    const [card] = tools(items);
    expect(card).toMatchObject({
      call_id: "c1",
      name: "edit",
      summary: "/tmp/a.go",
      files: ["/tmp/a.go"],
      status: "ok",
      detail: true,
      patch: true,
      truncated: true,
      full_output: true,
    });
    // The output comes back with the card, so an expand on a revived session
    // answers from memory exactly as it did before the crash.
    expect(details.get("c1")).toEqual({
      text: "applied",
      patch: "--- a\n+++ b\n",
      truncated: true,
      fullOutputPath: "/tmp/full.txt",
    });
  });

  test("a failed call keeps its headline without an expand", () => {
    const { items } = reconstructTranscript([
      assistantEntry("e1", "", [{ id: "c1", name: "bash", args: { command: "false" } }]),
      toolResultEntry("e2", "c1", "exit status 1", { isError: true }),
    ]);
    expect(tools(items)[0]).toMatchObject({ status: "error", error: "exit status 1" });
  });

  test("an empty assistant turn leaves no bubble", () => {
    const { items } = reconstructTranscript([
      assistantEntry("e1", "", [{ id: "c1", name: "ls", args: { path: "." } }]),
    ]);
    expect(items.filter((item) => item.kind === "message")).toEqual([]);
  });

  test("the tool result message itself never becomes a message", () => {
    const { items } = reconstructTranscript([
      assistantEntry("e1", "", [{ id: "c1", name: "bash", args: { command: "seq 1 5000" } }]),
      toolResultEntry("e2", "c1", "1\n2\n3\n"),
    ]);
    expect(items.filter((item) => item.kind === "message")).toEqual([]);
  });
});

describe("conversationInterrupted", () => {
  test("a conversation the agent finished is not interrupted", () => {
    const { items } = reconstructTranscript([
      userEntry("e1", "hello"),
      assistantEntry("e2", "hi"),
    ]);
    expect(conversationInterrupted(items)).toBe(false);
  });

  test("a prompt the agent never answered is interrupted", () => {
    const { items } = reconstructTranscript([userEntry("e1", "do the thing")]);
    expect(conversationInterrupted(items)).toBe(true);
  });

  test("a tool call with no result is interrupted", () => {
    const { items } = reconstructTranscript([
      userEntry("e1", "run it"),
      assistantEntry("e2", "Running.", [{ id: "c1", name: "bash", args: { command: "sleep 47" } }]),
    ]);
    expect(conversationInterrupted(items)).toBe(true);
  });

  test("a tool the agent never replied to is interrupted", () => {
    const { items } = reconstructTranscript([
      assistantEntry("e1", "", [{ id: "c1", name: "bash", args: { command: "ls" } }]),
      toolResultEntry("e2", "c1", "a.go"),
    ]);
    expect(conversationInterrupted(items)).toBe(true);
  });

  test("a conversation nobody has spoken in is not interrupted", () => {
    expect(conversationInterrupted([])).toBe(false);
  });
});

describe("launchPromptIsUndelivered", () => {
  const brief = "Fix the flaky test and report on the ticket.";

  test("a host that reopened nothing still owes the launch its brief", () => {
    // The zero-file early crash: killed before pi's first assistant message, so
    // the replacement opens a session that never heard the brief.
    expect(launchPromptIsUndelivered(brief, [])).toBe(true);
  });

  test("a reopened conversation is never asked the same thing twice", () => {
    const { items } = reconstructTranscript([userEntry("e1", brief), assistantEntry("e2", "On it.")]);
    expect(launchPromptIsUndelivered(brief, items)).toBe(false);
  });

  test("an interrupted conversation already carries the brief, so it is not re-sent", () => {
    // The prompt was persisted and the answer was not. Re-sending would make the
    // agent do the work twice; this reopens as `waiting_input` instead.
    const { items } = reconstructTranscript([userEntry("e1", brief)]);
    expect(conversationInterrupted(items)).toBe(true);
    expect(launchPromptIsUndelivered(brief, items)).toBe(false);
  });

  test("a session launched without a brief is owed nothing", () => {
    expect(launchPromptIsUndelivered("", [])).toBe(false);
    expect(launchPromptIsUndelivered("   ", [])).toBe(false);
  });
});

describe("TranscriptStore", () => {
  test("builds the same transcript the app builds from the same envelopes", () => {
    const store = new TranscriptStore();
    store.apply("run_started", { state: "working" });
    store.apply("message_start", { id: "m1", role: "assistant" });
    store.apply("message_delta", { id: "m1", text: "Look" });
    store.apply("message_delta", { id: "m1", text: "ing." });
    store.apply("tool_started", { call_id: "c1", name: "read", summary: "/tmp/a.go", files: ["/tmp/a.go"] });
    store.apply("tool_finished", {
      call_id: "c1", name: "read", status: "ok", summary: "/tmp/a.go", files: ["/tmp/a.go"],
      detail: true, patch: false, truncated: false, full_output: false,
    });
    store.apply("message_end", { id: "m1", role: "assistant", text: "Looking." });

    const snapshot = store.snapshot();
    expect(snapshot.running).toBe(true);
    expect(snapshot.truncated).toBe(false);
    expect(snapshot.items).toEqual([
      { kind: "message", id: "m1", role: "assistant", text: "Looking.", streaming: false },
      {
        kind: "tool", call_id: "c1", name: "read", summary: "/tmp/a.go", files: ["/tmp/a.go"],
        status: "ok", detail: true, patch: false, truncated: false, full_output: false,
      },
    ]);
  });

  test("a snapshot taken mid-run carries what is still streaming", () => {
    const store = new TranscriptStore();
    store.apply("run_started", { state: "working" });
    store.apply("message_start", { id: "m1", role: "assistant" });
    store.apply("message_delta", { id: "m1", text: "half a th" });
    store.apply("tool_started", { call_id: "c1", name: "bash", summary: "sleep 47", files: [] });

    const snapshot = store.snapshot();
    expect(snapshot.items[0]).toEqual({ kind: "message", id: "m1", role: "assistant", text: "half a th", streaming: true });
    expect(tools(snapshot.items)[0]!.status).toBe("running");
  });

  test("closes what the run left open, the way the app does", () => {
    const store = new TranscriptStore();
    store.apply("run_started", { state: "working" });
    store.apply("message_start", { id: "m1", role: "assistant" });
    store.apply("message_delta", { id: "m1", text: "…" });
    store.apply("tool_started", { call_id: "c1", name: "bash", summary: "sleep 47", files: [] });
    store.apply("run_settled", { state: "idle" });

    const snapshot = store.snapshot();
    expect(snapshot.running).toBe(false);
    expect(snapshot.items[0]).toMatchObject({ streaming: false });
    expect(tools(snapshot.items)[0]).toMatchObject({
      status: "error",
      error: "the run ended before this tool reported",
    });
  });

  test("carries pi's queues so an attaching client draws what is unread", () => {
    const store = new TranscriptStore();
    store.apply("queue_update", { steering: ["stop"], followUp: ["then this"] });
    expect(store.snapshot().queue).toEqual({ steering: ["stop"], followUp: ["then this"] });
  });

  test("drops the oldest items past the window and says it did", () => {
    const store = new TranscriptStore(3, 1 << 20);
    for (const id of ["m1", "m2", "m3", "m4", "m5"]) {
      store.apply("message_end", { id, role: "assistant", text: id });
    }
    const snapshot = store.snapshot();
    expect(snapshot.items.map((item) => (item.kind === "message" ? item.id : ""))).toEqual(["m3", "m4", "m5"]);
    expect(snapshot.total).toBe(5);
    expect(snapshot.truncated).toBe(true);
  });

  test("the byte budget binds before the item budget when text is large", () => {
    const store = new TranscriptStore(100, 64);
    store.apply("message_end", { id: "m1", role: "assistant", text: "x".repeat(80) });
    store.apply("message_end", { id: "m2", role: "assistant", text: "y".repeat(80) });
    const snapshot = store.snapshot();
    // The newest item is never dropped, even alone over budget: a snapshot with
    // nothing in it would be worse than one that names what it clipped.
    expect(snapshot.items).toHaveLength(1);
    expect(snapshot.truncated).toBe(true);
  });

  test("seeding replaces the transcript with reconstructed history", () => {
    const store = new TranscriptStore();
    store.apply("message_end", { id: "m1", role: "assistant", text: "from before" });
    const { items } = reconstructTranscript([userEntry("e1", "hello"), assistantEntry("e2", "hi")]);
    store.seed(items);
    expect(store.snapshot().items.map((item) => (item.kind === "message" ? item.id : ""))).toEqual(["h:e1", "h:e2"]);
    expect(store.snapshot().truncated).toBe(false);
  });
});

describe("the snapshot verb", () => {
  test("is one of the verbs the daemon sends", () => {
    expect(parseVerb('{"verb":"snapshot"}')).toEqual({ verb: "snapshot" });
  });
});
