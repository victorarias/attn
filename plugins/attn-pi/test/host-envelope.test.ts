import { describe, expect, test } from "bun:test";
import {
  DeltaCoalescer,
  EnvelopeStream,
  PiEventMapper,
  messageText,
  parseVerb,
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
  const mapper = new PiEventMapper(stream, deltas, (type) => unknown.push(type));
  return { emitted, clock, mapper, unknown };
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

    expect(emitted.map((e) => e.kind)).toEqual(["message_start"]);
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
    mapper.handle({ type: "summarization_retry_scheduled" });

    expect(emitted).toEqual([]);
    expect(unknown).toEqual(["bash_execution_update", "summarization_retry_scheduled"]);
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

describe("parseVerb", () => {
  test("accepts the verbs the daemon sends", () => {
    expect(parseVerb('{"verb":"prompt","text":"hello"}')).toEqual({ verb: "prompt", text: "hello" });
    expect(parseVerb('{"verb":"shutdown"}')).toEqual({ verb: "shutdown" });
  });

  test("names what is wrong instead of failing quietly", () => {
    expect(() => parseVerb('{"verb":"prompt","text":"  "}')).toThrow("prompt verb needs non-empty text");
    expect(() => parseVerb('{"verb":"steer","text":"x"}')).toThrow('unsupported verb "steer"');
    expect(() => parseVerb("[]")).toThrow("verb must be a JSON object");
  });
});
