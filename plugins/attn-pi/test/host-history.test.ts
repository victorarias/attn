import { describe, expect, test } from "bun:test";
import {
  DeltaCoalescer,
  EnvelopeStream,
  PiEventMapper,
  TranscriptStore,
  conversationInterrupted,
  parseVerb,
  reconstructTranscript,
  snapshotItemKey,
  type Envelope,
  type SessionEntryLike,
  type SnapshotItem,
} from "../host/envelope";

/**
 * Slice 5: history and depth.
 *
 * Three things are tested here. Paging is the sharp one — a snapshot is
 * broadcast and a client may already be showing scroll-back, so the window a
 * snapshot carries and the pages behind it have to describe themselves well
 * enough that a client never loses what it has. The notices are what makes a
 * compaction or a retry visible instead of a thirty-second silence. And the
 * verbs are the daemon's half of both.
 */

function harness() {
  const emitted: Envelope[] = [];
  const stream = new EnvelopeStream("attn-session-1", (envelope) => emitted.push(envelope));
  const pending: Array<() => void> = [];
  const deltas = new DeltaCoalescer(
    30,
    (id, text) => stream.emit("message_delta", { id, text }),
    (fn) => { pending.push(fn); return pending.length; },
    () => {},
  );
  const unknown: string[] = [];
  const mapper = new PiEventMapper(stream, deltas, (type) => unknown.push(type));
  return { emitted, mapper, unknown, tick: () => { for (const fn of pending.splice(0)) fn(); } };
}

const ids = (items: SnapshotItem[]): string[] =>
  items.map((item) => (item.kind === "tool" ? item.call_id : item.id));

function filled(count: number, epoch = "epoch-1"): TranscriptStore {
  const store = new TranscriptStore(epoch, 3, 1 << 20);
  for (let index = 1; index <= count; index += 1) {
    store.apply("message_end", { id: `m${index}`, role: "assistant", text: `line ${index}` });
  }
  return store;
}

describe("scroll-back paging", () => {
  test("a snapshot carries the newest window and the epoch that minted it", () => {
    const snapshot = filled(10).snapshot();
    expect(ids(snapshot.items)).toEqual(["m8", "m9", "m10"]);
    expect(snapshot.epoch).toBe("epoch-1");
    expect(snapshot.has_more).toBe(true);
    expect(snapshot.total).toBe(10);
  });

  test("pages walk backwards a window at a time and say when the start is reached", () => {
    const store = filled(7);
    const first = store.page(snapshotItemKey({ kind: "message", id: "m5", role: "assistant", text: "", streaming: false }));
    expect(first.before).toBe("message:m5");
    expect(ids(first.items)).toEqual(["m2", "m3", "m4"]);
    expect(first.has_more).toBe(true);

    const second = store.page("message:m2");
    expect(ids(second.items)).toEqual(["m1"]);
    // Nothing older than m1 exists, so a client that draws this page knows it
    // has the whole conversation and stops asking.
    expect(second.has_more).toBe(false);
  });

  test("an anchor this host never had answers with an empty page, not an error", () => {
    const page = filled(4).page("message:from-another-window");
    expect(page.items).toEqual([]);
    expect(page.has_more).toBe(false);
    expect(page.epoch).toBe("epoch-1");
  });

  test("the oldest item's own page is empty: there is nothing before the start", () => {
    const page = filled(4).page("message:m1");
    expect(page.items).toEqual([]);
    expect(page.has_more).toBe(false);
  });

  test("a tool card is a paging anchor like any other item", () => {
    const store = new TranscriptStore("epoch-1", 2, 1 << 20);
    store.apply("message_end", { id: "m1", role: "user", text: "run it" });
    store.apply("tool_started", { call_id: "c1", name: "bash", summary: "ls", files: [] });
    store.apply("message_end", { id: "m2", role: "assistant", text: "done" });
    expect(ids(store.page("tool:c1").items)).toEqual(["m1"]);
  });

  test("a window never clips the newest item, however large it is", () => {
    const store = new TranscriptStore("epoch-1", 50, 16);
    store.apply("message_end", { id: "m1", role: "assistant", text: "x".repeat(64) });
    expect(ids(store.snapshot().items)).toEqual(["m1"]);
  });

  test("keys never collide across kinds", () => {
    expect(snapshotItemKey({ kind: "message", id: "1", role: "user", text: "", streaming: false })).toBe("message:1");
    expect(snapshotItemKey({
      kind: "tool",
      call_id: "1",
      name: "bash",
      summary: "",
      files: [],
      status: "ok",
      detail: false,
      patch: false,
      truncated: false,
      full_output: false,
    })).toBe("tool:1");
    expect(snapshotItemKey({ kind: "notice", id: "1", level: "info", text: "", done: true })).toBe("notice:1");
  });
});

describe("compaction and retry notices", () => {
  test("a compaction opens one row and settles the same one", () => {
    const { emitted, mapper } = harness();
    mapper.handle({ type: "compaction_start", reason: "threshold" });
    mapper.handle({ type: "compaction_end", reason: "threshold", result: { tokensBefore: 120_000 }, aborted: false, willRetry: false });

    expect(emitted.map((envelope) => envelope.kind)).toEqual(["notice", "notice"]);
    expect(emitted[0]!.body).toEqual({ id: "n1", level: "info", text: "Compacting the conversation (threshold)...", done: false });
    expect(emitted[1]!.body).toEqual({ id: "n1", level: "info", text: "Compacted the conversation (120,000 tokens summarized)", done: true });
  });

  test("a second compaction draws its own row rather than overwriting the first", () => {
    const { emitted, mapper } = harness();
    for (const reason of ["threshold", "manual"]) {
      mapper.handle({ type: "compaction_start", reason });
      mapper.handle({ type: "compaction_end", reason, result: undefined, aborted: false, willRetry: false });
    }
    expect(emitted.map((envelope) => (envelope.body as { id: string }).id)).toEqual(["n1", "n1", "n2", "n2"]);
  });

  test("a failed compaction says what went wrong, on the row that was spinning", () => {
    const { emitted, mapper } = harness();
    mapper.handle({ type: "compaction_start", reason: "overflow" });
    mapper.handle({ type: "compaction_end", reason: "overflow", aborted: false, willRetry: false, errorMessage: "context window exceeded" });
    expect(emitted[1]!.body).toEqual({ id: "n1", level: "error", text: "Compaction failed: context window exceeded", done: true });
  });

  test("a retry says which attempt it is and how long the wait is", () => {
    const { emitted, mapper } = harness();
    mapper.handle({ type: "auto_retry_start", attempt: 2, maxAttempts: 5, delayMs: 4000, errorMessage: "overloaded_error" });
    mapper.handle({ type: "auto_retry_end", success: true, attempt: 2 });
    expect(emitted[0]!.body).toEqual({ id: "n1", level: "warn", text: "Retrying 2/5 in 4s: overloaded_error", done: false });
    expect(emitted[1]!.body).toEqual({ id: "n1", level: "info", text: "Recovered after 2 retry attempt(s)", done: true });
  });

  test("a retry that ran out of attempts is an error, and carries the last one", () => {
    const { emitted, mapper } = harness();
    mapper.handle({ type: "auto_retry_start", attempt: 5, maxAttempts: 5, delayMs: 0, errorMessage: "overloaded" });
    mapper.handle({ type: "auto_retry_end", success: false, attempt: 5, finalError: "still overloaded" });
    expect(emitted[1]!.body).toEqual({ id: "n1", level: "error", text: "Gave up after 5 retry attempt(s): still overloaded", done: true });
  });

  // The bug this exists for, caught live on 2026-08-09: a model the provider
  // had retired answered 404, pi persisted an empty assistant message with
  // `stopReason: "error"`, and the pane showed nothing at all — the composer
  // simply reopened as if the agent had chosen not to reply.
  test("a turn the provider refused says so instead of settling in silence", () => {
    const { emitted, mapper } = harness();
    const message = {
      role: "assistant",
      content: [],
      stopReason: "error",
      errorMessage: JSON.stringify({
        error: {
          message: JSON.stringify({ error: { code: 404, message: "This model models/gemini-2.0-flash is no\n  longer available.", status: "NOT_FOUND" } }),
          code: 404,
        },
      }),
    };
    mapper.handle({ type: "message_start", message });
    mapper.handle({ type: "message_end", message });

    expect(emitted.map((envelope) => envelope.kind)).toEqual(["notice"]);
    expect(emitted[0]!.body).toEqual({
      id: "n1",
      level: "error",
      // The provider's sentence, dug out of two layers of JSON envelope and
      // flattened to the one line the row is.
      text: "The agent could not answer: This model models/gemini-2.0-flash is no longer available.",
      done: true,
    });
  });

  test("a turn that succeeded draws no failure row", () => {
    const { emitted, mapper } = harness();
    const message = { role: "assistant", content: [{ type: "text", text: "hi" }], stopReason: "stop" };
    mapper.handle({ type: "message_start", message });
    mapper.handle({ type: "message_end", message });
    expect(emitted.map((envelope) => envelope.kind)).toEqual(["message_start", "message_end"]);
  });

  test("a failure the provider did not explain still gets a row", () => {
    const { emitted, mapper } = harness();
    const message = { role: "assistant", content: [], stopReason: "error" };
    mapper.handle({ type: "message_start", message });
    mapper.handle({ type: "message_end", message });
    expect(emitted[0]!.body).toMatchObject({
      level: "error",
      text: "The agent could not answer: The provider reported an error with no message.",
    });
  });

  test("an end with no start is still drawn: the record beats the pairing", () => {
    const { emitted, mapper } = harness();
    mapper.handle({ type: "compaction_end", reason: "manual", aborted: true, willRetry: false });
    expect(emitted).toHaveLength(1);
    expect(emitted[0]!.body).toEqual({ id: "n1", level: "warn", text: "Compaction was cancelled", done: true });
  });

  test("a notice never overtakes the text it follows", () => {
    const { emitted, mapper } = harness();
    mapper.handle({ type: "message_update", assistantMessageEvent: { type: "text_delta", delta: "thinking about it" } });
    mapper.handle({ type: "compaction_start", reason: "threshold" });
    expect(emitted.map((envelope) => envelope.kind)).toEqual(["message_start", "message_delta", "notice"]);
  });

  test("the transcript keeps one row per notice, moving it from pending to done", () => {
    const store = new TranscriptStore("epoch-1");
    store.apply("notice", { id: "n1", level: "info", text: "Compacting...", done: false });
    store.apply("notice", { id: "n1", level: "info", text: "Compacted", done: true });
    expect(store.snapshot().items).toEqual([{ kind: "notice", id: "n1", level: "info", text: "Compacted", done: true }]);
  });
});

describe("history off disk", () => {
  test("a compaction entry becomes the row that explains the missing start", () => {
    const entry: SessionEntryLike = { type: "compaction", id: "e2", tokensBefore: 90_000 };
    const { items } = reconstructTranscript([
      { type: "message", id: "e1", message: { role: "user", content: [{ type: "text", text: "hi" }] } },
      entry,
      { type: "message", id: "e3", message: { role: "assistant", content: [{ type: "text", text: "back" }] } },
    ]);
    expect(items[1]).toEqual({
      kind: "notice",
      id: "h:e2",
      level: "info",
      text: "Compacted the conversation (90,000 tokens summarized)",
      done: true,
    });
  });

  test("a model change is part of the history a revived conversation shows", () => {
    const { items } = reconstructTranscript([
      { type: "message", id: "e0", message: { role: "user", content: [{ type: "text", text: "hello" }] } },
      { type: "model_change", id: "e1", provider: "openai", modelId: "gpt-5.6-luna" },
    ]);
    expect(items[1]).toEqual({ kind: "notice", id: "h:e1", level: "info", text: "Model switched to openai/gpt-5.6-luna", done: true });
  });

  // Measured on a real fresh conversation (2026-08-09, pi 0.83.0): pi writes a
  // model_change and a thinking_level_change into the session file before
  // anything is said. Drawing those is a conversation claiming it switched
  // models before it existed — and, because the row is not an assistant
  // message, it also made every new session declare `waiting_input`.
  test("the model a session opened on is not a switch", () => {
    const { items } = reconstructTranscript([
      { type: "model_change", id: "e1", provider: "openai", modelId: "gpt-5.6-luna" },
      { type: "thinking_level_change", id: "e2", thinkingLevel: "off" },
    ]);
    expect(items).toEqual([]);
    expect(conversationInterrupted(items)).toBe(false);
  });

  // A conversation reopened after a provider error must not show the prompt
  // and then nothing — that reads as the agent having ignored it. The turn is
  // also still owed, so the session comes back waiting rather than idle.
  test("a turn the provider refused is still explained after a reopen", () => {
    const { items } = reconstructTranscript([
      { type: "message", id: "e1", message: { role: "user", content: [{ type: "text", text: "do it" }] } },
      { type: "message", id: "e2", message: { role: "assistant", content: [], stopReason: "error", errorMessage: "quota exhausted" } },
    ]);
    expect(items[1]).toEqual({
      kind: "notice",
      id: "h:e2:error",
      level: "error",
      text: "The agent could not answer: quota exhausted",
      done: true,
    });
    expect(conversationInterrupted(items)).toBe(true);
  });

  test("a model change with nothing to say is dropped rather than drawn blank", () => {
    const { items } = reconstructTranscript([
      { type: "message", id: "e0", message: { role: "user", content: [{ type: "text", text: "hello" }] } },
      { type: "model_change", id: "e1" },
    ]);
    expect(items.filter((item) => item.kind === "notice")).toEqual([]);
  });

  // A switch is something that happened TO the conversation. The agent still
  // had the last word, so the session is idle rather than waiting for one.
  test("a model switched after the agent finished does not read as interrupted", () => {
    const { items } = reconstructTranscript([
      { type: "message", id: "e0", message: { role: "user", content: [{ type: "text", text: "hello" }] } },
      { type: "message", id: "e1", message: { role: "assistant", content: [{ type: "text", text: "hi" }] } },
      { type: "model_change", id: "e2", provider: "openai", modelId: "gpt-5.6-luna" },
    ]);
    expect(items[items.length - 1]!.kind).toBe("notice");
    expect(conversationInterrupted(items)).toBe(false);
  });
});

describe("the slice-5 verbs", () => {
  test("history carries the anchor it pages before", () => {
    expect(parseVerb('{"verb":"history","before":"message:m4"}')).toEqual({ verb: "history", before: "message:m4" });
  });

  test("history without an anchor is refused rather than guessed at", () => {
    expect(() => parseVerb('{"verb":"history"}')).toThrow("history verb needs a before cursor");
    expect(() => parseVerb('{"verb":"history","before":"  "}')).toThrow("history verb needs a before cursor");
  });

  test("set_model carries the provider/model-id pair attn pins with", () => {
    expect(parseVerb('{"verb":"set_model","model":" openai/gpt-5.6-luna "}')).toEqual({
      verb: "set_model",
      model: "openai/gpt-5.6-luna",
    });
  });

  test("set_model without a model is refused", () => {
    expect(() => parseVerb('{"verb":"set_model"}')).toThrow("set_model verb needs a model");
  });
});

/**
 * Retention: the tripwire, not the window.
 *
 * `TRANSCRIPT_RETENTION_*` bounds what the host HOLDS, and a conversation that
 * reaches it has been talking for weeks — 50,000 items is 4.4x the longest
 * conversation anyone on this machine has ever had. Nothing real has ever
 * reached it, which is exactly why the path deserves to be driven here: what it
 * drops is gone for good, and every question a client can still ask has to have
 * an honest answer afterwards.
 */
describe("retention", () => {
  /** A store whose retention tripwire is small enough to reach. */
  function retained(count: number, items = 5, bytes = 1 << 20): TranscriptStore {
    const store = new TranscriptStore("epoch-1", 3, 1 << 20, items, bytes);
    for (let index = 1; index <= count; index += 1) {
      store.apply("message_end", { id: `m${index}`, role: "assistant", text: `line ${index}` });
    }
    return store;
  }

  test("the oldest items go, the newest stay", () => {
    const store = retained(12);
    expect(store.size).toBe(5);
    expect(store.droppedItems).toBe(7);
    // The window is applied on the way out and is smaller still.
    expect(ids(store.snapshot().items)).toEqual(["m10", "m11", "m12"]);
  });

  test("a snapshot still counts what it no longer holds", () => {
    // `total` is the conversation's length, not the host's inventory. A client
    // that is shown 3 of 12 deserves to know the other 9 existed, even for the
    // ones nobody can serve any more.
    const snapshot = retained(12).snapshot();
    expect(snapshot.total).toBe(12);
    expect(snapshot.truncated).toBe(true);
    // Two different questions: `has_more` is "can I page for more", which is
    // false once the window covers everything the host still holds.
    expect(snapshot.has_more).toBe(true);
  });

  test("a window covering everything left still reports the conversation as truncated", () => {
    // Retention has cut the transcript down to inside the window. Nothing can
    // be paged, and the client is nonetheless showing a fragment.
    const store = new TranscriptStore("epoch-1", 10, 1 << 20, 3, 1 << 20);
    for (let index = 1; index <= 9; index += 1) {
      store.apply("message_end", { id: `m${index}`, role: "assistant", text: `line ${index}` });
    }
    const snapshot = store.snapshot();
    expect(ids(snapshot.items)).toEqual(["m7", "m8", "m9"]);
    expect(snapshot.has_more).toBe(false);
    expect(snapshot.truncated).toBe(true);
    expect(snapshot.total).toBe(9);
  });

  test("paging past what retention dropped answers with nothing rather than an error", () => {
    // The zombie edge: a client holding scroll-back the host has since evicted
    // asks for the page before it. There is no page. The answer has to be an
    // empty one addressed to that anchor, so the asking client can stop.
    const store = retained(12);
    const page = store.page("message:m2");
    expect(page.before).toBe("message:m2");
    expect(page.items).toEqual([]);
    expect(page.has_more).toBe(false);
    expect(page.epoch).toBe("epoch-1");
  });

  test("the oldest item the host still holds has nothing before it", () => {
    const store = retained(12);
    const page = store.page("message:m8");
    expect(page.items).toEqual([]);
    expect(page.has_more).toBe(false);
  });

  test("bytes are given back when an item is dropped", () => {
    // The byte budget is a running total, so a drop that forgot to subtract
    // would ratchet the store into evicting on every push forever.
    const store = new TranscriptStore("epoch-1", 3, 1 << 20, 4, 1 << 20);
    for (let index = 1; index <= 40; index += 1) {
      store.apply("message_end", { id: `m${index}`, role: "assistant", text: `line ${index}` });
    }
    expect(store.size).toBe(4);
    const held = store.snapshot().items;
    expect(ids(held)).toEqual(["m38", "m39", "m40"]);
    // Four items of "line NN" plus their ids: tens of bytes, not thousands.
    expect(store.retainedBytes).toBeLessThan(200);
    expect(store.retainedBytes).toBeGreaterThan(0);
  });

  test("the byte tripwire drops history the item count would have kept", () => {
    const store = new TranscriptStore("epoch-1", 50, 1 << 20, 10_000, 200);
    for (let index = 1; index <= 20; index += 1) {
      store.apply("message_end", { id: `m${index}`, role: "assistant", text: "x".repeat(50) });
    }
    expect(store.size).toBeLessThan(20);
    expect(store.droppedItems).toBeGreaterThan(0);
    expect(store.retainedBytes).toBeLessThanOrEqual(200);
  });

  test("one item larger than the whole budget is kept rather than leaving nothing", () => {
    // Same bargain the window makes for the newest item: a transcript that
    // evicted its way to empty would show a live conversation as blank.
    const store = new TranscriptStore("epoch-1", 3, 1 << 20, 10_000, 100);
    store.apply("message_end", { id: "m1", role: "assistant", text: "x".repeat(5_000) });
    expect(store.size).toBe(1);
    expect(store.snapshot().items).toHaveLength(1);
  });

  test("a tool card evicted by retention takes its history with it", () => {
    const store = new TranscriptStore("epoch-1", 10, 1 << 20, 2, 1 << 20);
    store.apply("tool_started", { call_id: "c1", name: "bash", summary: "ls" });
    store.apply("message_end", { id: "m1", role: "assistant", text: "one" });
    store.apply("message_end", { id: "m2", role: "assistant", text: "two" });
    expect(ids(store.snapshot().items)).toEqual(["m1", "m2"]);
    expect(store.page("tool:c1").items).toEqual([]);
  });

  test("a message still streaming survives eviction even once it is not the newest", () => {
    // The shape that makes this reachable: pi opens a tool while the message is
    // still being written, so the open message is no longer last and the byte
    // tripwire reaches it. Evicting it there does not just lose the paragraph —
    // the next delta finds no open message, mints a fresh one from the tail
    // alone, and the sentence reappears truncated BELOW the tool it was written
    // above.
    const store = new TranscriptStore("epoch-1", 10, 1 << 20, 10_000, 400);
    store.apply("message_start", { id: "m1", role: "assistant" });
    store.apply("tool_started", { call_id: "c1", name: "bash", summary: "ls" });
    for (let index = 0; index < 40; index += 1) {
      store.apply("message_delta", { id: "m1", text: "0123456789" });
    }
    const items = store.snapshot().items;
    expect(ids(items)).toEqual(["m1", "c1"]);
    expect(items[0]!.kind === "message" && items[0]!.text).toBe("0123456789".repeat(40));
    expect(store.droppedItems).toBe(0);
  });

  test("what a still-streaming message held is dropped once it ends", () => {
    // The reprieve is for the writing, not forever: the budget is enforced again
    // as soon as the message settles.
    const store = new TranscriptStore("epoch-1", 10, 1 << 20, 10_000, 400);
    store.apply("message_start", { id: "m1", role: "assistant" });
    store.apply("tool_started", { call_id: "c1", name: "bash", summary: "ls" });
    for (let index = 0; index < 40; index += 1) {
      store.apply("message_delta", { id: "m1", text: "0123456789" });
    }
    expect(store.droppedItems).toBe(0);
    store.apply("message_end", { id: "m1", role: "assistant", text: "0123456789".repeat(40) });
    store.apply("message_end", { id: "m2", role: "assistant", text: "after" });
    expect(store.droppedItems).toBeGreaterThan(0);
  });

  test("a snapshot says how much is gone for good, and paging never lowers it", () => {
    // `truncated` only means "this message does not carry everything" and goes
    // false as a client pages back. `dropped` is the fact that outlives paging,
    // and it is what tells the app the conversation starts above anything it can
    // still be shown.
    const store = retained(12);
    expect(store.snapshot().dropped).toBe(7);
    const fresh = new TranscriptStore("epoch-1", 3, 1 << 20);
    for (let index = 1; index <= 12; index += 1) {
      fresh.apply("message_end", { id: `m${index}`, role: "assistant", text: `line ${index}` });
    }
    // Same conversation, retention never reached: nothing is gone.
    expect(fresh.snapshot().dropped).toBe(0);
    expect(fresh.snapshot().truncated).toBe(true);
  });

  test("a message still streaming is not evicted out from under its own deltas", () => {
    // message_delta finds the open message by id and appends. If retention drops
    // that message mid-stream, the next delta has nothing to append to and mints
    // a second message carrying only the tail — the agent's sentence arrives
    // split in two, under two ids.
    const store = new TranscriptStore("epoch-1", 10, 1 << 20, 10_000, 400);
    store.apply("message_start", { id: "m1", role: "assistant" });
    for (let index = 0; index < 40; index += 1) {
      store.apply("message_delta", { id: "m1", text: "0123456789" });
    }
    const items = store.snapshot().items;
    expect(items).toHaveLength(1);
    expect(ids(items)).toEqual(["m1"]);
    expect(items[0]!.kind === "message" && items[0]!.text).toBe("0123456789".repeat(40));
  });
});
