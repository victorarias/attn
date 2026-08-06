// The wire between the pi host process and attn's daemon.
//
// One envelope shape carries both streams the vision names: SEMANTIC kinds in
// attn's own vocabulary, which the daemon understands and integrates on, and
// RENDER kinds whose bodies exist only so the app can paint. The daemon routes
// render bodies without reading them, so their shape is owned here and in the
// app, not in attn's protocol.
//
// `seq` is a single monotonic spine across both streams: render deltas and the
// semantic events that bracket them are ordered against each other, which is
// what lets a later attach dedup a live stream against a snapshot watermark.

/** Semantic kinds. attn's vocabulary; the daemon may read these. */
export const SEMANTIC_KINDS = ["session_ready", "run_started", "run_settled"] as const;

/** Render kinds. Opaque to the daemon; host and app agree on the bodies. */
export const RENDER_KINDS = ["message_start", "message_delta", "message_end", "queue_update"] as const;

export type SemanticKind = (typeof SEMANTIC_KINDS)[number];
export type RenderKind = (typeof RENDER_KINDS)[number];
export type EnvelopeKind = SemanticKind | RenderKind;

/**
 * The attn session states this host declares.
 *
 * These are attn's words, not pi's, and they are the whole reason the state of
 * a conversation session needs no classifier and no screen: the host is attn
 * code sitting inside the agent's own event loop, so it can say what the
 * session is doing rather than infer it.
 *
 * `waiting_input` is here because a later slice's declaration will use it — an
 * approval request is the agent asking, and only the host knows one is open.
 * Slice 2 declares `working` and `idle` alone.
 */
export type HostSessionState = "working" | "idle" | "waiting_input";

export interface Envelope {
  session_id: string;
  seq: number;
  kind: EnvelopeKind;
  body: unknown;
}

/**
 * EVERY declaration carries the state it puts the session in. That invariant is
 * what keeps the daemon's rule to one line — a declaration's `state` is applied
 * at that declaration's seq — instead of a second envelope family that says the
 * same thing a beat later, or a daemon-side guess about what a run boundary
 * means.
 */
export interface DeclarationBody {
  state: HostSessionState;
}

export interface SessionReadyBody extends DeclarationBody {
  /** pi's own session-file path, or null when pi has not written one yet. */
  session_file: string | null;
  model: string;
  cwd: string;
  pi_version: string;
}

export interface RunStartedBody extends DeclarationBody {}

export interface RunSettledBody extends DeclarationBody {
  /** pi's last-run error text, when the run ended badly. Never keyed on. */
  error?: string;
}

export interface MessageStartBody {
  id: string;
  role: string;
}

export interface MessageDeltaBody {
  id: string;
  text: string;
}

export interface MessageEndBody {
  id: string;
  role: string;
  text: string;
}

/**
 * What is queued and not yet delivered, as pi sees it right now.
 *
 * pi emits this on both edges: when a message joins a queue, and again when it
 * leaves one — the drain fires just before the user `message_start` that is the
 * message being read. So the pair is the whole of "queued, then seen", and the
 * app needs nothing else to draw it.
 */
export interface QueueUpdateBody {
  steering: string[];
  followUp: string[];
}

/** Anything the mapper is handed. pi's event union grows without warning. */
type PiEvent = { type: string; [key: string]: unknown };

export interface EnvelopeSink {
  (envelope: Envelope): void;
}

/**
 * Mints the seq spine and stamps the session id. One per host process.
 */
export class EnvelopeStream {
  private seq = 0;

  constructor(
    private readonly sessionID: string,
    private readonly sink: EnvelopeSink,
  ) {}

  emit(kind: EnvelopeKind, body: unknown): Envelope {
    this.seq += 1;
    const envelope: Envelope = { session_id: this.sessionID, seq: this.seq, kind, body };
    this.sink(envelope);
    return envelope;
  }
}

/**
 * Batches text deltas into one `message_delta` per flush window.
 *
 * Receipt: a thinking model produces ~480-550 `message_update` events per ~5 s
 * reply, bursting to 1,970 events/s (2026-08-04 spike, s2-delta-rate). attn's
 * WebSocket clients buffer 256 messages and drop or disconnect past that, so
 * the raw stream cannot reach the wire. A 30 ms window caps one session at ~33
 * envelopes/s, two orders of magnitude under the burst and still faster than a
 * display refresh.
 *
 * `schedule` is injected so tests drive the window instead of waiting on it.
 */
export class DeltaCoalescer {
  private pending = new Map<string, string>();
  private timer: unknown = null;

  constructor(
    private readonly windowMs: number,
    private readonly emit: (messageID: string, text: string) => void,
    private readonly schedule: (fn: () => void, ms: number) => unknown = setTimeout,
    private readonly cancel: (handle: unknown) => void = (handle) => clearTimeout(handle as never),
  ) {}

  push(messageID: string, text: string): void {
    if (text === "") return;
    this.pending.set(messageID, (this.pending.get(messageID) ?? "") + text);
    if (this.timer === null) {
      this.timer = this.schedule(() => {
        this.timer = null;
        this.flush();
      }, this.windowMs);
    }
  }

  /**
   * Drains every pending message in insertion order. Called on the window and
   * again before any envelope that must not overtake the text it follows — a
   * `message_end` or a semantic event.
   */
  flush(): void {
    if (this.timer !== null) {
      this.cancel(this.timer);
      this.timer = null;
    }
    if (this.pending.size === 0) return;
    const batch = this.pending;
    this.pending = new Map();
    for (const [messageID, text] of batch) {
      this.emit(messageID, text);
    }
  }
}

/** Pulls display text out of whatever shape a pi message carries. */
export function messageText(message: unknown): string {
  if (typeof message === "string") return message;
  if (message === null || typeof message !== "object") return "";
  const content = (message as { content?: unknown }).content;
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  let text = "";
  for (const block of content) {
    if (block && typeof block === "object" && (block as { type?: unknown }).type === "text") {
      const value = (block as { text?: unknown }).text;
      if (typeof value === "string") text += value;
    }
  }
  return text;
}

/** pi's queue arrays are readonly and could grow entries we do not model. */
function stringList(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === "string") : [];
}

function messageRole(message: unknown): string {
  if (message && typeof message === "object") {
    const role = (message as { role?: unknown }).role;
    if (typeof role === "string" && role !== "") return role;
  }
  return "assistant";
}

/**
 * Turns pi's event stream into envelopes.
 *
 * pi ships ~3.6x/week and grows its event union without labelling it (the
 * 0.80.10 -> 0.83.0 diff added four types unannounced), so this switch has a
 * default arm and never claims exhaustiveness over pi's types. Only attn's own
 * kinds are closed.
 */
export class PiEventMapper {
  private messageCounter = 0;
  private currentMessageID: string | null = null;
  private readonly seenUnknown = new Set<string>();

  constructor(
    private readonly stream: EnvelopeStream,
    private readonly deltas: DeltaCoalescer,
    private readonly onUnknown: (type: string) => void = () => {},
  ) {}

  /** Emits whatever the pi event maps to. Unknown pi types are dropped. */
  handle(event: PiEvent): void {
    switch (event.type) {
      case "agent_start": {
        this.deltas.flush();
        const body: RunStartedBody = { state: "working" };
        this.stream.emit("run_started", body);
        return;
      }

      case "agent_settled": {
        this.deltas.flush();
        // `idle` and not `waiting_input`: a settled run in a chatbox is both at
        // once — the agent finished, and nothing more happens until the user
        // types — and attn's own vocabulary assigns that case to idle (see
        // internal/attention). They open a turn identically, so choosing
        // between the two words is not worth a classifier call per run.
        const body: RunSettledBody = { state: "idle" };
        this.stream.emit("run_settled", body);
        return;
      }

      case "queue_update": {
        // Ahead of the text it is about: a drain fires immediately before the
        // user message it delivered, and the app draws the queue emptying and
        // then the message arriving, in that order.
        this.deltas.flush();
        const body: QueueUpdateBody = {
          steering: stringList(event.steering),
          followUp: stringList(event.followUp),
        };
        this.stream.emit("queue_update", body);
        return;
      }

      case "message_start": {
        this.deltas.flush();
        this.messageCounter += 1;
        this.currentMessageID = `m${this.messageCounter}`;
        const body: MessageStartBody = { id: this.currentMessageID, role: messageRole(event.message) };
        this.stream.emit("message_start", body);
        return;
      }

      case "message_update": {
        const inner = event.assistantMessageEvent as { type?: string; delta?: unknown } | undefined;
        // Only assistant TEXT streams to the pane in slice 1. Thinking and
        // tool-call argument deltas are their own render surfaces (slices 3+)
        // and would otherwise land in the message body as noise.
        if (!inner || inner.type !== "text_delta" || typeof inner.delta !== "string") return;
        this.deltas.push(this.requireMessageID(), inner.delta);
        return;
      }

      case "message_end": {
        this.deltas.flush();
        const id = this.requireMessageID();
        const body: MessageEndBody = {
          id,
          role: messageRole(event.message),
          text: messageText(event.message),
        };
        this.stream.emit("message_end", body);
        this.currentMessageID = null;
        return;
      }

      default:
        // pi's union grows without notice; an unrecognized type is expected,
        // not a defect. Report each type once so a pin bump shows what is new
        // without drowning the log.
        if (!this.seenUnknown.has(event.type)) {
          this.seenUnknown.add(event.type);
          this.onUnknown(event.type);
        }
    }
  }

  /**
   * A delta or end without a preceding start means pi opened a message shape
   * this mapper does not model. Minting one keeps the pane coherent rather
   * than dropping the text on the floor.
   */
  private requireMessageID(): string {
    if (this.currentMessageID === null) {
      this.messageCounter += 1;
      this.currentMessageID = `m${this.messageCounter}`;
      this.stream.emit("message_start", { id: this.currentMessageID, role: "assistant" });
    }
    return this.currentMessageID;
  }
}

/**
 * One verb the daemon can send the host over stdin.
 *
 * Three of them carry text, and the difference between them is only WHEN the
 * agent reads it:
 *
 *   prompt     the composer's first word. Refused mid-run — the composer that
 *              sends it is shut for the whole of a run.
 *   steer      read at the next turn boundary, mid-run. This is what a doorbell
 *              uses: an interruption that lands as soon as the agent draws
 *              breath rather than after everything it planned to do.
 *   follow_up  read only when the whole run would otherwise settle. The way to
 *              queue something for after the work, without cutting into it.
 *
 * steer and follow_up are also valid on an idle session, where there is no run
 * to land in; the host opens one instead. Which is why the daemon never has to
 * ask what a session is doing before nudging it.
 */
export type HostVerbWithText = { verb: "prompt" | "steer" | "follow_up"; text: string };
export type HostVerb = HostVerbWithText | { verb: "shutdown" };

const TEXT_VERBS = new Set(["prompt", "steer", "follow_up"]);

export function parseVerb(line: string): HostVerb {
  const value: unknown = JSON.parse(line);
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("verb must be a JSON object");
  }
  const verb = (value as { verb?: unknown }).verb;
  if (typeof verb === "string" && TEXT_VERBS.has(verb)) {
    const text = (value as { text?: unknown }).text;
    if (typeof text !== "string" || text.trim() === "") throw new Error(`${verb} verb needs non-empty text`);
    return { verb: verb as HostVerbWithText["verb"], text };
  }
  if (verb === "shutdown") return { verb: "shutdown" };
  throw new Error(`unsupported verb ${JSON.stringify(verb)}`);
}
