import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { ConversationPane } from './index';
import { DaemonApiProvider, type DaemonApi } from '../../contexts/DaemonApiContext';
import { useConversationsStore } from '../../store/conversations';

const SESSION = 'sess-1';

function renderPane(sendAgentPrompt = vi.fn()) {
  const api = { sendAgentPrompt } as unknown as DaemonApi;
  render(
    <DaemonApiProvider api={api}>
      <ConversationPane sessionId={SESSION} paneActive />
    </DaemonApiProvider>,
  );
  return sendAgentPrompt;
}

// Envelopes land from the socket, outside React's event loop, so applying one
// is an act() the same way a socket message is.
function apply(kind: string, body: Record<string, unknown>, seq: number) {
  act(() => {
    useConversationsStore.getState().applyEnvelope(SESSION, seq, kind, body);
  });
}

describe('ConversationPane', () => {
  beforeEach(() => {
    useConversationsStore.setState({ conversations: {} });
  });

  it('holds the composer shut until the host is ready', () => {
    renderPane();
    expect(screen.getByTestId('conversation-input')).toBeDisabled();

    apply('session_ready', {}, 1);
    expect(screen.getByTestId('conversation-input')).not.toBeDisabled();
  });

  it('sends the prompt and clears the draft', () => {
    const sendAgentPrompt = renderPane();
    apply('session_ready', {}, 1);

    const input = screen.getByTestId('conversation-input');
    fireEvent.change(input, { target: { value: 'first prompt' } });
    fireEvent.click(screen.getByTestId('conversation-send'));

    expect(sendAgentPrompt).toHaveBeenCalledWith(SESSION, 'first prompt');
    expect(input).toHaveValue('');
  });

  it('sends on Enter and breaks the line on Shift+Enter', () => {
    const sendAgentPrompt = renderPane();
    apply('session_ready', {}, 1);

    const input = screen.getByTestId('conversation-input');
    fireEvent.change(input, { target: { value: 'line one' } });
    fireEvent.keyDown(input, { key: 'Enter', shiftKey: true });
    expect(sendAgentPrompt).not.toHaveBeenCalled();

    fireEvent.keyDown(input, { key: 'Enter' });
    expect(sendAgentPrompt).toHaveBeenCalledWith(SESSION, 'line one');
  });

  it('closes the composer while a run is open and reopens it after settle', () => {
    const sendAgentPrompt = renderPane();
    apply('session_ready', {}, 1);
    apply('run_started', {}, 2);

    const input = screen.getByTestId('conversation-input');
    expect(input).toBeDisabled();
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(sendAgentPrompt).not.toHaveBeenCalled();

    apply('run_settled', {}, 3);
    expect(screen.getByTestId('conversation-input')).not.toBeDisabled();

    fireEvent.change(screen.getByTestId('conversation-input'), { target: { value: 'second prompt' } });
    fireEvent.keyDown(screen.getByTestId('conversation-input'), { key: 'Enter' });
    expect(sendAgentPrompt).toHaveBeenCalledWith(SESSION, 'second prompt');
  });

  // The host's run_started is a round trip away, and the host answers a second
  // prompt mid-run with a log line the user never sees — so the second Enter
  // has to die here, with the second draft still in the box.
  it('takes one prompt per run even before the host answers', () => {
    const sendAgentPrompt = renderPane();
    apply('session_ready', {}, 1);

    const input = screen.getByTestId('conversation-input');
    fireEvent.change(input, { target: { value: 'first prompt' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(input).toBeDisabled();

    fireEvent.change(input, { target: { value: 'second prompt' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    fireEvent.click(screen.getByTestId('conversation-send'));
    expect(sendAgentPrompt).toHaveBeenCalledTimes(1);

    // The host's own run_started lands on an already-open run and moves
    // nothing; the settle is what reopens the composer.
    apply('run_started', {}, 2);
    expect(screen.getByTestId('conversation-input')).toBeDisabled();
    apply('run_settled', {}, 3);
    expect(screen.getByTestId('conversation-input')).not.toBeDisabled();
  });

  // The daemon settles a prompt that reached no host, so the composer that shut
  // itself at send time is not shut forever.
  it('reopens the composer when the daemon settles an undeliverable prompt', () => {
    renderPane();
    apply('session_ready', {}, 1);

    const input = screen.getByTestId('conversation-input');
    fireEvent.change(input, { target: { value: 'prompt into the void' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(input).toBeDisabled();

    apply('run_settled', { error: 'this conversation\'s agent is no longer running' }, 0);
    expect(screen.getByTestId('conversation-input')).not.toBeDisabled();
    expect(screen.getByTestId('conversation-message-error-0')).toHaveTextContent('no longer running');
  });

  it('refuses to send whitespace', () => {
    const sendAgentPrompt = renderPane();
    apply('session_ready', {}, 1);

    fireEvent.change(screen.getByTestId('conversation-input'), { target: { value: '   ' } });
    expect(screen.getByTestId('conversation-send')).toBeDisabled();
    fireEvent.keyDown(screen.getByTestId('conversation-input'), { key: 'Enter' });
    expect(sendAgentPrompt).not.toHaveBeenCalled();
  });

  it('draws the streamed reply as it arrives', () => {
    renderPane();
    apply('session_ready', {}, 1);
    apply('run_started', {}, 2);
    apply('message_start', { id: 'm1', role: 'assistant' }, 3);
    apply('message_delta', { id: 'm1', text: 'partial' }, 4);

    const message = screen.getByTestId('conversation-message-m1');
    expect(message).toHaveTextContent('partial');
    expect(message).toHaveAttribute('data-streaming', 'true');

    apply('message_end', { id: 'm1', role: 'assistant', text: 'partial and whole' }, 5);
    expect(screen.getByTestId('conversation-message-m1')).toHaveTextContent('partial and whole');
    expect(screen.getByTestId('conversation-message-m1')).toHaveAttribute('data-streaming', 'false');
  });
});
