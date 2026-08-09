import { StrictMode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConversationPane } from './index';
import { DaemonApiProvider, type DaemonApi } from '../../contexts/DaemonApiContext';
import { useConversationsStore } from '../../store/conversations';
import { useSessionStore } from '../../store/sessions';
import type { UISessionState } from '../../types/sessionState';

// The pane's two jobs when a conversation is not simply running in front of it:
// ask for a picture it does not have, and offer the way back when the host is
// gone.

const SESSION = 'sess-1';

function renderPane(options: {
  sessionState?: UISessionState;
  sendAgentAttach?: ReturnType<typeof vi.fn>;
  strict?: boolean;
} = {}) {
  const sendAgentAttach = options.sendAgentAttach ?? vi.fn();
  const api = { sendAgentPrompt: vi.fn(), sendAgentClearQueue: vi.fn(), sendAgentAttach } as unknown as DaemonApi;
  const tree = (
    <DaemonApiProvider api={api}>
      <ConversationPane sessionId={SESSION} paneActive sessionState={options.sessionState} />
    </DaemonApiProvider>
  );
  render(options.strict ? <StrictMode>{tree}</StrictMode> : tree);
  return sendAgentAttach;
}

function apply(kind: string, body: Record<string, unknown>, seq: number) {
  act(() => {
    useConversationsStore.getState().applyEnvelope(SESSION, seq, kind, body);
  });
}

describe('ConversationPane: attaching', () => {
  beforeEach(() => {
    useConversationsStore.setState({ conversations: {} });
  });

  it('asks a live host for a snapshot when it has never seen the stream', () => {
    const sendAgentAttach = renderPane({ sessionState: 'idle' });
    expect(sendAgentAttach).toHaveBeenCalledWith(SESSION);
  });

  it('asks for nothing when it is already watching the stream', () => {
    apply('session_ready', {}, 1);
    const sendAgentAttach = renderPane({ sessionState: 'idle' });
    expect(sendAgentAttach).not.toHaveBeenCalled();
  });

  // A launching session is about to volunteer its own session_ready and
  // snapshot, and a recoverable one has no host to answer. Asking either would
  // put a command error on the socket describing a race, not a fault.
  it.each<UISessionState>(['launching', 'recoverable', 'unknown'])(
    'asks nothing of a %s session',
    (sessionState) => {
      const sendAgentAttach = renderPane({ sessionState });
      expect(sendAgentAttach).not.toHaveBeenCalled();
    },
  );

  // StrictMode double-invokes effects. One mount must still be one request:
  // the ask is idempotent, but a pane that fires it twice per open would do so
  // for every window of every conversation.
  it('asks once under a replaying mount', () => {
    const sendAgentAttach = renderPane({ sessionState: 'idle', strict: true });
    expect(sendAgentAttach).toHaveBeenCalledTimes(1);
  });
});

describe('ConversationPane: recoverable', () => {
  beforeEach(() => {
    useConversationsStore.setState({ conversations: {} });
    useSessionStore.setState({ sessions: [] });
  });

  it('offers the way back when the host is gone', async () => {
    const reloadSession = vi.fn().mockResolvedValue(undefined);
    useSessionStore.setState({ reloadSession });
    renderPane({ sessionState: 'recoverable' });

    expect(screen.getByTestId('conversation-recoverable')).toBeInTheDocument();
    expect(screen.getByTestId('conversation-input')).toBeDisabled();

    fireEvent.click(screen.getByTestId('conversation-reload'));
    expect(reloadSession).toHaveBeenCalledWith(SESSION);
    await waitFor(() => expect(screen.getByTestId('conversation-reload')).not.toBeDisabled());
  });

  it('says why the reload did not happen instead of failing silently', async () => {
    useSessionStore.setState({ reloadSession: vi.fn().mockRejectedValue(new Error('no stored launch intent')) });
    renderPane({ sessionState: 'recoverable' });

    fireEvent.click(screen.getByTestId('conversation-reload'));
    await waitFor(() => {
      expect(screen.getByTestId('conversation-recoverable')).toHaveTextContent('no stored launch intent');
    });
  });

  it('drops the banner once the replacement host announces itself', () => {
    const { rerender } = renderPaneForRerender();
    expect(screen.getByTestId('conversation-recoverable')).toBeInTheDocument();

    rerender('idle');
    apply('session_ready', {}, 1);
    expect(screen.queryByTestId('conversation-recoverable')).not.toBeInTheDocument();
    expect(screen.getByTestId('conversation-input')).not.toBeDisabled();
  });
});

/** A pane whose session state can move, the way the daemon moves it. */
function renderPaneForRerender() {
  const api = {
    sendAgentPrompt: vi.fn(),
    sendAgentClearQueue: vi.fn(),
    sendAgentAttach: vi.fn(),
  } as unknown as DaemonApi;
  const tree = (sessionState: UISessionState) => (
    <DaemonApiProvider api={api}>
      <ConversationPane sessionId={SESSION} paneActive sessionState={sessionState} />
    </DaemonApiProvider>
  );
  const result = render(tree('recoverable'));
  return { rerender: (state: UISessionState) => result.rerender(tree(state)) };
}
