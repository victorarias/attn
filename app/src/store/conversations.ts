import { create } from 'zustand';

// Conversation sessions — the ones whose agent runs in a headless host instead
// of a PTY — built entirely from the host's envelope stream. An unknown kind is
// ignored on purpose: the host is pinned to a pi version the app is not.

export interface ConversationMessage {
  id: string;
  role: string;
  text: string;
  streaming: boolean;
}

export interface ConversationState {
  messages: ConversationMessage[];
  running: boolean;
  ready: boolean;
  // Highest seq applied; one host mints in order, so anything below is a dup.
  lastSeq: number;
}

const emptyConversation: ConversationState = { messages: [], running: false, ready: false, lastSeq: 0 };

interface ConversationsStore {
  conversations: Record<string, ConversationState>;
  applyEnvelope: (sessionId: string, seq: number, kind: string, body: Record<string, unknown>) => void;
  promptSent: (sessionId: string) => void;
  clearConversation: (sessionId: string) => void;
  // A transcript outlives its host on purpose, so the sessions list — not the
  // host's exit — is what bounds this store.
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
      // message_end precedes run_settled, so anything still open ended under the
      // run and will never grow again.
      const messages = current.messages.map((message) => (
        message.streaming ? { ...message, streaming: false } : message
      ));
      // Without this, a prompt that failed outright reads as agent silence.
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
        // Losing the agent's words is worse than an unlabelled role.
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
      // message_end carries the whole message and replaces the accumulated
      // deltas, so an unflushed coalescing window cannot lose one.
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

  // The run opens at send time, not on run_started, because the round trip is
  // long enough to press Enter twice in and the host drops a mid-run prompt
  // silently. Every way a run can end reopens the composer, including the
  // daemon's settle for a prompt that reached no host.
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
