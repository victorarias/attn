import { create } from 'zustand';

// The app's picture of a conversation session — the ones whose agent runs in a
// headless host instead of a PTY. It is built entirely from the host's envelope
// stream, which is why this store is append-only and never asks the daemon
// anything: the stream is the conversation.
//
// Two kinds of envelope land here. Semantic ones (session_ready, run_started,
// run_settled) say where the session is; render ones (message_start,
// message_delta, message_end, queue_update) say what it said and what it has
// not read yet. An unknown kind is ignored on purpose — the host is pinned to a
// pi version the app is not, and a kind this build does not draw must not break
// the ones it does.

export interface ConversationMessage {
  id: string;
  role: string;
  text: string;
  // True between message_start and message_end: the text is still arriving.
  streaming: boolean;
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
 */
export interface ConversationQueue {
  steering: string[];
  followUp: string[];
}

export interface ConversationState {
  messages: ConversationMessage[];
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
  messages: [],
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

function stringList(body: Record<string, unknown>, key: string): string[] {
  const value = body[key];
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === 'string') : [];
}

function applyToConversation(
  current: ConversationState,
  seq: number,
  kind: string,
  body: Record<string, unknown>,
): ConversationState {
  switch (kind) {
    case 'session_ready':
      return { ...current, ready: true };
    case 'run_started':
      return { ...current, running: true, awaitingRun: false };
    case 'run_settled': {
      // Whatever was still streaming when the run closed is finished: the host
      // emits message_end before run_settled, so a message left open here means
      // the run ended under it and it will never grow again.
      const messages = current.messages.map((message) => (
        message.streaming ? { ...message, streaming: false } : message
      ));
      // A run that ended badly says so in the transcript. Otherwise a prompt
      // that failed outright — an unauthenticated provider, a model that
      // refused — reads as an agent that answered with silence.
      const failure = text(body, 'error');
      if (failure !== '') {
        messages.push({ id: `error-${seq}`, role: 'error', text: failure, streaming: false });
      }
      // The queue empties with the run. pi drains a follow-up by starting the
      // next run rather than by ending this one, so anything still shown here
      // when a run closes is a queue this host will never speak about again —
      // most often because the host died under it.
      return { ...current, running: false, awaitingRun: false, messages, queue: emptyQueue };
    }
    case 'queue_update':
      return { ...current, queue: { steering: stringList(body, 'steering'), followUp: stringList(body, 'followUp') } };
    case 'message_start': {
      const id = text(body, 'id');
      if (!id || current.messages.some((message) => message.id === id)) return current;
      return {
        ...current,
        messages: [...current.messages, { id, role: text(body, 'role') || 'assistant', text: '', streaming: true }],
      };
    }
    case 'message_delta': {
      const id = text(body, 'id');
      const delta = text(body, 'text');
      if (!id || delta === '') return current;
      const index = current.messages.findIndex((message) => message.id === id);
      if (index < 0) {
        // A delta for a message we never saw start. Draw it rather than drop
        // it: losing the agent's words is worse than an unlabelled role.
        return { ...current, messages: [...current.messages, { id, role: 'assistant', text: delta, streaming: true }] };
      }
      const messages = current.messages.slice();
      messages[index] = { ...messages[index], text: messages[index].text + delta };
      return { ...current, messages };
    }
    case 'message_end': {
      const id = text(body, 'id');
      if (!id) return current;
      const index = current.messages.findIndex((message) => message.id === id);
      // message_end carries the whole message, so it replaces the accumulated
      // deltas rather than appending to them. A coalescing window the host
      // never got to flush cannot leave the app one delta short of the truth.
      const settled = { id, role: text(body, 'role') || 'assistant', text: text(body, 'text'), streaming: false };
      if (index < 0) return { ...current, messages: [...current.messages, settled] };
      const messages = current.messages.slice();
      messages[index] = { ...messages[index], ...settled };
      return { ...current, messages };
    }
    default:
      return current;
  }
}

export const useConversationsStore = create<ConversationsStore>((set) => ({
  conversations: {},

  applyEnvelope: (sessionId, seq, kind, body) => set((state) => {
    const current = state.conversations[sessionId] ?? emptyConversation;
    if (seq > 0 && seq <= current.lastSeq) return state;
    const next = applyToConversation(current, seq, kind, body);
    if (next === current && seq <= current.lastSeq) return state;
    return {
      conversations: { ...state.conversations, [sessionId]: { ...next, lastSeq: Math.max(current.lastSeq, seq) } },
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
