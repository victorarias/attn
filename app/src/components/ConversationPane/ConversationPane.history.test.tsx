import { StrictMode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { ConversationPane } from './index';
import { DaemonApiProvider, type DaemonApi } from '../../contexts/DaemonApiContext';
import { useConversationsStore } from '../../store/conversations';

/**
 * Slice 5 in the pane: the way back through a long conversation, the rows that
 * explain a silence, and the model picker.
 */

const SESSION = 'sess-1';

function renderPane({ strict = false } = {}) {
  const sendAgentHistory = vi.fn();
  const sendAgentSetModel = vi.fn();
  const api = {
    sendAgentPrompt: vi.fn(),
    sendAgentToolDetail: vi.fn(),
    sendAgentClearQueue: vi.fn(),
    sendAgentAttach: vi.fn(),
    sendAgentHistory,
    sendAgentSetModel,
  } as unknown as DaemonApi;
  const pane = (
    <DaemonApiProvider api={api}>
      <ConversationPane sessionId={SESSION} paneActive sessionState="idle" />
    </DaemonApiProvider>
  );
  render(strict ? <StrictMode>{pane}</StrictMode> : pane);
  return { sendAgentHistory, sendAgentSetModel };
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

function windowed(hasMore = true, seq = 2) {
  apply('conversation_snapshot', {
    epoch: 'e1',
    items: [
      { kind: 'message', id: 'm9', role: 'assistant', text: 'nine', streaming: false },
      { kind: 'message', id: 'm10', role: 'assistant', text: 'ten', streaming: false },
    ],
    has_more: hasMore,
    running: false,
    queue: { steering: [], followUp: [] },
  }, seq);
}

describe('ConversationPane history', () => {
  beforeEach(() => {
    useConversationsStore.setState({ conversations: {} });
  });

  it('offers a way back only while the host holds more', () => {
    renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    windowed(false);
    expect(screen.queryByTestId('conversation-load-earlier')).toBeNull();

    windowed(true, 3);
    expect(screen.getByTestId('conversation-load-earlier')).toBeInTheDocument();
  });

  it('asks for the page before the oldest item it is showing', () => {
    const { sendAgentHistory } = renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    windowed();

    fireEvent.click(screen.getByTestId('conversation-load-earlier'));
    expect(sendAgentHistory).toHaveBeenCalledWith(SESSION, 'message:m9');
  });

  // Paging puts content ABOVE what the reader is looking at, and the browser
  // keeps scrollTop — so without the anchor the page they were reading slides
  // down by however much arrived. The anchor is a layout effect, which React
  // double-invokes on a StrictMode mount, so it has to be idempotent too: an
  // anchor applied twice shifts the view by twice the page.
  it('holds the reader in place when older messages arrive above them', () => {
    renderPane({ strict: true });
    apply('session_ready', { state: 'idle' }, 1);
    windowed();

    const list = screen.getByTestId('conversation-messages');
    // jsdom lays nothing out, so the list is given a size to scroll within: a
    // 600px transcript in a 100px pane, read from the top.
    sizeList(list, { scrollHeight: 600, clientHeight: 100 });
    fireEvent.scroll(list);
    expect(list.scrollTop).toBe(0);

    // A page lands and the transcript grows upwards by 400px.
    sizeList(list, { scrollHeight: 1000, clientHeight: 100 });
    apply('conversation_page', {
      epoch: 'e1',
      before: 'message:m9',
      items: [{ kind: 'message', id: 'm8', role: 'assistant', text: 'eight', streaming: false }],
      has_more: false,
    }, 3);

    // 600 below the caret before, 600 below it after: the same words are on
    // screen, with the new ones above them.
    expect(list.scrollHeight - list.scrollTop).toBe(600);
  });

  it('does not ask again while a page is still in flight', () => {
    const { sendAgentHistory } = renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    windowed();

    fireEvent.click(screen.getByTestId('conversation-load-earlier'));
    fireEvent.click(screen.getByTestId('conversation-load-earlier'));
    expect(sendAgentHistory).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('conversation-load-earlier')).toBeDisabled();
  });

  it('draws the page it gets back above what was already there', () => {
    renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    windowed();
    fireEvent.click(screen.getByTestId('conversation-load-earlier'));

    apply('conversation_page', {
      epoch: 'e1',
      before: 'message:m9',
      items: [{ kind: 'message', id: 'm8', role: 'assistant', text: 'eight', streaming: false }],
      has_more: false,
    }, 3);

    const drawn = screen.getAllByTestId(/^conversation-message-/).map((node) => node.getAttribute('data-testid'));
    expect(drawn).toEqual(['conversation-message-m8', 'conversation-message-m9', 'conversation-message-m10']);
    expect(screen.queryByTestId('conversation-load-earlier')).toBeNull();
  });

  it('draws a notice as its own row, and says whether it is still happening', () => {
    renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    apply('notice', { id: 'n1', level: 'info', text: 'Compacting the conversation...', done: false }, 2);

    const row = screen.getByTestId('conversation-notice-n1');
    expect(row).toHaveTextContent('Compacting the conversation...');
    expect(row).toHaveAttribute('data-done', 'false');

    apply('notice', { id: 'n1', level: 'warn', text: 'Compaction was cancelled', done: true }, 3);
    const settled = screen.getByTestId('conversation-notice-n1');
    expect(settled).toHaveTextContent('Compaction was cancelled');
    expect(settled).toHaveAttribute('data-done', 'true');
    expect(settled).toHaveAttribute('data-level', 'warn');
    expect(screen.getAllByTestId(/^conversation-notice-/)).toHaveLength(1);
  });

  it('shows no model picker until the host says what is available', () => {
    renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    expect(screen.queryByTestId('conversation-model')).toBeNull();
  });

  it('switches the model, and shows the host answer rather than the click', () => {
    const { sendAgentSetModel } = renderPane();
    apply('session_ready', { state: 'idle', model: 'openai/luna', models: ['openai/luna', 'anthropic/claude'] }, 1);

    const picker = screen.getByTestId('conversation-model');
    expect(picker).toHaveValue('openai/luna');

    fireEvent.change(picker, { target: { value: 'anthropic/claude' } });
    expect(sendAgentSetModel).toHaveBeenCalledWith(SESSION, 'anthropic/claude');
    // pi refused: the host answers with the model still in force, and the
    // picker goes back to it.
    apply('model_changed', { model: 'openai/luna', error: 'no credentials' }, 2);
    expect(screen.getByTestId('conversation-model')).toHaveValue('openai/luna');
  });

  it('keeps a model outside the catalog visible instead of showing a wrong one', () => {
    renderPane();
    apply('session_ready', { state: 'idle', model: 'local/experimental', models: ['openai/luna'] }, 1);
    expect(screen.getByTestId('conversation-model')).toHaveTextContent('local/experimental');
  });
});
