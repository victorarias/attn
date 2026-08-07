import { beforeEach, describe, expect, it } from 'vitest';
import { useConversationsStore, type ConversationToolCall } from './conversations';

const SESSION = 'sess-1';

function apply(kind: string, body: Record<string, unknown>, seq: number) {
  useConversationsStore.getState().applyEnvelope(SESSION, seq, kind, body);
}

function conversation() {
  return useConversationsStore.getState().conversations[SESSION];
}

/** The transcript's messages, without the discriminant the list carries. */
function messages(sessionId = SESSION) {
  const state = useConversationsStore.getState().conversations[sessionId];
  return state.items
    .filter((item) => item.kind === 'message')
    .map(({ id, role, text, streaming }) => ({ id, role, text, streaming }));
}

function tools(): ConversationToolCall[] {
  return conversation().items.filter((item) => item.kind === 'tool');
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
    expect(messages()).toEqual([
      { id: 'm1', role: 'assistant', text: 'Hello', streaming: true },
    ]);

    apply('message_end', { id: 'm1', role: 'assistant', text: 'Hello there' }, 6);
    apply('run_settled', {}, 7);

    expect(messages()).toEqual([
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

    expect(messages().map((message) => [message.role, message.text])).toEqual([
      ['user', 'ask'],
      ['assistant', 'answer'],
    ]);
  });

  it('draws a delta whose message_start never arrived', () => {
    apply('message_delta', { id: 'orphan', text: 'said anyway' }, 1);
    expect(messages()).toEqual([
      { id: 'orphan', role: 'assistant', text: 'said anyway', streaming: true },
    ]);
  });

  it('drops a replayed envelope rather than doubling its text', () => {
    apply('message_start', { id: 'm1', role: 'assistant' }, 1);
    apply('message_delta', { id: 'm1', text: 'once' }, 2);
    apply('message_delta', { id: 'm1', text: 'once' }, 2);
    expect(messages()[0].text).toBe('once');
  });

  it('closes a message the run ended under', () => {
    apply('message_start', { id: 'm1', role: 'assistant' }, 1);
    apply('message_delta', { id: 'm1', text: 'partial' }, 2);
    apply('run_settled', {}, 3);
    expect(messages()[0].streaming).toBe(false);
  });

  it('shows why a run ended badly and reopens the composer', () => {
    apply('session_ready', {}, 1);
    apply('run_started', {}, 2);
    apply('run_settled', { error: 'No API key found for openai.' }, 3);

    expect(conversation().running).toBe(false);
    expect(messages()).toEqual([
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

  it('empties the queue on pi\'s own word, which is what a cancel produces', () => {
    apply('session_ready', {}, 1);
    apply('run_started', {}, 2);
    apply('queue_update', { steering: ['cut in'], followUp: ['and then'] }, 3);
    // What the host emits after clearQueue(): the strip empties because pi says
    // its queues are empty, never because the app removed an entry itself.
    apply('queue_update', { steering: [], followUp: [] }, 4);

    expect(conversation().queue).toEqual({ steering: [], followUp: [] });
    expect(conversation().running).toBe(true);
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
    expect(messages()).toHaveLength(1);
    // The unknown envelope still advances the cursor: it happened.
    expect(conversation().lastSeq).toBe(2);
  });

  it('keeps each session to its own conversation', () => {
    apply('message_start', { id: 'm1', role: 'assistant' }, 1);
    useConversationsStore.getState().applyEnvelope('sess-2', 1, 'message_start', { id: 'm1', role: 'assistant' });
    useConversationsStore.getState().applyEnvelope('sess-2', 2, 'message_delta', { id: 'm1', text: 'other' });

    expect(messages()[0].text).toBe('');
    expect(messages('sess-2')[0].text).toBe('other');
  });

  it('drops the conversations whose sessions are gone', () => {
    apply('session_ready', {}, 1);
    useConversationsStore.getState().applyEnvelope('sess-2', 1, 'session_ready', {});
    useConversationsStore.getState().retainConversations(['sess-2']);

    expect(useConversationsStore.getState().conversations[SESSION]).toBeUndefined();
    expect(useConversationsStore.getState().conversations['sess-2']).toBeDefined();
  });

  describe('tool calls', () => {
    it('shows a call as running before it reports, then as done', () => {
      apply('tool_started', { call_id: 'c1', name: 'bash', summary: 'ls -la', files: [] }, 1);
      expect(tools()).toEqual([{
        kind: 'tool',
        callId: 'c1',
        name: 'bash',
        summary: 'ls -la',
        files: [],
        status: 'running',
        hasDetail: false,
        hasPatch: false,
        truncated: false,
        fullOutput: false,
      }]);

      apply('tool_finished', {
        call_id: 'c1',
        name: 'bash',
        status: 'ok',
        summary: 'ls -la',
        files: [],
        detail: true,
        patch: false,
        truncated: true,
        full_output: true,
      }, 2);

      const [tool] = tools();
      expect(tool.status).toBe('ok');
      expect(tool.hasDetail).toBe(true);
      expect(tool.truncated).toBe(true);
      expect(tool.fullOutput).toBe(true);
      // One card, not two: the finish updates the call that started.
      expect(tools()).toHaveLength(1);
    });

    it('sits the card where the call happened, between what was said around it', () => {
      apply('message_end', { id: 'm1', role: 'assistant', text: 'let me look' }, 1);
      apply('tool_started', { call_id: 'c1', name: 'read', summary: 'main.go', files: ['main.go'] }, 2);
      apply('tool_finished', { call_id: 'c1', name: 'read', status: 'ok', summary: 'main.go', files: ['main.go'], detail: true, patch: false, truncated: false, full_output: false }, 3);
      apply('message_end', { id: 'm2', role: 'assistant', text: 'found it' }, 4);

      expect(conversation().items.map((item) => item.kind)).toEqual(['message', 'tool', 'message']);
    });

    it('carries the failure headline without opening the card', () => {
      apply('tool_started', { call_id: 'c1', name: 'bash', summary: 'make build', files: [] }, 1);
      apply('tool_finished', {
        call_id: 'c1',
        name: 'bash',
        status: 'error',
        summary: 'make build',
        files: [],
        detail: true,
        patch: false,
        truncated: false,
        full_output: false,
        error: 'exit status 2',
      }, 2);

      expect(tools()[0].status).toBe('error');
      expect(tools()[0].error).toBe('exit status 2');
    });

    it('draws a finish whose start never arrived', () => {
      apply('tool_finished', { call_id: 'c9', name: 'grep', status: 'ok', summary: 'TODO in .', files: [], detail: true, patch: false, truncated: false, full_output: false }, 1);
      expect(tools()).toHaveLength(1);
      expect(tools()[0].name).toBe('grep');
      expect(tools()[0].status).toBe('ok');
    });

    it('fills a card in when its detail arrives, addressed by call id', () => {
      apply('tool_started', { call_id: 'c1', name: 'read', summary: 'main.go', files: ['main.go'] }, 1);
      apply('tool_finished', { call_id: 'c1', name: 'read', status: 'ok', summary: 'main.go', files: ['main.go'], detail: true, patch: false, truncated: false, full_output: false }, 2);
      apply('tool_detail', { call_id: 'c1', text: 'package main', full: false, truncated: false }, 3);

      expect(tools()[0].detail).toEqual({ text: 'package main', full: false, truncated: false });
    });

    it('keeps a full answer when a clipped one lands after it', () => {
      apply('tool_finished', { call_id: 'c1', name: 'bash', status: 'ok', summary: 'yes | head -n 100000', files: [], detail: true, patch: false, truncated: true, full_output: true }, 1);
      apply('tool_detail', { call_id: 'c1', text: 'everything', full: true, truncated: false }, 2);
      // A second client's collapsed fetch answering late must not shrink the
      // card the user is reading.
      apply('tool_detail', { call_id: 'c1', text: 'clipped', full: false, truncated: true }, 3);

      expect(tools()[0].detail).toEqual({ text: 'everything', full: true, truncated: false });
    });

    it('carries a patch so the card can draw a diff', () => {
      apply('tool_finished', { call_id: 'c1', name: 'edit', status: 'ok', summary: 'main.go', files: ['main.go'], detail: true, patch: true, truncated: false, full_output: false }, 1);
      apply('tool_detail', { call_id: 'c1', text: '', patch: '--- a/main.go\n+++ b/main.go\n', full: false, truncated: false }, 2);

      expect(tools()[0].hasPatch).toBe(true);
      expect(tools()[0].detail?.patch).toBe('--- a/main.go\n+++ b/main.go\n');
    });

    it('says why the detail is missing rather than showing an empty card', () => {
      apply('tool_finished', { call_id: 'c1', name: 'bash', status: 'ok', summary: 'ls', files: [], detail: true, patch: false, truncated: false, full_output: false }, 1);
      apply('tool_detail', { call_id: 'c1', text: '', full: false, truncated: false, error: 'no detail held for call c1' }, 2);

      expect(tools()[0].detail?.error).toBe('no detail held for call c1');
    });

    it('closes a tool the run ended under', () => {
      apply('tool_started', { call_id: 'c1', name: 'bash', summary: 'sleep 600', files: [] }, 1);
      apply('run_settled', {}, 2);

      expect(tools()[0].status).toBe('error');
      expect(tools()[0].error).toBe('the run ended before this tool reported');
    });

    it('leaves a finished tool alone when the run settles', () => {
      apply('tool_started', { call_id: 'c1', name: 'read', summary: 'main.go', files: ['main.go'] }, 1);
      apply('tool_finished', { call_id: 'c1', name: 'read', status: 'ok', summary: 'main.go', files: ['main.go'], detail: true, patch: false, truncated: false, full_output: false }, 2);
      apply('run_settled', {}, 3);

      expect(tools()[0].status).toBe('ok');
      expect(tools()[0].error).toBeUndefined();
    });

    it('drops a replayed tool_started rather than doubling the card', () => {
      apply('tool_started', { call_id: 'c1', name: 'bash', summary: 'ls', files: [] }, 1);
      apply('tool_started', { call_id: 'c1', name: 'bash', summary: 'ls', files: [] }, 1);
      expect(tools()).toHaveLength(1);
    });
  });
});
