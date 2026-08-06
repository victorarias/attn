import { beforeEach, describe, expect, it } from 'vitest';
import { useConversationsStore } from './conversations';

const SESSION = 'sess-1';

function apply(kind: string, body: Record<string, unknown>, seq: number) {
  useConversationsStore.getState().applyEnvelope(SESSION, seq, kind, body);
}

function conversation() {
  return useConversationsStore.getState().conversations[SESSION];
}

describe('conversations store', () => {
  beforeEach(() => {
    useConversationsStore.setState({ conversations: {} });
  });

  it('builds a message from deltas and settles it on message_end', () => {
    apply('session_ready', { model: 'openai/gpt-5.6-luna' }, 1);
    apply('run_started', {}, 2);
    apply('message_start', { id: 'm1', role: 'assistant' }, 3);
    apply('message_delta', { id: 'm1', text: 'Hel' }, 4);
    apply('message_delta', { id: 'm1', text: 'lo' }, 5);
    expect(conversation().messages).toEqual([
      { id: 'm1', role: 'assistant', text: 'Hello', streaming: true },
    ]);

    apply('message_end', { id: 'm1', role: 'assistant', text: 'Hello there' }, 6);
    apply('run_settled', {}, 7);

    expect(conversation().messages).toEqual([
      { id: 'm1', role: 'assistant', text: 'Hello there', streaming: false },
    ]);
    expect(conversation().running).toBe(false);
    expect(conversation().ready).toBe(true);
  });

  it('opens the composer only between runs', () => {
    apply('session_ready', {}, 1);
    expect(conversation().running).toBe(false);
    apply('run_started', {}, 2);
    expect(conversation().running).toBe(true);
    apply('run_settled', {}, 3);
    expect(conversation().running).toBe(false);
  });

  it('keeps two messages of one run apart', () => {
    apply('message_start', { id: 'm1', role: 'user' }, 1);
    apply('message_end', { id: 'm1', role: 'user', text: 'ask' }, 2);
    apply('message_start', { id: 'm2', role: 'assistant' }, 3);
    apply('message_delta', { id: 'm2', text: 'answer' }, 4);

    expect(conversation().messages.map((message) => [message.role, message.text])).toEqual([
      ['user', 'ask'],
      ['assistant', 'answer'],
    ]);
  });

  it('draws a delta whose message_start never arrived', () => {
    apply('message_delta', { id: 'orphan', text: 'said anyway' }, 1);
    expect(conversation().messages).toEqual([
      { id: 'orphan', role: 'assistant', text: 'said anyway', streaming: true },
    ]);
  });

  it('drops a replayed envelope rather than doubling its text', () => {
    apply('message_start', { id: 'm1', role: 'assistant' }, 1);
    apply('message_delta', { id: 'm1', text: 'once' }, 2);
    apply('message_delta', { id: 'm1', text: 'once' }, 2);
    expect(conversation().messages[0].text).toBe('once');
  });

  it('closes a message the run ended under', () => {
    apply('message_start', { id: 'm1', role: 'assistant' }, 1);
    apply('message_delta', { id: 'm1', text: 'partial' }, 2);
    apply('run_settled', {}, 3);
    expect(conversation().messages[0].streaming).toBe(false);
  });

  it('shows why a run ended badly and reopens the composer', () => {
    apply('session_ready', {}, 1);
    apply('run_started', {}, 2);
    apply('run_settled', { error: 'No API key found for openai.' }, 3);

    expect(conversation().running).toBe(false);
    expect(conversation().messages).toEqual([
      { id: 'error-3', role: 'error', text: 'No API key found for openai.', streaming: false },
    ]);
  });

  it('carries pi\'s two queues verbatim, and empties them when a run ends', () => {
    apply('session_ready', {}, 1);
    apply('run_started', {}, 2);
    apply('queue_update', { steering: ['one'], followUp: ['two', 'three'] }, 3);

    expect(conversation().queue).toEqual({ steering: ['one'], followUp: ['two', 'three'] });

    // Nothing survives a settle: pi drains a follow-up inside the run rather
    // than after it, so a queue still standing here belongs to a host that has
    // stopped speaking.
    apply('run_settled', {}, 4);
    expect(conversation().queue).toEqual({ steering: [], followUp: [] });
  });

  it('shuts the composer only until the host acknowledges the run', () => {
    apply('session_ready', {}, 1);
    useConversationsStore.getState().promptSent(SESSION);

    expect(conversation().running).toBe(true);
    expect(conversation().awaitingRun).toBe(true);

    apply('run_started', {}, 2);
    expect(conversation().running).toBe(true);
    expect(conversation().awaitingRun).toBe(false);
  });

  it('reopens the composer when a prompt that reached no host is settled', () => {
    apply('session_ready', {}, 1);
    useConversationsStore.getState().promptSent(SESSION);
    apply('run_settled', { error: 'this conversation\'s agent is no longer running' }, 0);

    expect(conversation().running).toBe(false);
    expect(conversation().awaitingRun).toBe(false);
  });

  it('ignores a kind this build does not draw', () => {
    apply('message_start', { id: 'm1', role: 'assistant' }, 1);
    apply('tool_card', { id: 't1' }, 2);
    expect(conversation().messages).toHaveLength(1);
    // The unknown envelope still advances the cursor: it happened.
    expect(conversation().lastSeq).toBe(2);
  });

  it('keeps each session to its own conversation', () => {
    apply('message_start', { id: 'm1', role: 'assistant' }, 1);
    useConversationsStore.getState().applyEnvelope('sess-2', 1, 'message_start', { id: 'm1', role: 'assistant' });
    useConversationsStore.getState().applyEnvelope('sess-2', 2, 'message_delta', { id: 'm1', text: 'other' });

    expect(conversation().messages[0].text).toBe('');
    expect(useConversationsStore.getState().conversations['sess-2'].messages[0].text).toBe('other');
  });

  it('drops the conversations whose sessions are gone', () => {
    apply('session_ready', {}, 1);
    useConversationsStore.getState().applyEnvelope('sess-2', 1, 'session_ready', {});
    useConversationsStore.getState().retainConversations(['sess-2']);

    expect(useConversationsStore.getState().conversations[SESSION]).toBeUndefined();
    expect(useConversationsStore.getState().conversations['sess-2']).toBeDefined();
  });
});
