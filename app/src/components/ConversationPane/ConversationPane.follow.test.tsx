import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { ConversationPane } from './index';
import { DaemonApiProvider, type DaemonApi } from '../../contexts/DaemonApiContext';
import { useConversationsStore } from '../../store/conversations';

/**
 * Following the live end of a stream, and the two ways a reader leaves it.
 */

const SESSION = 'sess-follow';

function renderPane() {
  const api = {
    sendAgentPrompt: vi.fn(),
    sendAgentToolDetail: vi.fn(),
    sendAgentClearQueue: vi.fn(),
    sendAgentAttach: vi.fn(),
    sendAgentHistory: vi.fn(),
    sendAgentSetModel: vi.fn(),
  } as unknown as DaemonApi;
  render(
    <DaemonApiProvider api={api}>
      <ConversationPane sessionId={SESSION} paneActive sessionState="working" />
    </DaemonApiProvider>,
  );
}

/** Gives a jsdom element the geometry a scrollable list would have. */
function sizeList(list: HTMLElement, size: { scrollHeight: number; clientHeight: number }) {
  Object.defineProperty(list, 'scrollHeight', { value: size.scrollHeight, configurable: true });
  Object.defineProperty(list, 'clientHeight', { value: size.clientHeight, configurable: true });
}

function apply(kind: string, body: Record<string, unknown>, seq: number) {
  act(() => {
    useConversationsStore.getState().applyEnvelope(SESSION, seq, kind, body);
  });
}

function snapshot(text: string, seq: number) {
  apply('conversation_snapshot', {
    epoch: 'e1',
    items: [{ kind: 'message', id: 'm1', role: 'assistant', text, streaming: true }],
    has_more: false,
    running: true,
    queue: { steering: [], followUp: [] },
  }, seq);
}

describe('ConversationPane follow mode', () => {
  beforeEach(() => {
    useConversationsStore.setState({ conversations: {} });
  });

  it('opens on the newest message and keeps following it', () => {
    renderPane();
    apply('session_ready', { state: 'working' }, 1);
    const list = screen.getByTestId('conversation-messages');
    sizeList(list, { scrollHeight: 600, clientHeight: 100 });
    snapshot('first', 2);
    expect(list.scrollTop).toBe(600);

    sizeList(list, { scrollHeight: 900, clientHeight: 100 });
    apply('message_delta', { id: 'm1', text: ' and more' }, 3);
    expect(list.scrollTop).toBe(900);
  });

  /**
   * followingRef starts true because that is what opening a conversation wants.
   * A reader restored mid-transcript arrives already scrolled, and no scroll
   * event has fired yet — so an assumed `true` would yank them to the bottom on
   * the very first delta.
   */
  it('leaves a reader restored mid-transcript where they were', () => {
    renderPane();
    apply('session_ready', { state: 'working' }, 1);
    const list = screen.getByTestId('conversation-messages');
    sizeList(list, { scrollHeight: 600, clientHeight: 100 });
    list.scrollTop = 200;
    snapshot('first', 2);
    expect(list.scrollTop).toBe(200);

    sizeList(list, { scrollHeight: 900, clientHeight: 100 });
    apply('message_delta', { id: 'm1', text: ' and more' }, 3);
    expect(list.scrollTop).toBe(200);
  });

  it('picks following back up when the reader returns to the bottom', () => {
    renderPane();
    apply('session_ready', { state: 'working' }, 1);
    const list = screen.getByTestId('conversation-messages');
    sizeList(list, { scrollHeight: 600, clientHeight: 100 });
    list.scrollTop = 200;
    snapshot('first', 2);

    list.scrollTop = 500;
    fireEvent.scroll(list);
    sizeList(list, { scrollHeight: 900, clientHeight: 100 });
    apply('message_delta', { id: 'm1', text: ' and more' }, 3);
    expect(list.scrollTop).toBe(900);
  });
});
