import { StrictMode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { ConversationPane } from './index';
import { DaemonApiProvider, type DaemonApi } from '../../contexts/DaemonApiContext';
import { useConversationsStore } from '../../store/conversations';

// @pierre/diffs renders through a custom element with its own shadow root and a
// Shiki highlighter — a real browser's job, covered by the packaged-app
// scenario. Here the question is only whether the card hands it the patch pi
// produced, so stand in for it with something assertable.
vi.mock('@pierre/diffs/react', () => ({
  PatchDiff: ({ patch }: { patch: string }) => <div data-testid="patch-diff">{patch}</div>,
}));

const SESSION = 'sess-1';

function renderPane(api: Partial<DaemonApi> = {}, { strict = false } = {}) {
  const sendAgentToolDetail = vi.fn();
  const sendAgentClearQueue = vi.fn();
  const full = { sendAgentPrompt: vi.fn(), sendAgentToolDetail, sendAgentClearQueue, ...api } as unknown as DaemonApi;
  const pane = (
    <DaemonApiProvider api={full}>
      <ConversationPane sessionId={SESSION} paneActive />
    </DaemonApiProvider>
  );
  render(strict ? <StrictMode>{pane}</StrictMode> : pane);
  return { sendAgentToolDetail, sendAgentClearQueue };
}

function apply(kind: string, body: Record<string, unknown>, seq: number) {
  act(() => {
    useConversationsStore.getState().applyEnvelope(SESSION, seq, kind, body);
  });
}

function finishBash(overrides: Record<string, unknown> = {}, seq = 2) {
  apply('tool_finished', {
    call_id: 'c1',
    name: 'bash',
    status: 'ok',
    summary: 'rg TODO',
    files: [],
    detail: true,
    patch: false,
    truncated: false,
    full_output: false,
    ...overrides,
  }, seq);
}

describe('ConversationPane tool cards', () => {
  beforeEach(() => {
    useConversationsStore.setState({ conversations: {} });
  });

  it('shows a call as a collapsed line and fetches nothing until it is opened', () => {
    const { sendAgentToolDetail } = renderPane();
    apply('session_ready', {}, 1);
    apply('tool_started', { call_id: 'c1', name: 'bash', summary: 'rg TODO', files: [] }, 2);

    const card = screen.getByTestId('conversation-tool-c1');
    expect(card).toHaveTextContent('bash');
    expect(card).toHaveTextContent('rg TODO');
    expect(card).toHaveAttribute('data-tool-status', 'running');
    expect(screen.queryByTestId('conversation-tool-body')).toBeNull();
    expect(sendAgentToolDetail).not.toHaveBeenCalled();
  });

  it('asks the host for the detail when the card is opened, and only once', () => {
    const { sendAgentToolDetail } = renderPane();
    apply('session_ready', {}, 1);
    finishBash({}, 2);

    fireEvent.click(screen.getByTestId('conversation-tool-toggle'));
    expect(sendAgentToolDetail).toHaveBeenCalledWith(SESSION, 'c1', false);
    expect(screen.getByTestId('conversation-tool-waiting')).toBeInTheDocument();

    // Closing and reopening must not re-ask: the answer is already on its way
    // or already here.
    fireEvent.click(screen.getByTestId('conversation-tool-toggle'));
    fireEvent.click(screen.getByTestId('conversation-tool-toggle'));
    expect(sendAgentToolDetail).toHaveBeenCalledTimes(1);
  });

  // React may invoke a state updater more than once, so the read has to be
  // decided in the click handler and not inside the setter. StrictMode is how
  // that replay is reproducible: with the decision inside the updater, one
  // click asks the host twice.
  it('asks once even when React replays the state update', () => {
    const { sendAgentToolDetail } = renderPane({}, { strict: true });
    apply('session_ready', {}, 1);
    finishBash({}, 2);

    fireEvent.click(screen.getByTestId('conversation-tool-toggle'));
    expect(sendAgentToolDetail).toHaveBeenCalledTimes(1);
  });

  it('draws the output when the detail lands', () => {
    renderPane();
    apply('session_ready', {}, 1);
    finishBash({}, 2);
    fireEvent.click(screen.getByTestId('conversation-tool-toggle'));

    apply('tool_detail', { call_id: 'c1', text: 'src/main.go:12: TODO', full: false, truncated: false }, 3);

    expect(screen.getByTestId('conversation-tool-output')).toHaveTextContent('src/main.go:12: TODO');
    expect(screen.queryByTestId('conversation-tool-waiting')).toBeNull();
  });

  it('offers the whole of a clipped output, and asks for it once', () => {
    const { sendAgentToolDetail } = renderPane();
    apply('session_ready', {}, 1);
    finishBash({ truncated: true, full_output: true }, 2);
    fireEvent.click(screen.getByTestId('conversation-tool-toggle'));
    apply('tool_detail', { call_id: 'c1', text: 'first 2000 lines', full: false, truncated: true, full_output_path: '/tmp/out' }, 3);

    fireEvent.click(screen.getByTestId('conversation-tool-full'));
    expect(sendAgentToolDetail).toHaveBeenLastCalledWith(SESSION, 'c1', true);
    expect(screen.getByTestId('conversation-tool-full')).toBeDisabled();

    apply('tool_detail', { call_id: 'c1', text: 'every last line', full: true, truncated: false }, 4);
    expect(screen.getByTestId('conversation-tool-output')).toHaveTextContent('every last line');
    expect(screen.queryByTestId('conversation-tool-full')).toBeNull();
  });

  it('says so when a clipped output has nothing behind it', () => {
    renderPane();
    apply('session_ready', {}, 1);
    finishBash({ truncated: true, full_output: false }, 2);
    fireEvent.click(screen.getByTestId('conversation-tool-toggle'));
    apply('tool_detail', { call_id: 'c1', text: 'what there is', full: false, truncated: true }, 3);

    expect(screen.queryByTestId('conversation-tool-full')).toBeNull();
    expect(screen.getByTestId('conversation-tool-truncated')).toBeInTheDocument();
  });

  it('draws an edit as a diff of the patch pi produced', () => {
    renderPane();
    apply('session_ready', {}, 1);
    apply('tool_finished', {
      call_id: 'c1', name: 'edit', status: 'ok', summary: 'app/main.go', files: ['app/main.go'],
      detail: true, patch: true, truncated: false, full_output: false,
    }, 2);
    fireEvent.click(screen.getByTestId('conversation-tool-toggle'));

    const patch = '--- a/app/main.go\n+++ b/app/main.go\n@@ -1,3 +1,3 @@\n-old\n+new\n';
    apply('tool_detail', { call_id: 'c1', text: '', patch, full: false, truncated: false }, 3);

    expect(screen.getByTestId('conversation-tool-patch')).toBeInTheDocument();
    expect(screen.getByTestId('patch-diff')).toHaveTextContent('+new');
    expect(screen.getByTestId('conversation-tool-files')).toHaveTextContent('app/main.go');
  });

  it('shows a failure without asking the reader to open the card', () => {
    renderPane();
    apply('session_ready', {}, 1);
    finishBash({ status: 'error', error: 'exit status 2: no such file' }, 2);

    expect(screen.getByTestId('conversation-tool-c1')).toHaveAttribute('data-tool-status', 'error');
    expect(screen.getByTestId('conversation-tool-error')).toHaveTextContent('exit status 2: no such file');
  });

  it('names why the detail is missing and offers the ask again', () => {
    const { sendAgentToolDetail } = renderPane();
    apply('session_ready', {}, 1);
    finishBash({}, 2);
    fireEvent.click(screen.getByTestId('conversation-tool-toggle'));
    apply('tool_detail', { call_id: 'c1', text: '', full: false, truncated: false, error: 'no detail held for call c1: the 16 MB budget evicted 3 older calls' }, 3);

    expect(screen.getByTestId('conversation-tool-detail-error')).toHaveTextContent('16 MB budget');
    fireEvent.click(screen.getByTestId('conversation-tool-retry'));
    expect(sendAgentToolDetail).toHaveBeenCalledTimes(2);
  });

  it('cancels the queue and waits for pi to say it is empty', () => {
    const { sendAgentClearQueue } = renderPane();
    apply('session_ready', {}, 1);
    apply('run_started', {}, 2);
    apply('queue_update', { steering: ['cut in'], followUp: ['and then'] }, 3);

    expect(screen.getAllByTestId('conversation-queued')).toHaveLength(2);

    fireEvent.click(screen.getByTestId('conversation-queue-clear'));
    expect(sendAgentClearQueue).toHaveBeenCalledWith(SESSION);
    // Still shown: the strip is pi's answer about pi's queues, and nothing has
    // come back yet.
    expect(screen.getAllByTestId('conversation-queued')).toHaveLength(2);

    apply('queue_update', { steering: [], followUp: [] }, 4);
    expect(screen.queryByTestId('conversation-queued')).toBeNull();
    expect(screen.queryByTestId('conversation-queue-clear')).toBeNull();
  });
});
