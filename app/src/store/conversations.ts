import { create } from 'zustand';

// The app's picture of a conversation session — the ones whose agent runs in a
// headless host instead of a PTY. It is built entirely from the host's envelope
// stream, which is why this store is append-only and never asks the daemon
// anything: the stream is the conversation.
//
// Two kinds of envelope land here. Semantic ones (session_ready, run_started,
// run_settled) say where the session is; render ones (message_start,
// message_delta, message_end) say what it said. An unknown kind is ignored on
// purpose — the host is pinned to a pi version the app is not, and a kind this
// build does not draw must not break the ones it does.

export interface ConversationMessage {
  id: string;
  role: string;
  text: string;
  // True between message_start and message_end: the text is still arriving.
  streaming: boolean;
}

export interface ConversationState {
  messages: ConversationMessage[];
  // A run is open: the agent is working and the composer is closed.
  running: boolean;
  // The host has its pi session up and will accept prompts.
  ready: boolean;
  // The highest envelope seq applied. Envelopes are minted in order by one
  // host, so anything at or below this is a duplicate to drop.
  lastSeq: number;
}

const emptyConversation: ConversationState = { messages: [], running: false, ready: false, lastSeq: 0 };

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
      return { ...current, running: true };
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
      return { ...current, running: false, messages };
    }
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
  // and the host answers a second prompt mid-run with a log line and nothing
  // else — the draft would be gone and the user would be told nothing. So the
  // run opens here, when the prompt is sent, and the composer is shut for the
  // whole of it. run_started then finds it already open and changes nothing;
  // every way a run can end reopens it, including the daemon's settle for a
  // prompt that reached no host at all.
  promptSent: (sessionId) => set((state) => {
    const current = state.conversations[sessionId] ?? emptyConversation;
    if (!current.ready || current.running) return state;
    return { conversations: { ...state.conversations, [sessionId]: { ...current, running: true } } };
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
