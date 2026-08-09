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
export const SEMANTIC_KINDS = [
  "session_ready",
  "run_started",
  "run_settled",
  "tool_started",
  "tool_finished",
] as const;

/** Render kinds. Opaque to the daemon; host and app agree on the bodies. */
export const RENDER_KINDS = [
  "message_start",
  "message_delta",
  "message_end",
  "queue_update",
  "tool_detail",
  "conversation_snapshot",
] as const;

/**
 * The semantic kinds that MOVE the session, and therefore carry a `state`.
 *
 * The rest of the semantic family are facts about a run that is already open:
 * a tool starting says what the agent is doing, not that the session became
 * something else. Re-declaring `working` on every tool boundary would restamp
 * `state_since` on each one and reset the dashboard's "working for 4m" to zero
 * several times a minute, so a fact carries no state and the daemon never
 * applies one.
 */
export const STATE_DECLARATION_KINDS = ["session_ready", "run_started", "run_settled"] as const;

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
 * `idle` and `waiting_input` both open a turn and both accept a nudge, so the
 * choice between them is never about behavior — it is about telling the user WHY
 * the session went quiet. A run that ended on its own is `idle`. A conversation
 * revived from a session file whose last exchange never finished is
 * `waiting_input`: the agent did not stop, it was stopped, and nothing will move
 * until the user decides what to do about it (see `conversationInterrupted`).
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

/**
 * A tool call the agent started. One per `call_id`, matched by a
 * `tool_finished` with the same id.
 *
 * The body is deliberately small — a name, a one-line summary, the files it
 * names — because it is the transcript's permanent record of the call. What
 * the tool actually read, wrote, or printed is fetched on demand (see
 * ToolDetailBody): the corpus receipt behind this slice is a p99 11.6 MB
 * transcript with ~0.4% message text, and inlining tool output is how it got
 * that way.
 */
export interface ToolStartedBody {
  call_id: string;
  /** pi's own tool name: bash, read, edit, write, grep, find, ls, or any custom one. */
  name: string;
  /** One line naming the call: the command, the path, the pattern. May be clipped. */
  summary: string;
  /** Files this call touches. Search roots are in the summary, not here. */
  files: string[];
}

export interface ToolFinishedBody {
  call_id: string;
  name: string;
  status: "ok" | "error";
  summary: string;
  files: string[];
  /** Detail can be fetched for this call — the `tool_detail` verb answers. */
  detail: boolean;
  /** The detail carries a unified patch (the edit tool), so it draws as a diff. */
  patch: boolean;
  /** pi clipped the tool's own output; the whole of it may still exist on disk. */
  truncated: boolean;
  /** pi wrote the untruncated output to a file, so `full` detail can be asked for. */
  full_output: boolean;
  /** The failure, when the tool errored. Clipped to SUMMARY_LIMIT like a summary. */
  error?: string;
}

/**
 * What an expanded tool card shows, answered for one `tool_detail` verb.
 *
 * It is addressed by `call_id` and not by a request id: every client watching
 * the session gets it, and a card that was waiting for it draws. Two clients
 * expanding the same call cost one fetch, and a client that asked for the full
 * output upgrades everyone's card — same output, more of it.
 */
export interface ToolDetailBody {
  call_id: string;
  /** The tool's output text. */
  text: string;
  /** The unified patch, for tools that produce one. */
  patch?: string;
  /** `text` came from the full-output file rather than pi's clipped result. */
  full: boolean;
  /** More output exists than `text` shows. */
  truncated: boolean;
  /** Where pi kept the whole output, when it kept it. */
  full_output_path?: string;
  /** The fetch failed, and this says why. The card shows it in place of text. */
  error?: string;
}

/** Anything the mapper is handed. pi's event union grows without warning. */
type PiEvent = { type: string; [key: string]: unknown };

/**
 * How long a one-line summary — a command, a path, a failure — may be before
 * the declaration clips it.
 *
 * A declaration is the transcript's permanent per-call record, so it must not
 * be where output lives. pi's own tool-output cap is 2,000 lines / 50 KB;
 * 2,000 characters is 4% of that, past any command line or error headline a
 * person writes, and the whole text is one expand away. A clip says so, with
 * the limit and the real length, rather than trailing off.
 */
export const SUMMARY_LIMIT = 2000;

export function clipSummary(text: string, limit = SUMMARY_LIMIT): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  if (collapsed.length <= limit) return collapsed;
  return `${collapsed.slice(0, limit)}… [clipped at ${limit} of ${collapsed.length} characters]`;
}

function argString(args: unknown, key: string): string {
  if (!args || typeof args !== "object") return "";
  const value = (args as Record<string, unknown>)[key];
  return typeof value === "string" ? value : "";
}

/**
 * The one line a collapsed card shows for a call.
 *
 * Only pi's built-in tools are named here. A custom tool — an MCP server, a
 * user extension — falls through to its first string argument, which is nearly
 * always the thing it is acting on, and to nothing at all when it has none.
 * Guessing harder would only produce a confident wrong label.
 */
export function toolSummary(name: string, args: unknown): string {
  switch (name) {
    case "bash":
      return clipSummary(argString(args, "command"));
    case "read":
    case "write":
    case "edit":
      return clipSummary(argString(args, "path"));
    case "ls":
      return clipSummary(argString(args, "path") || ".");
    case "grep":
    case "find": {
      const pattern = argString(args, "pattern");
      const path = argString(args, "path");
      return clipSummary(path ? `${pattern} in ${path}` : pattern);
    }
    default: {
      if (!args || typeof args !== "object") return "";
      for (const value of Object.values(args as Record<string, unknown>)) {
        if (typeof value === "string" && value.trim() !== "") return clipSummary(value);
      }
      return "";
    }
  }
}

/**
 * The files a call touches — what a card can offer to open.
 *
 * A search root is not a file the agent touched, so grep/find/ls paths stay in
 * the summary. Only the tools that name one file are listed here.
 */
export function toolFiles(name: string, args: unknown): string[] {
  switch (name) {
    case "read":
    case "write":
    case "edit": {
      const path = argString(args, "path");
      return path ? [path] : [];
    }
    default:
      return [];
  }
}

/** What an expanded card needs, held until it is asked for or evicted. */
export interface ToolDetail {
  text: string;
  patch?: string;
  truncated: boolean;
  fullOutputPath?: string;
}

/**
 * Holds each tool call's detail until the app expands the card, under a byte
 * budget.
 *
 * A conversation session runs all day, so this cannot be an unbounded map of
 * every tool result the agent ever produced. It is insertion-ordered and drops
 * the oldest entries first, and a call whose detail is gone answers with a
 * message that names the budget and the count rather than an empty card.
 */
export class ToolDetailStore {
  private entries = new Map<string, ToolDetail>();
  private bytes = 0;
  private evicted = 0;

  constructor(private readonly budgetBytes: number) {}

  put(callId: string, detail: ToolDetail): void {
    this.remove(callId);
    const size = detailBytes(detail);
    // One detail larger than the whole budget would evict everything and then
    // itself; keep it rather than the history, since it is the one the user is
    // most likely about to expand.
    this.entries.set(callId, detail);
    this.bytes += size;
    for (const [key, held] of this.entries) {
      if (this.bytes <= this.budgetBytes || key === callId) break;
      this.entries.delete(key);
      this.bytes -= detailBytes(held);
      this.evicted += 1;
    }
  }

  get(callId: string): ToolDetail | undefined {
    return this.entries.get(callId);
  }

  /** Why a card has no detail, in words that name the limit and the ask. */
  missingReason(callId: string): string {
    return (
      `no detail held for tool call ${callId}: this host keeps the most recent ` +
      `${(this.budgetBytes / (1 << 20)).toFixed(0)} MB of tool output and has dropped ${this.evicted} older call(s)`
    );
  }

  private remove(callId: string): void {
    const existing = this.entries.get(callId);
    if (!existing) return;
    this.entries.delete(callId);
    this.bytes -= detailBytes(existing);
  }

  get retainedBytes(): number {
    return this.bytes;
  }

  get size(): number {
    return this.entries.size;
  }
}

function detailBytes(detail: ToolDetail): number {
  return detail.text.length + (detail.patch?.length ?? 0);
}

/** Pulls the text blocks out of a pi tool result's content array. */
export function toolResultText(result: unknown): string {
  if (!result || typeof result !== "object") return "";
  const content = (result as { content?: unknown }).content;
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  const parts: string[] = [];
  for (const block of content) {
    if (block && typeof block === "object" && (block as { type?: unknown }).type === "text") {
      const text = (block as { text?: unknown }).text;
      if (typeof text === "string") parts.push(text);
    }
  }
  return parts.join("\n");
}

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

/** One string field off a pi details object, or "" when it is not there. */
function readString(source: unknown, key: string): string {
  if (!source || typeof source !== "object") return "";
  const value = (source as Record<string, unknown>)[key];
  return typeof value === "string" ? value : "";
}

/**
 * pi message roles the transcript does not draw.
 *
 * `toolResult` is a message whose whole body is one tool's output — pi's own
 * way of putting the result back in front of the model. The transcript's record
 * of that call is its card, which holds a name and a line and fetches the
 * output only when someone opens it; drawing the message too would inline every
 * byte the card exists to keep out (receipt: `seq 1 5000` measured at 23,893
 * bytes in one message, 2026-08-06).
 */
const UNRENDERED_ROLES = new Set(["toolResult"]);

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
  /**
   * The calls that have started and not finished, by call id.
   *
   * `tool_execution_end` carries the result but not the arguments, so what the
   * call WAS has to be remembered from its start to be repeated on the finish
   * — the app draws one card and needs the same label on both halves. Bounded
   * by concurrency, not by session length: an entry lives only until its end
   * event lands.
   */
  private readonly openCalls = new Map<string, { name: string; summary: string; files: string[] }>();
  /** The role of the message pi currently has open, for the id it will mint. */
  private currentRole = "assistant";

  constructor(
    private readonly stream: EnvelopeStream,
    private readonly deltas: DeltaCoalescer,
    private readonly onUnknown: (type: string) => void = () => {},
    private readonly details: ToolDetailStore | null = null,
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

      case "tool_execution_start": {
        // Ahead of any text still pending: the card belongs after the sentence
        // that announced it, not before.
        this.deltas.flush();
        const callID = typeof event.toolCallId === "string" ? event.toolCallId : "";
        const name = typeof event.toolName === "string" ? event.toolName : "";
        if (callID === "") return;
        const call = { name, summary: toolSummary(name, event.args), files: toolFiles(name, event.args) };
        this.openCalls.set(callID, call);
        const body: ToolStartedBody = { call_id: callID, ...call };
        this.stream.emit("tool_started", body);
        return;
      }

      case "tool_execution_update":
        // The bash tool's throttled partial output. Deliberately dropped: a
        // card that streams its output for the whole of a `go test` run is the
        // per-tool version of the balloon collapsed cards exist to avoid, and
        // the finished card carries the same text one expand away. Named here
        // rather than left to the default arm so it does not read as a pi event
        // this host failed to keep up with.
        return;

      case "tool_execution_end": {
        this.deltas.flush();
        const callID = typeof event.toolCallId === "string" ? event.toolCallId : "";
        if (callID === "") return;
        const name = typeof event.toolName === "string" ? event.toolName : "";
        const started = this.openCalls.get(callID);
        this.openCalls.delete(callID);
        const isError = event.isError === true;
        const result = event.result;
        const text = toolResultText(result);
        const toolDetails = (result as { details?: unknown } | undefined)?.details;
        const patch = readString(toolDetails, "patch");
        const fullOutputPath = readString(toolDetails, "fullOutputPath");
        const truncation = toolDetails && typeof toolDetails === "object"
          ? (toolDetails as { truncation?: unknown }).truncation
          : undefined;
        const truncated = truncation !== null && typeof truncation === "object"
          && (truncation as { truncated?: unknown }).truncated === true;

        if (this.details && (text !== "" || patch !== "")) {
          this.details.put(callID, {
            text,
            patch: patch === "" ? undefined : patch,
            truncated,
            fullOutputPath: fullOutputPath === "" ? undefined : fullOutputPath,
          });
        }

        const body: ToolFinishedBody = {
          call_id: callID,
          name: started?.name || name,
          status: isError ? "error" : "ok",
          summary: started?.summary ?? "",
          files: started?.files ?? [],
          detail: text !== "" || patch !== "",
          patch: patch !== "",
          truncated,
          full_output: fullOutputPath !== "",
        };
        // The failure headline rides along: a card the user has to expand to
        // learn WHAT went wrong is a card they have to expand every time.
        if (isError && text !== "") body.error = clipSummary(text);
        this.stream.emit("tool_finished", body);
        return;
      }

      case "message_start": {
        // Nothing is emitted here. pi opens a message before anyone knows
        // whether it will have anything to say: an assistant turn that only
        // calls tools ends with empty text, and its tool RESULT arrives as a
        // message of its own carrying the tool's entire output. Emitting on
        // open would put both in the transcript — an empty bubble, and the very
        // inlined output the tool card exists to keep out of it.
        //
        // So a message appears when it has content: the first delta mints it
        // (requireMessageID), and one that streamed nothing is decided at its
        // end.
        this.deltas.flush();
        this.currentMessageID = null;
        this.currentRole = messageRole(event.message);
        return;
      }

      case "message_update": {
        const inner = event.assistantMessageEvent as { type?: string; delta?: unknown } | undefined;
        // Only assistant TEXT streams to the pane. Thinking and tool-call
        // argument deltas are their own render surfaces and would otherwise land
        // in the message body as noise.
        if (!inner || inner.type !== "text_delta" || typeof inner.delta !== "string") return;
        if (UNRENDERED_ROLES.has(this.currentRole)) return;
        this.deltas.push(this.requireMessageID(), inner.delta);
        return;
      }

      case "message_end": {
        this.deltas.flush();
        const role = messageRole(event.message);
        const text = messageText(event.message);
        const open = this.currentMessageID;
        this.currentMessageID = null;
        this.currentRole = "assistant";
        // A tool's output belongs to its card, which fetches it on demand. A
        // message that never said anything is not a message.
        if (UNRENDERED_ROLES.has(role) || (open === null && text === "")) return;
        const id = open ?? this.mintMessage(role);
        this.stream.emit("message_end", { id, role, text } satisfies MessageEndBody);
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
   * The id of the message the text belongs to, opening one if it is the first
   * thing that message has said. Also covers a delta whose `message_start`
   * never arrived — pi opening a message shape this mapper does not model must
   * not drop the agent's words on the floor.
   */
  private requireMessageID(): string {
    if (this.currentMessageID === null) this.currentMessageID = this.mintMessage(this.currentRole);
    return this.currentMessageID;
  }

  /** Opens a message on the wire and returns its id. */
  private mintMessage(role: string): string {
    this.messageCounter += 1;
    const id = `m${this.messageCounter}`;
    this.stream.emit("message_start", { id, role } satisfies MessageStartBody);
    return id;
  }
}

/**
 * One drawn thing in a conversation, in the shape a snapshot carries it.
 *
 * These mirror what the app's own store builds from the live stream, because a
 * snapshot has to be indistinguishable from having watched the stream from the
 * start — the client replaces its transcript with one and must not be able to
 * tell the difference.
 */
export interface SnapshotMessageItem {
  kind: "message";
  id: string;
  role: string;
  text: string;
  /** The text is still arriving. True only for the message a live run has open. */
  streaming: boolean;
}

export interface SnapshotToolItem {
  kind: "tool";
  call_id: string;
  name: string;
  summary: string;
  files: string[];
  status: "running" | "ok" | "error";
  error?: string;
  detail: boolean;
  patch: boolean;
  truncated: boolean;
  full_output: boolean;
}

export type SnapshotItem = SnapshotMessageItem | SnapshotToolItem;

/**
 * The whole of what a client needs to draw a conversation it has not been
 * watching: the transcript, what the agent is doing, and what it has not read.
 *
 * This is the conversation half of the terminal's restore contract. There, the
 * daemon worker serializes its terminal and the client writes the dump and then
 * dedups the live stream against `last_seq`; here the host serializes its
 * transcript and the client does the same against the envelope spine. The
 * snapshot REPLACES what the client had, for the same reason the VT dump does:
 * one authority, no merge, and two clients that attach see the same thing.
 *
 * `truncated` says older items exist than `items` carries — the window is
 * bounded (see SNAPSHOT_ITEM_LIMIT) and paging past it is slice 5's.
 */
export interface ConversationSnapshotBody {
  items: SnapshotItem[];
  /** Items in the whole conversation, including the ones clipped from `items`. */
  total: number;
  truncated: boolean;
  /** A run is open right now, so the composer sends a steer rather than a prompt. */
  running: boolean;
  /** pi's queues as of now, so an attaching client draws what is still unread. */
  queue: QueueUpdateBody;
}

/**
 * How many items one snapshot carries.
 *
 * Every item is one DOM node in a pane that draws all of them, so this is a
 * render budget before it is a wire budget. 500 is past the length of any
 * conversation that is still readable by scrolling — the measured transcript for
 * a slice-3 session that read, printed 5,000 lines, edited and slept was 406
 * CHARACTERS across a handful of items — and a session long enough to feel it is
 * the scroll-back paging slice 5 owns, not a case to silently truncate.
 */
export const SNAPSHOT_ITEM_LIMIT = 500;

/**
 * How many bytes of item text one snapshot carries.
 *
 * The corpus this design is grounded on (claude JSONL transcripts: p50 0.15 MB,
 * p99 11.6 MB, ~0.4% message text) puts roughly 46 KB of actual message text in
 * a p99 transcript. 1 MB is ~20x that, and three orders of magnitude under the
 * daemon's 64 MB envelope-line ceiling. Tool OUTPUT is not in here at all — a
 * snapshot's tool items are the same name-and-a-line declarations the live
 * stream sends, and the output is still fetched per card.
 */
export const SNAPSHOT_BYTES_LIMIT = 1 << 20;

function itemBytes(item: SnapshotItem): number {
  if (item.kind === "message") return item.text.length + item.role.length + item.id.length;
  return item.summary.length + item.name.length + (item.error?.length ?? 0)
    + item.files.reduce((total, file) => total + file.length, 0);
}

const isSnapshotTool = (callID: string) => (item: SnapshotItem): item is SnapshotToolItem =>
  item.kind === "tool" && item.call_id === callID;
const isSnapshotMessage = (id: string) => (item: SnapshotItem): item is SnapshotMessageItem =>
  item.kind === "message" && item.id === id;

/**
 * The host's own copy of the transcript the app is drawing.
 *
 * It is fed the host's OWN envelopes — the same bodies, through the same sink —
 * rather than pi's events, which is what keeps it from drifting from what a
 * client watching the stream ended up with. There is exactly one reducer on each
 * side of the wire and they consume identical input.
 *
 * Why the host holds one at all: a client attaching mid-run needs the message
 * that is streaming right now and the tool that is running right now, and
 * neither is in pi's session file yet — pi persists a message when it ends. A
 * snapshot rebuilt from disk would hand an attaching client a conversation that
 * stops one paragraph short of the truth, and a broadcast replace would take
 * that paragraph away from everyone else too.
 *
 * Bounded by both limits above; `dropped` is what makes a clipped snapshot say
 * so instead of looking complete.
 */
export class TranscriptStore {
  private items: SnapshotItem[] = [];
  private bytes = 0;
  private dropped = 0;
  private running = false;
  private queue: QueueUpdateBody = { steering: [], followUp: [] };

  constructor(
    private readonly itemLimit: number = SNAPSHOT_ITEM_LIMIT,
    private readonly bytesLimit: number = SNAPSHOT_BYTES_LIMIT,
  ) {}

  /** Replaces the transcript with reconstructed history. Used once, at revive. */
  seed(items: SnapshotItem[]): void {
    this.items = [];
    this.bytes = 0;
    this.dropped = 0;
    for (const item of items) this.push(item);
  }

  /**
   * Applies one of the host's own envelopes. Kinds that say nothing about the
   * transcript — a tool's fetched detail, a snapshot itself — are ignored.
   */
  apply(kind: EnvelopeKind, body: unknown): void {
    const fields = (body ?? {}) as Record<string, unknown>;
    switch (kind) {
      case "run_started":
        this.running = true;
        return;
      case "run_settled": {
        this.running = false;
        // Whatever was open when the run closed is closed. Same rule the app
        // applies, for the same reason: the host emits message_end before the
        // settle, so a message still open here ended under the run.
        for (const item of this.items) {
          if (item.kind === "message") item.streaming = false;
          else if (item.status === "running") {
            item.status = "error";
            item.error = "the run ended before this tool reported";
          }
        }
        return;
      }
      case "queue_update":
        this.queue = { steering: stringList(fields.steering), followUp: stringList(fields.followUp) };
        return;
      case "message_start": {
        const id = readString(fields, "id");
        if (id === "" || this.items.some(isSnapshotMessage(id))) return;
        this.push({ kind: "message", id, role: readString(fields, "role") || "assistant", text: "", streaming: true });
        return;
      }
      case "message_delta": {
        const id = readString(fields, "id");
        const delta = readString(fields, "text");
        if (id === "" || delta === "") return;
        const open = this.items.find(isSnapshotMessage(id));
        if (!open) {
          this.push({ kind: "message", id, role: "assistant", text: delta, streaming: true });
          return;
        }
        open.text += delta;
        this.bytes += delta.length;
        this.trim();
        return;
      }
      case "message_end": {
        const id = readString(fields, "id");
        if (id === "") return;
        const settled: SnapshotMessageItem = {
          kind: "message",
          id,
          role: readString(fields, "role") || "assistant",
          text: readString(fields, "text"),
          streaming: false,
        };
        this.replaceOrPush(isSnapshotMessage(id), settled);
        return;
      }
      case "tool_started": {
        const callID = readString(fields, "call_id");
        if (callID === "" || this.items.some(isSnapshotTool(callID))) return;
        this.push({
          kind: "tool",
          call_id: callID,
          name: readString(fields, "name"),
          summary: readString(fields, "summary"),
          files: stringList(fields.files),
          status: "running",
          detail: false,
          patch: false,
          truncated: false,
          full_output: false,
        });
        return;
      }
      case "tool_finished": {
        const callID = readString(fields, "call_id");
        if (callID === "") return;
        const existing = this.items.find(isSnapshotTool(callID));
        const error = readString(fields, "error");
        const finished: SnapshotToolItem = {
          kind: "tool",
          call_id: callID,
          name: readString(fields, "name") || existing?.name || "",
          summary: readString(fields, "summary") || existing?.summary || "",
          files: stringList(fields.files).length > 0 ? stringList(fields.files) : existing?.files ?? [],
          status: readString(fields, "status") === "error" ? "error" : "ok",
          ...(error === "" ? {} : { error }),
          detail: fields.detail === true,
          patch: fields.patch === true,
          truncated: fields.truncated === true,
          full_output: fields.full_output === true,
        };
        this.replaceOrPush(isSnapshotTool(callID), finished);
        return;
      }
      default:
        return;
    }
  }

  snapshot(): ConversationSnapshotBody {
    return {
      items: this.items.map((item) => ({ ...item })),
      total: this.items.length + this.dropped,
      truncated: this.dropped > 0,
      running: this.running,
      queue: { steering: [...this.queue.steering], followUp: [...this.queue.followUp] },
    };
  }

  /** For the log line that says how close a real session gets to the window. */
  get size(): number {
    return this.items.length;
  }

  get retainedBytes(): number {
    return this.bytes;
  }

  private push(item: SnapshotItem): void {
    this.items.push(item);
    this.bytes += itemBytes(item);
    this.trim();
  }

  private replaceOrPush(match: (item: SnapshotItem) => boolean, replacement: SnapshotItem): void {
    const index = this.items.findIndex(match);
    if (index < 0) {
      this.push(replacement);
      return;
    }
    this.bytes -= itemBytes(this.items[index]!);
    this.items[index] = replacement;
    this.bytes += itemBytes(replacement);
    this.trim();
  }

  /** Drops the oldest items until both budgets hold. Never drops the newest. */
  private trim(): void {
    while (this.items.length > 1 && (this.items.length > this.itemLimit || this.bytes > this.bytesLimit)) {
      const dropped = this.items.shift()!;
      this.bytes -= itemBytes(dropped);
      this.dropped += 1;
    }
  }
}

/** The subset of a pi session entry this host reads. pi's entry union grows. */
export interface SessionEntryLike {
  type: string;
  id: string;
  message?: unknown;
}

/** What reconstructing a session file produced. */
export interface ReconstructedTranscript {
  items: SnapshotItem[];
  /** Each finished call's held detail, so an expanded card works after a revive. */
  details: Map<string, ToolDetail>;
}

function contentBlocks(message: unknown): unknown[] {
  if (!message || typeof message !== "object") return [];
  const content = (message as { content?: unknown }).content;
  return Array.isArray(content) ? content : [];
}

/**
 * Rebuilds the drawn transcript from a reopened pi session file.
 *
 * This is what "history intact" means after a crash: pi's entries are messages
 * and tool results, and the pane draws messages and tool cards, so the same
 * derivations the live mapper runs are run again over the file. A tool card
 * comes back with its summary, its files, its status and its OUTPUT held for an
 * expand — a revived session whose cards were all empty would be history in name
 * only.
 *
 * Message ids are namespaced `h:` after the entry that produced them, so a
 * revived host minting `m1` for its next reply cannot collide with a message
 * that came off disk.
 */
export function reconstructTranscript(entries: SessionEntryLike[]): ReconstructedTranscript {
  const items: SnapshotItem[] = [];
  const details = new Map<string, ToolDetail>();
  const toolsByCallID = new Map<string, SnapshotToolItem>();

  for (const entry of entries) {
    if (entry.type !== "message") continue;
    const message = entry.message;
    const role = messageRole(message);
    if (role === "toolResult") {
      const callID = readString(message, "toolCallId");
      const card = callID === "" ? undefined : toolsByCallID.get(callID);
      if (!card) continue;
      const result = message as { isError?: unknown; details?: unknown };
      const text = toolResultText(message);
      const resultDetails = result.details;
      const patch = readString(resultDetails, "patch");
      const fullOutputPath = readString(resultDetails, "fullOutputPath");
      const truncation = resultDetails && typeof resultDetails === "object"
        ? (resultDetails as { truncation?: unknown }).truncation
        : undefined;
      const truncated = truncation !== null && typeof truncation === "object"
        && (truncation as { truncated?: unknown }).truncated === true;
      card.status = result.isError === true ? "error" : "ok";
      if (result.isError === true && text !== "") card.error = clipSummary(text);
      card.detail = text !== "" || patch !== "";
      card.patch = patch !== "";
      card.truncated = truncated;
      card.full_output = fullOutputPath !== "";
      if (card.detail) {
        details.set(callID, {
          text,
          patch: patch === "" ? undefined : patch,
          truncated,
          fullOutputPath: fullOutputPath === "" ? undefined : fullOutputPath,
        });
      }
      continue;
    }
    const text = messageText(message);
    if (text !== "") {
      items.push({ kind: "message", id: `h:${entry.id}`, role, text, streaming: false });
    }
    for (const block of contentBlocks(message)) {
      if (!block || typeof block !== "object" || (block as { type?: unknown }).type !== "toolCall") continue;
      const callID = readString(block, "id");
      if (callID === "") continue;
      const name = readString(block, "name");
      const args = (block as { arguments?: unknown }).arguments;
      const card: SnapshotToolItem = {
        kind: "tool",
        call_id: callID,
        name,
        summary: toolSummary(name, args),
        files: toolFiles(name, args),
        // Every call starts as running here and is answered by its own
        // toolResult entry below. One that never gets an answer is a call the
        // host died inside, which is exactly what conversationInterrupted reads.
        status: "running",
        detail: false,
        patch: false,
        truncated: false,
        full_output: false,
      };
      toolsByCallID.set(callID, card);
      items.push(card);
    }
  }
  return { items, details };
}

/**
 * Whether a reconstructed conversation stopped mid-thought.
 *
 * One rule: a conversation is interrupted unless the agent had the last word.
 * Every way a run really ends leaves an assistant message behind — pi persists
 * one per turn, and the turn that decides to stop is a turn. Anything else at
 * the end means the host died inside the run: a prompt nothing answered, a tool
 * call with no result, or a result the agent never got to read.
 *
 * This is the whole basis for a revived session declaring `waiting_input` rather
 * than `idle`. An empty conversation is nobody's interruption — it is a fresh
 * session, which is idle.
 */
export function conversationInterrupted(items: SnapshotItem[]): boolean {
  const last = items[items.length - 1];
  if (!last) return false;
  return last.kind !== "message" || last.role !== "assistant";
}

/**
 * Whether a host that has just reopened its conversation still owes it the
 * message the launch was given.
 *
 * A launch prompt — today that is a delegation brief — belongs to the SESSION,
 * not to the process that first received it, so the daemon hands
 * the same one to every replacement host. That is what saves a delegation from
 * a crash before pi's first assistant message, which leaves no session file at
 * all: the replacement is a fresh session, and a fresh session with no brief is
 * an agent with nothing to do.
 *
 * Emptiness is the whole test, and it is exact rather than approximate: pi
 * persists nothing until a message ends, so a conversation that reopens with
 * items in it is one the prompt already reached — including the interrupted
 * case, where the prompt is there and the answer is not. Asking again there
 * would make the agent do the task twice; `waiting_input` and a nudge are the
 * way out of that one, and they are the user's to spend.
 */
export function launchPromptIsUndelivered(prompt: string, reopened: SnapshotItem[]): boolean {
  return prompt.trim() !== "" && reopened.length === 0;
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
 *
 * Two more verbs are about what is already in flight rather than new text:
 *
 *   tool_detail  what an expanded card shows, answered as a `tool_detail`
 *                envelope. `full` reads pi's full-output file instead of the
 *                clipped result it returned to the model.
 *   clear_queue  drops everything queued and unread. pi clears both queues at
 *                once — there is no per-entry removal — and answers with its
 *                own `queue_update`, so the strip empties on pi's word.
 *   snapshot     the whole conversation as it stands, for a client that has not
 *                been watching the stream. Answered as a `conversation_snapshot`
 *                envelope, which is the conversation's version of the terminal's
 *                restore dump.
 */
export type HostVerbWithText = { verb: "prompt" | "steer" | "follow_up"; text: string };
export type HostVerb =
  | HostVerbWithText
  | { verb: "shutdown" }
  | { verb: "clear_queue" }
  | { verb: "snapshot" }
  | { verb: "tool_detail"; callID: string; full: boolean };

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
  if (verb === "clear_queue") return { verb: "clear_queue" };
  if (verb === "snapshot") return { verb: "snapshot" };
  if (verb === "tool_detail") {
    const callID = (value as { call_id?: unknown }).call_id;
    if (typeof callID !== "string" || callID.trim() === "") throw new Error("tool_detail verb needs a call_id");
    return { verb: "tool_detail", callID, full: (value as { full?: unknown }).full === true };
  }
  throw new Error(`unsupported verb ${JSON.stringify(verb)}`);
}
