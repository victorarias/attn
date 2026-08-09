import { describe, expect, test } from "bun:test";
import {
  DeltaCoalescer,
  EnvelopeStream,
  PiEventMapper,
  SUMMARY_LIMIT,
  ToolDetailStore,
  messageText,
  parseVerb,
  toolFiles,
  toolResultText,
  toolSummary,
  type Envelope,
} from "../host/envelope";

/** A hand-driven clock so window behavior is asserted, never waited on. */
function manualScheduler() {
  const scheduled: Array<{ id: number; fn: () => void }> = [];
  let nextID = 1;
  return {
    schedule: (fn: () => void) => {
      const id = nextID++;
      scheduled.push({ id, fn });
      return id;
    },
    cancel: (handle: unknown) => {
      const index = scheduled.findIndex((entry) => entry.id === handle);
      if (index >= 0) scheduled.splice(index, 1);
    },
    /** Fires every pending window, as a real timer eventually would. */
    tick: () => {
      const due = scheduled.splice(0, scheduled.length);
      for (const entry of due) entry.fn();
    },
    pending: () => scheduled.length,
  };
}

function harness() {
  const emitted: Envelope[] = [];
  const clock = manualScheduler();
  const stream = new EnvelopeStream("attn-session-1", (envelope) => emitted.push(envelope));
  const unknown: string[] = [];
  const deltas = new DeltaCoalescer(
    30,
    (id, text) => stream.emit("message_delta", { id, text }),
    clock.schedule,
    clock.cancel,
  );
  const details = new ToolDetailStore(1 << 20);
  const mapper = new PiEventMapper(stream, deltas, (type) => unknown.push(type), details);
  return { emitted, clock, mapper, unknown, details };
}

const assistant = (text: string) => ({ role: "assistant", content: [{ type: "text", text }] });

describe("EnvelopeStream", () => {
  test("stamps the session id and a strictly increasing seq", () => {
    const emitted: Envelope[] = [];
    const stream = new EnvelopeStream("s1", (envelope) => emitted.push(envelope));
    stream.emit("run_started", {});
    stream.emit("run_settled", {});
    expect(emitted.map((e) => [e.session_id, e.seq, e.kind])).toEqual([
      ["s1", 1, "run_started"],
      ["s1", 2, "run_settled"],
    ]);
  });
});

describe("DeltaCoalescer", () => {
  test("batches every delta in a window into one emission", () => {
    const clock = manualScheduler();
    const flushed: Array<[string, string]> = [];
    const deltas = new DeltaCoalescer(30, (id, text) => flushed.push([id, text]), clock.schedule, clock.cancel);

    for (const piece of ["Hel", "lo ", "wor", "ld"]) deltas.push("m1", piece);
    expect(flushed).toEqual([]);
    expect(clock.pending()).toBe(1); // one window for four deltas, not four

    clock.tick();
    expect(flushed).toEqual([["m1", "Hello world"]]);
  });

  test("opens a fresh window after a flush instead of going silent", () => {
    const clock = manualScheduler();
    const flushed: Array<[string, string]> = [];
    const deltas = new DeltaCoalescer(30, (id, text) => flushed.push([id, text]), clock.schedule, clock.cancel);

    deltas.push("m1", "a");
    clock.tick();
    deltas.push("m1", "b");
    clock.tick();

    expect(flushed).toEqual([
      ["m1", "a"],
      ["m1", "b"],
    ]);
  });

  test("an explicit flush cancels the pending window so text is not emitted twice", () => {
    const clock = manualScheduler();
    const flushed: Array<[string, string]> = [];
    const deltas = new DeltaCoalescer(30, (id, text) => flushed.push([id, text]), clock.schedule, clock.cancel);

    deltas.push("m1", "a");
    deltas.flush();
    clock.tick();

    expect(flushed).toEqual([["m1", "a"]]);
    expect(clock.pending()).toBe(0);
  });

  test("keeps messages apart when two are pending in the same window", () => {
    const clock = manualScheduler();
    const flushed: Array<[string, string]> = [];
    const deltas = new DeltaCoalescer(30, (id, text) => flushed.push([id, text]), clock.schedule, clock.cancel);

    deltas.push("m1", "one");
    deltas.push("m2", "two");
    clock.tick();

    expect(flushed).toEqual([
      ["m1", "one"],
      ["m2", "two"],
    ]);
  });
});

describe("PiEventMapper", () => {
  test("maps a whole run to the semantic and render kinds attn expects", () => {
    const { emitted, clock, mapper } = harness();

    mapper.handle({ type: "agent_start" });
    mapper.handle({ type: "message_start", message: assistant("") });
    mapper.handle({ type: "message_update", assistantMessageEvent: { type: "text_delta", delta: "Hi" } });
    mapper.handle({ type: "message_update", assistantMessageEvent: { type: "text_delta", delta: " there" } });
    clock.tick();
    mapper.handle({ type: "message_end", message: assistant("Hi there") });
    mapper.handle({ type: "agent_settled" });

    expect(emitted.map((e) => e.kind)).toEqual([
      "run_started",
      "message_start",
      "message_delta",
      "message_end",
      "run_settled",
    ]);
    expect(emitted[2].body).toEqual({ id: "m1", text: "Hi there" });
    expect(emitted[3].body).toEqual({ id: "m1", role: "assistant", text: "Hi there" });
    expect(emitted.map((e) => e.seq)).toEqual([1, 2, 3, 4, 5]);
  });

  test("every declaration carries the attn state it puts the session in", () => {
    const { emitted, mapper } = harness();

    mapper.handle({ type: "agent_start" });
    mapper.handle({ type: "agent_settled" });

    expect(emitted.map((e) => [e.kind, (e.body as { state?: string }).state])).toEqual([
      ["run_started", "working"],
      ["run_settled", "idle"],
    ]);
  });

  test("surfaces both of pi's queues, and the drain that empties them", () => {
    const { emitted, mapper } = harness();

    mapper.handle({ type: "queue_update", steering: ["stop and look at x"], followUp: [] });
    // pi removes the entry and re-announces the queue immediately before the
    // user message that is the agent reading it — queued, then seen.
    mapper.handle({ type: "queue_update", steering: [], followUp: [] });
    mapper.handle({ type: "message_start", message: { role: "user", content: "stop and look at x" } });
    mapper.handle({ type: "message_end", message: { role: "user", content: "stop and look at x" } });

    expect(emitted.map((e) => e.kind)).toEqual([
      "queue_update",
      "queue_update",
      "message_start",
      "message_end",
    ]);
    expect(emitted[0].body).toEqual({ steering: ["stop and look at x"], followUp: [] });
    expect(emitted[1].body).toEqual({ steering: [], followUp: [] });
  });

  test("flushes pending text before a queue update so the queue never overtakes the reply", () => {
    const { emitted, mapper } = harness();

    mapper.handle({ type: "message_start", message: assistant("") });
    mapper.handle({ type: "message_update", assistantMessageEvent: { type: "text_delta", delta: "working on it" } });
    mapper.handle({ type: "queue_update", steering: ["one more thing"], followUp: [] });

    expect(emitted.map((e) => e.kind)).toEqual(["message_start", "message_delta", "queue_update"]);
  });

  test("flushes pending text before message_end so the pane never sees the end first", () => {
    const { emitted, mapper } = harness();

    mapper.handle({ type: "message_start", message: assistant("") });
    mapper.handle({ type: "message_update", assistantMessageEvent: { type: "text_delta", delta: "unflushed" } });
    mapper.handle({ type: "message_end", message: assistant("unflushed") });

    expect(emitted.map((e) => e.kind)).toEqual(["message_start", "message_delta", "message_end"]);
    expect(emitted[1].body).toEqual({ id: "m1", text: "unflushed" });
  });

  test("flushes pending text before run_settled", () => {
    const { emitted, mapper } = harness();

    mapper.handle({ type: "message_start", message: assistant("") });
    mapper.handle({ type: "message_update", assistantMessageEvent: { type: "text_delta", delta: "tail" } });
    mapper.handle({ type: "agent_settled" });

    expect(emitted.map((e) => e.kind)).toEqual(["message_start", "message_delta", "run_settled"]);
  });

  test("streams assistant text only — thinking and tool-call deltas are not message text", () => {
    const { emitted, clock, mapper } = harness();

    mapper.handle({ type: "message_start", message: assistant("") });
    mapper.handle({ type: "message_update", assistantMessageEvent: { type: "thinking_delta", delta: "hmm" } });
    mapper.handle({ type: "message_update", assistantMessageEvent: { type: "toolcall_delta", delta: '{"a":' } });
    clock.tick();

    // Not even a message_start: a message opens when it has text, and neither
    // of these is text.
    expect(emitted.map((e) => e.kind)).toEqual([]);
  });

  test("gives each message its own id", () => {
    const { emitted, mapper } = harness();

    mapper.handle({ type: "message_start", message: { role: "user", content: "hello" } });
    mapper.handle({ type: "message_end", message: { role: "user", content: "hello" } });
    mapper.handle({ type: "message_start", message: assistant("hi") });
    mapper.handle({ type: "message_end", message: assistant("hi") });

    expect(emitted.map((e) => (e.body as { id: string }).id)).toEqual(["m1", "m1", "m2", "m2"]);
    expect(emitted.map((e) => (e.body as { role?: string }).role)).toEqual([
      "user",
      "user",
      "assistant",
      "assistant",
    ]);
  });

  test("keeps a tool result out of the transcript — its card is the record", () => {
    const { emitted, mapper } = harness();

    // pi puts a tool's whole output back in front of the model as a message of
    // its own. Drawing it would inline every byte the card exists to fetch on
    // demand, and say the same thing twice.
    mapper.handle({ type: "message_start", message: { role: "toolResult", content: "1\n2\n3\n" } });
    mapper.handle({ type: "message_end", message: { role: "toolResult", content: "1\n2\n3\n" } });

    expect(emitted).toEqual([]);
  });

  test("draws no bubble for an assistant turn that only called tools", () => {
    const { emitted, mapper } = harness();

    mapper.handle({ type: "message_start", message: assistant("") });
    mapper.handle({ type: "message_end", message: assistant("") });

    expect(emitted).toEqual([]);
  });

  test("draws a message that never streamed but has text", () => {
    const { emitted, mapper } = harness();

    // A user message is the case: pi delivers it whole, with no deltas.
    mapper.handle({ type: "message_start", message: { role: "user", content: "look at x" } });
    mapper.handle({ type: "message_end", message: { role: "user", content: "look at x" } });

    expect(emitted.map((e) => e.kind)).toEqual(["message_start", "message_end"]);
    expect(emitted[1].body).toEqual({ id: "m1", role: "user", text: "look at x" });
  });

  test("opens a message for text that arrives without a start rather than dropping it", () => {
    const { emitted, clock, mapper } = harness();

    mapper.handle({ type: "message_update", assistantMessageEvent: { type: "text_delta", delta: "orphan" } });
    clock.tick();

    expect(emitted.map((e) => e.kind)).toEqual(["message_start", "message_delta"]);
    expect(emitted[1].body).toEqual({ id: "m1", text: "orphan" });
  });

  test("drops pi event types it does not model, reporting each one once", () => {
    const { emitted, mapper, unknown } = harness();

    mapper.handle({ type: "bash_execution_update", delta: "line" });
    mapper.handle({ type: "bash_execution_update", delta: "line" });
    mapper.handle({ type: "a_pi_event_from_a_later_release" });

    expect(emitted).toEqual([]);
    expect(unknown).toEqual(["bash_execution_update", "a_pi_event_from_a_later_release"]);
  });
});

describe("messageText", () => {
  test("reads the shapes a pi message arrives in", () => {
    expect(messageText("plain")).toBe("plain");
    expect(messageText({ content: "plain" })).toBe("plain");
    expect(messageText({ content: [{ type: "text", text: "a" }, { type: "thinking", thinking: "x" }, { type: "text", text: "b" }] })).toBe("ab");
    expect(messageText({ content: [] })).toBe("");
    expect(messageText(null)).toBe("");
  });
});

describe("tool events", () => {
  const started = (callID: string, toolName: string, args: unknown) => ({
    type: "tool_execution_start",
    toolCallId: callID,
    toolName,
    args,
  });

  test("a call becomes a started/finished pair the app can draw one card from", () => {
    const { emitted, mapper } = harness();
    mapper.handle(started("call-1", "read", { path: "src/main.ts" }));
    mapper.handle({
      type: "tool_execution_end",
      toolCallId: "call-1",
      toolName: "read",
      isError: false,
      result: { content: [{ type: "text", text: "line one\nline two" }], details: {} },
    });
    expect(emitted.map((envelope) => envelope.kind)).toEqual(["tool_started", "tool_finished"]);
    expect(emitted[0].body).toEqual({
      call_id: "call-1",
      name: "read",
      summary: "src/main.ts",
      files: ["src/main.ts"],
    });
    // The finish repeats the label the start established: `tool_execution_end`
    // carries no arguments, so a card drawn from it alone would lose them.
    expect(emitted[1].body).toEqual({
      call_id: "call-1",
      name: "read",
      status: "ok",
      summary: "src/main.ts",
      files: ["src/main.ts"],
      detail: true,
      patch: false,
      truncated: false,
      full_output: false,
    });
  });

  test("the declaration says detail exists; the output itself is not on it", () => {
    const { emitted, mapper, details } = harness();
    const output = "x".repeat(50_000);
    mapper.handle(started("call-2", "bash", { command: "go test ./..." }));
    mapper.handle({
      type: "tool_execution_end",
      toolCallId: "call-2",
      toolName: "bash",
      isError: false,
      result: {
        content: [{ type: "text", text: output }],
        details: { truncation: { truncated: true }, fullOutputPath: "/tmp/pi-output-a1.log" },
      },
    });
    const finished = emitted[1].body as Record<string, unknown>;
    expect(finished.truncated).toBe(true);
    expect(finished.full_output).toBe(true);
    expect(JSON.stringify(finished)).not.toContain(output);
    expect(details.get("call-2")).toEqual({
      text: output,
      patch: undefined,
      truncated: true,
      fullOutputPath: "/tmp/pi-output-a1.log",
    });
  });

  test("an edit's patch is held for the diff and flagged on the card", () => {
    const { emitted, mapper, details } = harness();
    const patch = "--- a/x.ts\n+++ b/x.ts\n@@ -1 +1 @@\n-old\n+new\n";
    mapper.handle(started("call-3", "edit", { path: "x.ts", edits: [{ oldText: "old", newText: "new" }] }));
    mapper.handle({
      type: "tool_execution_end",
      toolCallId: "call-3",
      toolName: "edit",
      isError: false,
      result: { content: [{ type: "text", text: "edited x.ts" }], details: { patch, diff: "…" } },
    });
    expect((emitted[1].body as Record<string, unknown>).patch).toBe(true);
    expect(details.get("call-3")?.patch).toBe(patch);
  });

  test("a failure carries its headline so the card does not have to be opened to read it", () => {
    const { emitted, mapper } = harness();
    mapper.handle(started("call-4", "bash", { command: "false" }));
    mapper.handle({
      type: "tool_execution_end",
      toolCallId: "call-4",
      toolName: "bash",
      isError: true,
      result: { content: [{ type: "text", text: "Command exited with code 1" }], details: {} },
    });
    const finished = emitted[1].body as Record<string, unknown>;
    expect(finished.status).toBe("error");
    expect(finished.error).toBe("Command exited with code 1");
  });

  test("a huge summary is clipped, and says so with the limit and the real length", () => {
    const { emitted, mapper } = harness();
    mapper.handle(started("call-5", "bash", { command: "echo " + "y".repeat(SUMMARY_LIMIT * 2) }));
    const summary = (emitted[0].body as { summary: string }).summary;
    expect(summary).toContain(`clipped at ${SUMMARY_LIMIT} of ${SUMMARY_LIMIT * 2 + 5} characters`);
  });

  test("partial tool output is dropped rather than logged as an event we failed to map", () => {
    const { emitted, mapper, unknown } = harness();
    mapper.handle({ type: "tool_execution_update", toolCallId: "call-6", toolName: "bash", args: {}, partialResult: {} });
    expect(emitted).toEqual([]);
    expect(unknown).toEqual([]);
  });

  test("pending text is flushed ahead of a card, so the card lands after the sentence", () => {
    const { emitted, mapper } = harness();
    mapper.handle({ type: "message_start", message: assistant("") });
    mapper.handle({ type: "message_update", assistantMessageEvent: { type: "text_delta", delta: "Let me look." } });
    mapper.handle(started("call-7", "read", { path: "a.ts" }));
    expect(emitted.map((envelope) => envelope.kind)).toEqual(["message_start", "message_delta", "tool_started"]);
  });
});

describe("toolSummary and toolFiles", () => {
  test("name the call in one line, and list only files the call touched", () => {
    expect(toolSummary("bash", { command: "ls -la" })).toBe("ls -la");
    expect(toolSummary("grep", { pattern: "TODO", path: "internal" })).toBe("TODO in internal");
    expect(toolSummary("grep", { pattern: "TODO" })).toBe("TODO");
    expect(toolSummary("ls", {})).toBe(".");
    // An unknown tool falls through to its first string argument.
    expect(toolSummary("mcp__jira__issue", { count: 3, key: "ASTERISK-1" })).toBe("ASTERISK-1");
    expect(toolSummary("mcp__jira__issue", { count: 3 })).toBe("");

    expect(toolFiles("edit", { path: "a.ts" })).toEqual(["a.ts"]);
    expect(toolFiles("write", { path: "b.ts" })).toEqual(["b.ts"]);
    // A search root is not a file the agent touched.
    expect(toolFiles("grep", { pattern: "x", path: "internal" })).toEqual([]);
    expect(toolFiles("ls", { path: "internal" })).toEqual([]);
  });
});

describe("ToolDetailStore", () => {
  test("drops the oldest calls once the budget is spent, and says how many", () => {
    const store = new ToolDetailStore(100);
    store.put("a", { text: "a".repeat(60), truncated: false });
    store.put("b", { text: "b".repeat(60), truncated: false });
    expect(store.get("a")).toBeUndefined();
    expect(store.get("b")?.text.length).toBe(60);
    expect(store.missingReason("a")).toContain("dropped 1 older call(s)");
  });

  test("keeps a call bigger than the whole budget — it is the one about to be expanded", () => {
    const store = new ToolDetailStore(100);
    store.put("big", { text: "z".repeat(500), truncated: true });
    expect(store.get("big")?.text.length).toBe(500);
    expect(store.size).toBe(1);
  });

  test("re-putting a call replaces it rather than double-counting its bytes", () => {
    const store = new ToolDetailStore(1000);
    store.put("a", { text: "a".repeat(100), truncated: false });
    store.put("a", { text: "a".repeat(10), truncated: false });
    expect(store.retainedBytes).toBe(10);
  });
});

describe("toolResultText", () => {
  test("joins the text blocks and ignores everything else", () => {
    expect(toolResultText({ content: [{ type: "text", text: "one" }, { type: "image", data: "…" }, { type: "text", text: "two" }] })).toBe("one\ntwo");
    expect(toolResultText({ content: "plain" })).toBe("plain");
    expect(toolResultText(undefined)).toBe("");
  });
});

describe("parseVerb", () => {
  test("accepts the verbs the daemon sends", () => {
    expect(parseVerb('{"verb":"prompt","text":"hello"}')).toEqual({ verb: "prompt", text: "hello" });
    expect(parseVerb('{"verb":"steer","text":"actually, stop"}')).toEqual({ verb: "steer", text: "actually, stop" });
    expect(parseVerb('{"verb":"follow_up","text":"then this"}')).toEqual({ verb: "follow_up", text: "then this" });
    expect(parseVerb('{"verb":"shutdown"}')).toEqual({ verb: "shutdown" });
    expect(parseVerb('{"verb":"clear_queue"}')).toEqual({ verb: "clear_queue" });
    expect(parseVerb('{"verb":"tool_detail","call_id":"c1"}')).toEqual({ verb: "tool_detail", callID: "c1", full: false });
    expect(parseVerb('{"verb":"tool_detail","call_id":"c1","full":true}')).toEqual({ verb: "tool_detail", callID: "c1", full: true });
  });

  test("refuses a detail fetch with nothing to fetch", () => {
    expect(() => parseVerb('{"verb":"tool_detail"}')).toThrow("tool_detail verb needs a call_id");
  });

  test("names what is wrong instead of failing quietly", () => {
    expect(() => parseVerb('{"verb":"prompt","text":"  "}')).toThrow("prompt verb needs non-empty text");
    expect(() => parseVerb('{"verb":"steer"}')).toThrow("steer verb needs non-empty text");
    expect(() => parseVerb('{"verb":"follow_up","text":""}')).toThrow("follow_up verb needs non-empty text");
    expect(() => parseVerb('{"verb":"nudge","text":"x"}')).toThrow('unsupported verb "nudge"');
    expect(() => parseVerb("[]")).toThrow("verb must be a JSON object");
  });
});
