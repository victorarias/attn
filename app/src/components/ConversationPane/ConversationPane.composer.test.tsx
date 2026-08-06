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

    expect(sendAgentPrompt).toHaveBeenCalledWith(SESSION, 'first prompt', 'prompt');
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
    expect(sendAgentPrompt).toHaveBeenCalledWith(SESSION, 'line one', 'prompt');
  });

  // While the agent works, Enter is a steer: the message lands at the agent's
  // next turn boundary instead of waiting for everything it had planned to do.
  it('sends a steer while a run is open and a prompt again after settle', () => {
    const sendAgentPrompt = renderPane();
    apply('session_ready', {}, 1);
    apply('run_started', {}, 2);

    const input = screen.getByTestId('conversation-input');
    expect(input).not.toBeDisabled();
    expect(screen.getByTestId('conversation-send')).toHaveTextContent('Steer');

    fireEvent.change(input, { target: { value: 'actually, look at x first' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(sendAgentPrompt).toHaveBeenCalledWith(SESSION, 'actually, look at x first', 'steer');

    apply('run_settled', {}, 3);
    expect(screen.getByTestId('conversation-send')).toHaveTextContent('Send');

    fireEvent.change(screen.getByTestId('conversation-input'), { target: { value: 'second prompt' } });
    fireEvent.keyDown(screen.getByTestId('conversation-input'), { key: 'Enter' });
    expect(sendAgentPrompt).toHaveBeenCalledWith(SESSION, 'second prompt', 'prompt');
  });

  // The other queue, one button away: a follow-up waits for the run to finish
  // rather than cutting into it.
  it('offers a follow-up only while a run is open', () => {
    const sendAgentPrompt = renderPane();
    apply('session_ready', {}, 1);
    expect(screen.queryByTestId('conversation-follow-up')).toBeNull();

    apply('run_started', {}, 2);
    fireEvent.change(screen.getByTestId('conversation-input'), { target: { value: 'when you are done, push' } });
    fireEvent.click(screen.getByTestId('conversation-follow-up'));

    expect(sendAgentPrompt).toHaveBeenCalledWith(SESSION, 'when you are done, push', 'follow_up');
    expect(screen.getByTestId('conversation-input')).toHaveValue('');
  });

  // Queued, then seen. The queue is the host's word for what pi has not read
  // yet, so the strip clears when the agent reads it and not a moment before.
  it('shows what the agent has not read yet until it reads it', () => {
    renderPane();
    apply('session_ready', {}, 1);
    apply('run_started', {}, 2);
    apply('queue_update', { steering: ['look at x first'], followUp: [] }, 3);

    expect(screen.getByTestId('conversation-queue')).toHaveTextContent('look at x first');
    expect(screen.getByTestId('conversation-queue')).toHaveTextContent('Steering');

    apply('queue_update', { steering: [], followUp: [] }, 4);
    apply('message_start', { id: 'm1', role: 'user' }, 5);
    apply('message_end', { id: 'm1', role: 'user', text: 'look at x first' }, 6);

    expect(screen.queryByTestId('conversation-queue')).toBeNull();
    expect(screen.getByTestId('conversation-message-m1')).toHaveTextContent('look at x first');
  });

  // The host's run_started is a round trip away, and a steer sent into that gap
  // would reach a host with no run to steer — so the second Enter has to die
  // here, with the second draft still in the box.
  it('shuts the composer only for the round trip that opens the run', () => {
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

    // The host's own run_started is the acknowledgement, and it reopens the
    // composer as a steer box rather than leaving it shut for the whole run.
    apply('run_started', {}, 2);
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
