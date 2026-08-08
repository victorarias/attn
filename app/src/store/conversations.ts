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
  | ({ kind: 'tool' } & ConversationToolCall);

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
  running: false,
  awaitingRun: false,
  ready: false,
  queue: emptyQueue,
  lastSeq: 0,
};

interface ConversationsStore {
  conversations: Record<string, ConversationState>;
  applyEnvelope: (sessionId: string, seq: number, kind: string, body: Record<string, unknown>) => void;
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

function queueFrom(value: unknown): ConversationQueue {
  if (!value || typeof value !== 'object') return emptyQueue;
  const body = value as Record<string, unknown>;
  return { steering: stringList(body, 'steering'), followUp: stringList(body, 'followUp') };
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

function applyToConversation(
  current: ConversationState,
  seq: number,
  kind: string,
  body: Record<string, unknown>,
): ConversationState {
  switch (kind) {
    case 'session_ready':
      // A host that has just come up has no run open, whatever the dead one was
      // doing when it went. The transcript is left alone: the snapshot that
      // follows replaces it, and a host too old to send one leaves the user with
      // the history they were already reading rather than an empty pane.
      return { ...current, ready: true, running: false, awaitingRun: false, queue: emptyQueue };
    case 'conversation_snapshot': {
      const raw = body.items;
      if (!Array.isArray(raw)) return current;
      // Replace, never merge. The host's transcript is the authority — the same
      // contract the terminal's VT dump has — so two clients that attach at
      // different moments end up drawing the same conversation.
      const items = raw.map(snapshotItem).filter((item): item is ConversationItem => item !== null);
      return {
        ...current,
        items,
        ready: true,
        running: flag(body, 'running'),
        awaitingRun: false,
        queue: queueFrom(body.queue),
      };
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
        if (item.status !== 'running') return item;
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
    if (!current || (!current.ready && !current.running && !current.awaitingRun)) return state;
    return {
      conversations: {
        ...state.conversations,
        [sessionId]: { ...current, ready: false, running: false, awaitingRun: false, queue: emptyQueue },
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
