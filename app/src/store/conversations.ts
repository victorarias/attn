import { create } from 'zustand';

// The app's picture of a conversation session — the ones whose agent runs in a
// headless host instead of a PTY. It is built entirely from the host's envelope
// stream, which is why this store is append-only and never asks the daemon
// anything: the stream is the conversation.
//
// Two kinds of envelope land here. Semantic ones (session_ready, run_started,
// run_settled, tool_started, tool_finished) say where the session is and what
// the agent did; render ones (message_start, message_delta, message_end,
// queue_update, tool_detail) say what it said, what it has not read yet, and
// what one opened tool card shows. An unknown kind is ignored on purpose — the
// host is pinned to a pi version the app is not, and a kind this build does not
// draw must not break the ones it does.
//
// What a tool call actually read, wrote or printed is NOT in this store until
// the user opens the card. A tool declaration is a name, a line and a status;
// the output is fetched with `agent_tool_detail` and arrives as its own
// envelope. That is the whole reason a long conversation stays small: the
// corpus behind this design is a p99 11.6 MB transcript with ~0.4% message
// text, and eagerly inlining tool output is how it got that way.

export interface ConversationMessage {
  id: string;
  role: string;
  text: string;
  // True between message_start and message_end: the text is still arriving.
  streaming: boolean;
}

/** What an opened tool card shows, fetched on demand. */
export interface ConversationToolDetail {
  text: string;
  /** A unified patch, for tools that produce one. Drawn as a diff. */
  patch?: string;
  /** `text` is the whole output, read back from the file pi wrote it to. */
  full: boolean;
  /** More output exists than `text` shows. */
  truncated: boolean;
  fullOutputPath?: string;
  /** The fetch failed, or answered with less than was asked for, and why. */
  error?: string;
}

/**
 * One tool call, drawn as a card in the transcript where it happened.
 *
 * It exists from the moment the call starts — a card that appears only once the
 * tool finishes would leave a session that is grinding through a long command
 * looking like it stopped talking.
 */
export interface ConversationToolCall {
  callId: string;
  name: string;
  /** One line naming the call: the command, the path, the pattern. */
  summary: string;
  /** Files the call touched. Search roots live in the summary, not here. */
  files: string[];
  status: 'running' | 'ok' | 'error';
  /** The failure headline, when it failed. Shown without opening the card. */
  error?: string;
  /** Detail can be fetched for this call. */
  hasDetail: boolean;
  /** The detail carries a patch, so the card draws a diff. */
  hasPatch: boolean;
  /** pi clipped the output it gave the model. */
  truncated: boolean;
  /** pi kept the whole output on disk, so `full` detail can be asked for. */
  fullOutput: boolean;
  detail?: ConversationToolDetail;
}

/**
 * The transcript, in the order it happened: what was said and what was done.
 *
 * One list rather than two, because a tool card's whole meaning is where it
 * sits — after the sentence that announced it, before the one that reports what
 * it found.
 */
export type ConversationItem =
  | ({ kind: 'message' } & ConversationMessage)
  | ({ kind: 'tool' } & ConversationToolCall)
  | ({ kind: 'notice' } & ConversationNotice);

/**
 * The key an item is addressed by, across snapshots, pages and this build's own
 * lists. It is the host's `snapshotItemKey`, and the two have to agree: a page is
 * asked for by the key of the oldest item a client holds, and the host answers by
 * finding that key in its own transcript.
 */
export function conversationItemKey(item: ConversationItem): string {
  return item.kind === 'tool' ? `tool:${item.callId}` : `${item.kind}:${item.id}`;
}

/**
 * Something that happened to the conversation rather than in it: a compaction,
 * an automatic retry, a model switch.
 *
 * These exist because the alternative is silence. A compaction on a long
 * conversation is thirty seconds during which the agent says nothing and does
 * nothing visible, and a user watching that has no way to tell it apart from a
 * hang. `done` is false while it is still happening, which is what draws the row
 * as in-progress and then settles it in place.
 */
export interface ConversationNotice {
  id: string;
  level: 'info' | 'warn' | 'error';
  text: string;
  done: boolean;
}

/**
 * When the agent reads a message the user sends.
 *
 * `prompt` opens a run and is what the composer sends when nothing is running.
 * The other two exist only while a run is open: a steer cuts in at the agent's
 * next turn boundary, a follow-up waits for the run to finish. The host resolves
 * either of them on an idle session into a plain run, so this is a statement of
 * intent rather than a claim about what the agent is doing.
 */
export type AgentPromptMode = 'prompt' | 'steer' | 'follow_up';

/**
 * What the agent has been sent and not yet read, straight from pi's own queues.
 *
 * The app never adds to this itself — a message the user sends appears here
 * when the host says pi queued it, and disappears when the host says pi read
 * it, which is the whole of "queued, then seen". Inventing a local optimistic
 * entry would mean two sources for one truth and a queue that can get stuck.
 * The same rule holds for cancelling: `agent_clear_queue` empties pi's queues
 * and pi's own queue_update is what empties this one.
 */
export interface ConversationQueue {
  steering: string[];
  followUp: string[];
}

export interface ConversationState {
  items: ConversationItem[];
  // Which host built this transcript. A host mints one at startup, carries it on
  // every snapshot and page, and a new one means the conversation was rebuilt
  // from disk rather than continued — see the snapshot case for why that is the
  // difference between splicing and replacing.
  epoch: string;
  // The host holds conversation older than the oldest item here, and will serve
  // it a page at a time. False both when the start has been reached and when
  // nothing has ever said otherwise.
  hasMoreBefore: boolean;
  // A page has been asked for and not yet answered. Only ever one: a page is
  // addressed by the oldest item held, so a second request while one is in
  // flight would ask for the same page twice.
  loadingHistory: boolean;
  // How many items the host's retention budget has dropped for good. No page
  // will ever answer with them, so unlike `hasMoreBefore` this does not go away
  // by scrolling: it is the reason the transcript starts where it does, and the
  // transcript says so rather than appearing to begin mid-thought.
  droppedBefore: number;
  // The model this session's agent is running on, and everything this machine
  // could switch it to. Both come from the host, which reads them out of pi.
  model: string;
  models: string[];
  // A run is open: the agent is working, and a message the user sends now is a
  // steer or a follow-up rather than a prompt.
  running: boolean;
  // A prompt is in flight and the host has not acknowledged it yet. This is the
  // ONLY window the composer is shut in: until run_started lands there is no
  // run to steer, and a steer sent into that gap would reach a host that has
  // not opened one. It closes on the host's own word — run_started, or the
  // settle for a prompt that reached nobody.
  awaitingRun: boolean;
  // The host has its pi session up and will accept prompts.
  ready: boolean;
  // Sent and not yet read. See ConversationQueue.
  queue: ConversationQueue;
  // The highest envelope seq applied. Envelopes are minted in order by one
  // host, so anything at or below this is a duplicate to drop.
  lastSeq: number;
}

const emptyQueue: ConversationQueue = { steering: [], followUp: [] };

const emptyConversation: ConversationState = {
  items: [],
  epoch: '',
  hasMoreBefore: false,
  loadingHistory: false,
  droppedBefore: 0,
  model: '',
  models: [],
  running: false,
  awaitingRun: false,
  ready: false,
  queue: emptyQueue,
  lastSeq: 0,
};

interface ConversationsStore {
  conversations: Record<string, ConversationState>;
  applyEnvelope: (sessionId: string, seq: number, kind: string, body: Record<string, unknown>) => void;
  // A page of older history has been asked for. Kept separate from the envelope
  // path because the request leaves the app and the answer comes back on the
  // broadcast stream, so nothing correlates the two but the anchor.
  historyRequested: (sessionId: string) => void;
  // Opens the run at send time, before the host has said anything about it.
  promptSent: (sessionId: string) => void;
  // The host is gone. The transcript stays — a dead conversation is still worth
  // reading — but nothing about it is live any more.
  hostExited: (sessionId: string) => void;
  clearConversation: (sessionId: string) => void;
  // Drops every conversation whose session is gone. A transcript outlives its
  // host on purpose — an exited session stays readable — so the sessions list
  // is what bounds this store, not the host's exit.
  retainConversations: (liveSessionIds: string[]) => void;
}

function text(body: Record<string, unknown>, key: string): string {
  const value = body[key];
  return typeof value === 'string' ? value : '';
}

function flag(body: Record<string, unknown>, key: string): boolean {
  return body[key] === true;
}

function count(body: Record<string, unknown>, key: string): number {
  const value = body[key];
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? Math.floor(value) : 0;
}

function stringList(body: Record<string, unknown>, key: string): string[] {
  const value = body[key];
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === 'string') : [];
}

/** Replaces one item in place, matched by a predicate. Returns null if absent. */
function replaceItem(
  items: ConversationItem[],
  match: (item: ConversationItem) => boolean,
  update: (item: ConversationItem) => ConversationItem,
): ConversationItem[] | null {
  const index = items.findIndex(match);
  if (index < 0) return null;
  const next = items.slice();
  next[index] = update(next[index]);
  return next;
}

const isTool = (callId: string) => (item: ConversationItem) => item.kind === 'tool' && item.callId === callId;
const isMessage = (id: string) => (item: ConversationItem) => item.kind === 'message' && item.id === id;
const isNotice = (id: string) => (item: ConversationItem) => item.kind === 'notice' && item.id === id;

function queueFrom(value: unknown): ConversationQueue {
  if (!value || typeof value !== 'object') return emptyQueue;
  const body = value as Record<string, unknown>;
  return { steering: stringList(body, 'steering'), followUp: stringList(body, 'followUp') };
}

function noticeLevel(value: string): ConversationNotice['level'] {
  return value === 'warn' || value === 'error' ? value : 'info';
}

/** Reads a wire item list, dropping shapes this build does not draw. */
function snapshotItems(value: unknown): ConversationItem[] | null {
  if (!Array.isArray(value)) return null;
  return value.map(snapshotItem).filter((item): item is ConversationItem => item !== null);
}

/**
 * Reads one snapshot item off the wire, or null if it is not one this build
 * draws. A shape the app does not recognise is dropped rather than rendered as
 * a blank row: the host is pinned to a pi version the app is not.
 */
function snapshotItem(value: unknown): ConversationItem | null {
  if (!value || typeof value !== 'object') return null;
  const body = value as Record<string, unknown>;
  if (body.kind === 'message') {
    const id = text(body, 'id');
    if (!id) return null;
    return {
      kind: 'message',
      id,
      role: text(body, 'role') || 'assistant',
      text: text(body, 'text'),
      streaming: flag(body, 'streaming'),
    };
  }
  if (body.kind === 'notice') {
    const id = text(body, 'id');
    if (!id) return null;
    return { kind: 'notice', id, level: noticeLevel(text(body, 'level')), text: text(body, 'text'), done: flag(body, 'done') };
  }
  if (body.kind === 'tool') {
    const callId = text(body, 'call_id');
    if (!callId) return null;
    const status = text(body, 'status');
    const failure = text(body, 'error');
    return {
      kind: 'tool',
      callId,
      name: text(body, 'name'),
      summary: text(body, 'summary'),
      files: stringList(body, 'files'),
      status: status === 'running' ? 'running' : status === 'error' ? 'error' : 'ok',
      error: failure === '' ? undefined : failure,
      hasDetail: flag(body, 'detail'),
      hasPatch: flag(body, 'patch'),
      truncated: flag(body, 'truncated'),
      fullOutput: flag(body, 'full_output'),
    };
  }
  return null;
}

/**
 * Splices a snapshot's window onto what a client already holds, or replaces.
 *
 * The splice hinges on one question: does this client hold the window's oldest
 * item? If it does, everything before it is scroll-back this client paged in and
 * the host still has — keep it, and take the window as the tail. If it does not,
 * this client is behind the window (or empty), and the window IS the newest
 * truth: replace.
 *
 * `hasMoreBefore` follows the same split. After a splice the oldest item held is
 * unchanged, so what the client already knew about the conversation behind it
 * still holds; the snapshot's own answer is about the window's start, which is
 * no longer the start of what is drawn.
 *
 * `droppedBefore` is the snapshot's own count on both sides. The host is the
 * authority on what it has thrown away, and within one epoch that number only
 * grows; across epochs a rebuilt host has read pi's session file from the start,
 * so carrying the dead host's losses forward would claim a gap this transcript
 * does not have.
 */
function mergeSnapshotWindow(
  current: ConversationState,
  epoch: string,
  window: ConversationItem[],
  hasMore: boolean,
  dropped: number,
): Pick<ConversationState, 'items' | 'epoch' | 'hasMoreBefore' | 'droppedBefore'> {
  const replace = { items: window, epoch, hasMoreBefore: hasMore, droppedBefore: dropped };
  if (epoch === '' || epoch !== current.epoch || window.length === 0) return replace;
  const anchor = conversationItemKey(window[0]);
  const index = current.items.findIndex((item) => conversationItemKey(item) === anchor);
  if (index <= 0) return replace;
  return {
    items: [...current.items.slice(0, index), ...window],
    epoch,
    hasMoreBefore: current.hasMoreBefore,
    droppedBefore: dropped,
  };
}

function applyToConversation(
  current: ConversationState,
  seq: number,
  kind: string,
  body: Record<string, unknown>,
): ConversationState {
  switch (kind) {
    case 'session_ready': {
      // A host that has just come up has no run open, whatever the dead one was
      // doing when it went. The transcript is left alone: the snapshot that
      // follows replaces it, and a host too old to send one leaves the user with
      // the history they were already reading rather than an empty pane.
      const models = stringList(body, 'models');
      return {
        ...current,
        ready: true,
        running: false,
        awaitingRun: false,
        loadingHistory: false,
        queue: emptyQueue,
        model: text(body, 'model') || current.model,
        models: models.length > 0 ? models : current.models,
      };
    }
    case 'conversation_snapshot': {
      const window = snapshotItems(body.items);
      if (!window) return current;
      // A snapshot carries the NEWEST window of the host's transcript, not all
      // of it, and it is broadcast — so a client that has paged back through a
      // long conversation would lose its scroll-back every time any other window
      // attached. The epoch is what makes that avoidable: same epoch means the
      // same host built both, so the window can be spliced onto the older items
      // this client already holds. A different epoch means the transcript was
      // rebuilt (a revived host reading pi's session file), and then replacing
      // is the only honest answer — the item ids came from somewhere else.
      const merged = mergeSnapshotWindow(
        current,
        text(body, 'epoch'),
        window,
        flag(body, 'has_more'),
        count(body, 'dropped'),
      );
      return {
        ...current,
        ...merged,
        ready: true,
        running: flag(body, 'running'),
        awaitingRun: false,
        loadingHistory: false,
        queue: queueFrom(body.queue),
      };
    }
    case 'conversation_page': {
      const items = snapshotItems(body.items);
      // A page is addressed by the item it comes before, and it is broadcast, so
      // every client sees every page anyone asked for. A client takes one only
      // when the anchor is the oldest item it is holding — anything else is a
      // page for a window scrolled somewhere else, and prepending it would put a
      // gap in this transcript.
      if (!items || current.items.length === 0) return current;
      if (text(body, 'epoch') !== current.epoch) return current;
      if (text(body, 'before') !== conversationItemKey(current.items[0])) return current;
      const held = new Set(current.items.map(conversationItemKey));
      const older = items.filter((item) => !held.has(conversationItemKey(item)));
      return {
        ...current,
        items: older.length > 0 ? [...older, ...current.items] : current.items,
        hasMoreBefore: flag(body, 'has_more'),
        loadingHistory: false,
      };
    }
    case 'model_changed': {
      // The host's answer, whether the switch landed or was refused: the model
      // named here is the one actually in force. A picker that moved early
      // corrects itself from this.
      const model = text(body, 'model');
      return model === '' || model === current.model ? current : { ...current, model };
    }
    case 'notice': {
      const id = text(body, 'id');
      if (!id) return current;
      const notice: ConversationItem = {
        kind: 'notice',
        id,
        level: noticeLevel(text(body, 'level')),
        text: text(body, 'text'),
        done: flag(body, 'done'),
      };
      // One row per notice, settling in place: a compaction that opened a
      // spinner is the same row that says it finished.
      const replaced = replaceItem(current.items, isNotice(id), () => notice);
      return { ...current, items: replaced ?? [...current.items, notice] };
    }
    case 'run_started':
      return { ...current, running: true, awaitingRun: false };
    case 'run_settled': {
      // Whatever was still streaming when the run closed is finished: the host
      // emits message_end before run_settled, so a message left open here means
      // the run ended under it and it will never grow again. The same is true
      // of a tool card still showing as running — pi reports every tool it
      // starts, so one that never reported is one whose run ended under it.
      const items = current.items.map((item) => {
        if (item.kind === 'message') return item.streaming ? { ...item, streaming: false } : item;
        // A notice settles on its own event, or not at all: a compaction the
        // host never reported the end of is a compaction nobody knows the
        // outcome of, and claiming it failed would be inventing one.
        if (item.kind !== 'tool' || item.status !== 'running') return item;
        return { ...item, status: 'error' as const, error: 'the run ended before this tool reported' };
      });
      // A run that ended badly says so in the transcript. Otherwise a prompt
      // that failed outright — an unauthenticated provider, a model that
      // refused — reads as an agent that answered with silence.
      const failure = text(body, 'error');
      if (failure !== '') {
        items.push({ kind: 'message', id: `error-${seq}`, role: 'error', text: failure, streaming: false });
      }
      // The queue empties with the run. pi drains a follow-up by starting the
      // next run rather than by ending this one, so anything still shown here
      // when a run closes is a queue this host will never speak about again —
      // most often because the host died under it.
      return { ...current, running: false, awaitingRun: false, items, queue: emptyQueue };
    }
    case 'queue_update':
      return { ...current, queue: { steering: stringList(body, 'steering'), followUp: stringList(body, 'followUp') } };
    case 'tool_started': {
      const callId = text(body, 'call_id');
      if (!callId || current.items.some(isTool(callId))) return current;
      return {
        ...current,
        items: [...current.items, {
          kind: 'tool',
          callId,
          name: text(body, 'name'),
          summary: text(body, 'summary'),
          files: stringList(body, 'files'),
          status: 'running',
          hasDetail: false,
          hasPatch: false,
          truncated: false,
          fullOutput: false,
        }],
      };
    }
    case 'tool_finished': {
      const callId = text(body, 'call_id');
      if (!callId) return current;
      const failure = text(body, 'error');
      const finished = (item: ConversationToolCall): ConversationItem => ({
        ...item,
        kind: 'tool',
        name: text(body, 'name') || item.name,
        summary: text(body, 'summary') || item.summary,
        files: stringList(body, 'files').length > 0 ? stringList(body, 'files') : item.files,
        status: text(body, 'status') === 'error' ? 'error' : 'ok',
        error: failure === '' ? undefined : failure,
        hasDetail: flag(body, 'detail'),
        hasPatch: flag(body, 'patch'),
        truncated: flag(body, 'truncated'),
        fullOutput: flag(body, 'full_output'),
      });
      const replaced = replaceItem(current.items, isTool(callId), (item) => finished(item as ConversationToolCall));
      if (replaced) return { ...current, items: replaced };
      // A finish for a call we never saw start. Draw it rather than drop it:
      // losing the record of what the agent did is worse than a card that
      // never showed as running.
      return {
        ...current,
        items: [...current.items, finished({
          callId,
          name: '',
          summary: '',
          files: [],
          status: 'ok',
          hasDetail: false,
          hasPatch: false,
          truncated: false,
          fullOutput: false,
        })],
      };
    }
    case 'tool_detail': {
      const callId = text(body, 'call_id');
      if (!callId) return current;
      const patch = text(body, 'patch');
      const failure = text(body, 'error');
      const fullOutputPath = text(body, 'full_output_path');
      const detail: ConversationToolDetail = {
        text: text(body, 'text'),
        full: flag(body, 'full'),
        truncated: flag(body, 'truncated'),
        ...(patch === '' ? {} : { patch }),
        ...(fullOutputPath === '' ? {} : { fullOutputPath }),
        ...(failure === '' ? {} : { error: failure }),
      };
      // The answer is addressed by call id and reaches every client, so a card
      // opened in another window fills in here too. A `full` answer supersedes
      // the clipped one; a clipped one never overwrites a full one that already
      // landed.
      const replaced = replaceItem(current.items, isTool(callId), (item) => {
        const tool = item as ConversationToolCall;
        if (tool.detail?.full && !detail.full) return item;
        return { ...tool, kind: 'tool', detail };
      });
      return replaced ? { ...current, items: replaced } : current;
    }
    case 'message_start': {
      const id = text(body, 'id');
      if (!id || current.items.some(isMessage(id))) return current;
      return {
        ...current,
        items: [...current.items, {
          kind: 'message',
          id,
          role: text(body, 'role') || 'assistant',
          text: '',
          streaming: true,
        }],
      };
    }
    case 'message_delta': {
      const id = text(body, 'id');
      const delta = text(body, 'text');
      if (!id || delta === '') return current;
      const replaced = replaceItem(current.items, isMessage(id), (item) => ({
        ...(item as ConversationMessage),
        kind: 'message',
        text: (item as ConversationMessage).text + delta,
      }));
      if (replaced) return { ...current, items: replaced };
      // A delta for a message we never saw start. Draw it rather than drop
      // it: losing the agent's words is worse than an unlabelled role.
      return {
        ...current,
        items: [...current.items, { kind: 'message', id, role: 'assistant', text: delta, streaming: true }],
      };
    }
    case 'message_end': {
      const id = text(body, 'id');
      if (!id) return current;
      // message_end carries the whole message, so it replaces the accumulated
      // deltas rather than appending to them. A coalescing window the host
      // never got to flush cannot leave the app one delta short of the truth.
      const settled = {
        kind: 'message' as const,
        id,
        role: text(body, 'role') || 'assistant',
        text: text(body, 'text'),
        streaming: false,
      };
      const replaced = replaceItem(current.items, isMessage(id), () => settled);
      return { ...current, items: replaced ?? [...current.items, settled] };
    }
    default:
      return current;
  }
}

export const useConversationsStore = create<ConversationsStore>((set) => ({
  conversations: {},

  applyEnvelope: (sessionId, seq, kind, body) => set((state) => {
    const current = state.conversations[sessionId] ?? emptyConversation;
    // The seq spine belongs to one host process. A revived session is a NEW
    // host, which mints its envelopes from 1 again — so every envelope of its
    // life reads as a duplicate of the dead host's and the dedup below would
    // drop the whole conversation. `session_ready` is where a spine begins: it
    // is the one kind exempt from the guard, and it resets the cursor so the
    // envelopes after it are read against the host that sent them.
    const spineReset = kind === 'session_ready';
    if (!spineReset && seq > 0 && seq <= current.lastSeq) return state;
    const base = spineReset ? { ...current, lastSeq: 0 } : current;
    const lastSeq = spineReset ? seq : Math.max(current.lastSeq, seq);
    const next = applyToConversation(base, seq, kind, body);
    if (next === base && lastSeq === current.lastSeq) return state;
    return {
      conversations: { ...state.conversations, [sessionId]: { ...next, lastSeq } },
    };
  }),

  // Asking for a page is a broadcast round trip with nothing correlating it but
  // the anchor, so the only local state it owns is "one is in flight". Every way
  // the answer can arrive — the page itself, a snapshot, a host that went away —
  // clears it, which is what keeps a spinner from outliving the thing it waits on.
  historyRequested: (sessionId) => set((state) => {
    const current = state.conversations[sessionId];
    if (!current || current.loadingHistory) return state;
    return {
      conversations: { ...state.conversations, [sessionId]: { ...current, loadingHistory: true } },
    };
  }),

  // A prompt's round trip to the host is long enough to press Enter twice in,
  // and the host answers a second prompt with a log line and nothing else — the
  // draft would be gone and the user would be told nothing. So the run opens
  // here, when the prompt is sent, and the composer is shut until the host says
  // the run is real. From that moment on the composer is open again and what it
  // sends is a steer. Every way a run can end clears both flags, including the
  // daemon's settle for a prompt that reached no host at all.
  promptSent: (sessionId) => set((state) => {
    const current = state.conversations[sessionId] ?? emptyConversation;
    if (!current.ready || current.running) return state;
    return {
      conversations: { ...state.conversations, [sessionId]: { ...current, running: true, awaitingRun: true } },
    };
  }),

  // A host that exited is answering nothing else. Shutting the composer here is
  // what keeps a user from typing into a session that cannot hear them: the
  // daemon would answer the prompt with a command error, which is a worse way
  // to find out. The run flags go with it — whatever was open died with the
  // host, and a revived one starts from `session_ready`.
  hostExited: (sessionId) => set((state) => {
    const current = state.conversations[sessionId];
    if (!current || (!current.ready && !current.running && !current.awaitingRun && !current.loadingHistory)) return state;
    return {
      conversations: {
        ...state.conversations,
        [sessionId]: { ...current, ready: false, running: false, awaitingRun: false, loadingHistory: false, queue: emptyQueue },
      },
    };
  }),

  clearConversation: (sessionId) => set((state) => {
    if (!(sessionId in state.conversations)) return state;
    const conversations = { ...state.conversations };
    delete conversations[sessionId];
    return { conversations };
  }),

  retainConversations: (liveSessionIds) => set((state) => {
    const live = new Set(liveSessionIds);
    const stale = Object.keys(state.conversations).filter((id) => !live.has(id));
    if (stale.length === 0) return state;
    const conversations = { ...state.conversations };
    for (const id of stale) delete conversations[id];
    return { conversations };
  }),
}));

export function selectConversation(sessionId: string) {
  return (state: ConversationsStore): ConversationState => state.conversations[sessionId] ?? emptyConversation;
}
