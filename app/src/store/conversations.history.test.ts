import { beforeEach, describe, expect, it } from 'vitest';
import { useConversationsStore, selectConversation, conversationItemKey } from './conversations';

/**
 * Slice 5: what the app does with a conversation longer than one window.
 *
 * A snapshot is broadcast and REPLACES — that is the bargain the host's
 * transcript makes with every client. What changes here is that a snapshot no
 * longer carries the whole conversation, so replacing blindly would mean any
 * window attaching shortens what every other window is showing. The epoch is
 * what makes the difference decidable, and most of this file is about that.
 */

const SESSION = 'sess-history';

function apply(kind: string, body: Record<string, unknown>, seq: number) {
  useConversationsStore.getState().applyEnvelope(SESSION, seq, kind, body);
}

function read() {
  return selectConversation(SESSION)(useConversationsStore.getState());
}

function message(id: string, text = id) {
  return { kind: 'message', id, role: 'assistant', text, streaming: false };
}

function keys() {
  return read().items.map(conversationItemKey);
}

describe('conversation snapshots that are only a window', () => {
  beforeEach(() => {
    useConversationsStore.setState({ conversations: {} });
  });

  it('takes the window and remembers there is more behind it', () => {
    apply('conversation_snapshot', {
      epoch: 'e1',
      items: [message('m8'), message('m9')],
      total: 9,
      truncated: true,
      has_more: true,
      running: false,
      queue: { steering: [], followUp: [] },
    }, 1);

    expect(keys()).toEqual(['message:m8', 'message:m9']);
    expect(read().epoch).toBe('e1');
    expect(read().hasMoreBefore).toBe(true);
  });

  it('splices a later snapshot from the same host onto scroll-back already paged in', () => {
    apply('conversation_snapshot', {
      epoch: 'e1', items: [message('m3'), message('m4')], has_more: true, running: false,
    }, 1);
    apply('conversation_page', {
      epoch: 'e1', before: 'message:m3', items: [message('m1'), message('m2')], has_more: false,
    }, 2);
    expect(keys()).toEqual(['message:m1', 'message:m2', 'message:m3', 'message:m4']);

    // Another window attaches. The host answers with its newest window, which
    // starts at m3 — everything this client paged in is still valid.
    apply('conversation_snapshot', {
      epoch: 'e1', items: [message('m3'), message('m4'), message('m5')], has_more: true, running: false,
    }, 3);

    expect(keys()).toEqual(['message:m1', 'message:m2', 'message:m3', 'message:m4', 'message:m5']);
    // The client reached the start of the conversation by paging; the
    // snapshot's own has_more is about the window's start, not the transcript's.
    expect(read().hasMoreBefore).toBe(false);
  });

  it('replaces when the transcript was rebuilt by another host', () => {
    apply('conversation_snapshot', { epoch: 'e1', items: [message('m1'), message('m2')], has_more: false, running: false }, 1);
    // A revived host read pi's session file and minted its own ids: nothing it
    // sends can be spliced onto what came from the dead one.
    apply('session_ready', { state: 'idle' }, 1);
    apply('conversation_snapshot', { epoch: 'e2', items: [message('h1')], has_more: true, running: false }, 2);

    expect(keys()).toEqual(['message:h1']);
    expect(read().epoch).toBe('e2');
    expect(read().hasMoreBefore).toBe(true);
  });

  it('replaces when the window starts past everything this client holds', () => {
    apply('conversation_snapshot', { epoch: 'e1', items: [message('m1')], has_more: false, running: false }, 1);
    apply('conversation_snapshot', { epoch: 'e1', items: [message('m4'), message('m5')], has_more: true, running: false }, 2);

    expect(keys()).toEqual(['message:m4', 'message:m5']);
    expect(read().hasMoreBefore).toBe(true);
  });
});

describe('paging older history in', () => {
  beforeEach(() => {
    useConversationsStore.setState({ conversations: {} });
    apply('conversation_snapshot', {
      epoch: 'e1', items: [message('m5'), message('m6')], has_more: true, running: false,
    }, 1);
  });

  it('prepends a page whose anchor is the oldest item held', () => {
    useConversationsStore.getState().historyRequested(SESSION);
    expect(read().loadingHistory).toBe(true);

    apply('conversation_page', {
      epoch: 'e1', before: 'message:m5', items: [message('m3'), message('m4')], has_more: true,
    }, 2);

    expect(keys()).toEqual(['message:m3', 'message:m4', 'message:m5', 'message:m6']);
    expect(read().hasMoreBefore).toBe(true);
    expect(read().loadingHistory).toBe(false);
  });

  // Pages are broadcast, so a window scrolled somewhere else sees every page
  // anyone asked for. Taking one would put a gap in this transcript.
  it('ignores a page anchored anywhere but this client oldest item', () => {
    apply('conversation_page', {
      epoch: 'e1', before: 'message:m6', items: [message('m4')], has_more: true,
    }, 2);
    expect(keys()).toEqual(['message:m5', 'message:m6']);
  });

  it('ignores a page from a host that is no longer the one drawn', () => {
    apply('conversation_page', {
      epoch: 'e0', before: 'message:m5', items: [message('m4')], has_more: true,
    }, 2);
    expect(keys()).toEqual(['message:m5', 'message:m6']);
  });

  it('drops items it already holds rather than drawing them twice', () => {
    apply('conversation_page', {
      epoch: 'e1', before: 'message:m5', items: [message('m4'), message('m5')], has_more: false,
    }, 2);
    expect(keys()).toEqual(['message:m4', 'message:m5', 'message:m6']);
  });

  it('says the start is reached, which is what stops the asking', () => {
    apply('conversation_page', {
      epoch: 'e1', before: 'message:m5', items: [message('m4')], has_more: false,
    }, 2);
    expect(read().hasMoreBefore).toBe(false);
  });

  // A host that died under a page request leaves a spinner nothing will ever
  // answer.
  it('stops waiting for a page when the host goes away', () => {
    useConversationsStore.getState().historyRequested(SESSION);
    useConversationsStore.getState().hostExited(SESSION);
    expect(read().loadingHistory).toBe(false);
  });

  it('keeps paged-in scroll-back reachable across a tool card anchor', () => {
    apply('tool_started', { call_id: 'c9', name: 'bash', summary: 'ls', files: [] }, 2);
    apply('conversation_page', {
      epoch: 'e1', before: 'message:m5', items: [message('m4')], has_more: false,
    }, 3);
    expect(keys()).toEqual(['message:m4', 'message:m5', 'message:m6', 'tool:c9']);
  });
});

describe('notices and the model', () => {
  beforeEach(() => {
    useConversationsStore.setState({ conversations: {} });
  });

  it('draws a compaction as one row that settles in place', () => {
    apply('session_ready', { state: 'idle' }, 1);
    apply('notice', { id: 'n1', level: 'info', text: 'Compacting...', done: false }, 2);
    expect(read().items).toEqual([{ kind: 'notice', id: 'n1', level: 'info', text: 'Compacting...', done: false }]);

    apply('notice', { id: 'n1', level: 'info', text: 'Compacted', done: true }, 3);
    expect(read().items).toEqual([{ kind: 'notice', id: 'n1', level: 'info', text: 'Compacted', done: true }]);
  });

  it('leaves an unsettled notice alone when the run ends', () => {
    apply('session_ready', { state: 'idle' }, 1);
    apply('notice', { id: 'n1', level: 'warn', text: 'Retrying 1/5', done: false }, 2);
    apply('run_settled', { state: 'idle' }, 3);
    expect(read().items[0]).toMatchObject({ kind: 'notice', done: false });
  });

  it('reads a level it does not know as plain information', () => {
    apply('notice', { id: 'n1', level: 'catastrophe', text: 'something', done: true }, 1);
    expect(read().items[0]).toMatchObject({ level: 'info' });
  });

  it('takes the model and the catalog from session_ready, and corrections from the host', () => {
    apply('session_ready', { state: 'idle', model: 'openai/gpt-5.6-luna', models: ['openai/gpt-5.6-luna', 'anthropic/claude'] }, 1);
    expect(read().model).toBe('openai/gpt-5.6-luna');
    expect(read().models).toEqual(['openai/gpt-5.6-luna', 'anthropic/claude']);

    apply('model_changed', { model: 'anthropic/claude' }, 2);
    expect(read().model).toBe('anthropic/claude');
  });

  // A refusal reaches the app as the model still in force; a host that answered
  // with nothing must not blank the picker.
  it('keeps the model it has when a change carries none', () => {
    apply('session_ready', { state: 'idle', model: 'openai/gpt-5.6-luna', models: ['openai/gpt-5.6-luna'] }, 1);
    apply('model_changed', { error: 'no credentials for anthropic' }, 2);
    expect(read().model).toBe('openai/gpt-5.6-luna');
  });

  it('keeps the catalog a revived host did not manage to read', () => {
    apply('session_ready', { state: 'idle', model: 'a/b', models: ['a/b', 'c/d'] }, 1);
    apply('session_ready', { state: 'idle', model: 'a/b' }, 1);
    expect(read().models).toEqual(['a/b', 'c/d']);
  });
});
