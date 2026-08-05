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
export const RENDER_KINDS = ["message_start", "message_delta", "message_end"] as const;

export type SemanticKind = (typeof SEMANTIC_KINDS)[number];
export type RenderKind = (typeof RENDER_KINDS)[number];
export type EnvelopeKind = SemanticKind | RenderKind;

export interface Envelope {
  session_id: string;
  seq: number;
  kind: EnvelopeKind;
  body: unknown;
}

export interface SessionReadyBody {
  /** pi's own session-file path, or null when pi has not written one yet. */
  session_file: string | null;
  model: string;
  cwd: string;
  pi_version: string;
}

export interface RunSettledBody {
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
      case "agent_start":
        this.deltas.flush();
        this.stream.emit("run_started", {});
        return;

      case "agent_settled": {
        this.deltas.flush();
        this.stream.emit("run_settled", {});
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

/** One verb the daemon can send the host over stdin. */
export type HostVerb = { verb: "prompt"; text: string } | { verb: "shutdown" };

export function parseVerb(line: string): HostVerb {
  const value: unknown = JSON.parse(line);
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("verb must be a JSON object");
  }
  const verb = (value as { verb?: unknown }).verb;
  if (verb === "prompt") {
    const text = (value as { text?: unknown }).text;
    if (typeof text !== "string" || text.trim() === "") throw new Error("prompt verb needs non-empty text");
    return { verb: "prompt", text };
  }
  if (verb === "shutdown") return { verb: "shutdown" };
  throw new Error(`unsupported verb ${JSON.stringify(verb)}`);
}
