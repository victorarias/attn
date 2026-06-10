import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '../test/utils';
import type { DaemonTour } from '../hooks/useDaemonSocket';
import { TourConnectionState, TourStatus } from '../types/generated';
import { TourPanel } from './TourPanel';

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async () => ({ svg: '<svg><text>diagram</text></svg>' })),
  },
}));

vi.mock('./DiffView', () => ({
  DiffView: ({ fontSize }: { fontSize?: number }) => (
    <div data-testid="tour-diff" data-font-size={fontSize}>diff</div>
  ),
}));

const tour: DaemonTour = {
  tour_id: 'tour-1',
  session_id: 'session-1',
  name: 'Listener tour',
  repo_path: '/repo',
  guide_path: '/home/user/.attn/tours/guide.yml',
  base_ref: 'main',
  status: TourStatus.Active,
  connection_state: TourConnectionState.Connected,
  summary: '# Summary\n\n```mermaid\nflowchart LR\nA-->B\n```',
  warnings: [],
  files: [{
    path: 'main.go',
    status: 'modified',
    additions: 2,
    deletions: 1,
    group: 'tour',
    view: 'diff',
    note: 'Read this first.',
    original: 'package main\nold()\n',
    modified: 'package main\nnewCall()\n',
    annotations: [],
  }],
  drafts: [],
  transcript: [],
  listener_event_seq: 0,
  created_at: '2026-06-10T10:00:00Z',
  updated_at: '2026-06-10T10:00:00Z',
};

function renderPanel() {
  const askTour = vi.fn(async () => tour);
  const submitTour = vi.fn(async () => tour);
  render(
    <TourPanel
      tour={tour}
      resolvedTheme="dark"
      uiScale={1}
      onClose={vi.fn()}
      refreshTour={vi.fn(async () => tour)}
      saveTourDraft={vi.fn(async () => tour)}
      askTour={askTour}
      submitTour={submitTour}
    />,
  );
  return { askTour, submitTour };
}

describe('TourPanel', () => {
  it('sends a contextual question without adding it to feedback', async () => {
    const { askTour, submitTour } = renderPanel();
    fireEvent.change(screen.getByPlaceholderText('Ask about the selected file'), {
      target: { value: 'Why does this call change?' },
    });
    fireEvent.change(screen.getByPlaceholderText('Start line'), { target: { value: '2' } });
    fireEvent.change(screen.getByPlaceholderText('End line'), { target: { value: '2' } });
    fireEvent.click(screen.getByRole('button', { name: 'Ask question' }));

    await waitFor(() => {
      expect(askTour).toHaveBeenCalledWith('tour-1', 'Why does this call change?', {
        source: 'tour',
        path: 'main.go',
        line_start: 2,
        line_end: 2,
        code: 'newCall()',
      });
    });
    expect(submitTour).not.toHaveBeenCalled();
    expect(screen.getByPlaceholderText('Ask about the selected file')).toHaveValue('');
  });

  it('ends explicitly and states that feedback stays off GitHub', async () => {
    const { submitTour } = renderPanel();
    expect(screen.getByText(/Nothing is submitted to GitHub/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'End tour' }));
    await waitFor(() => {
      expect(submitTour).toHaveBeenCalledWith(
        'tour-1',
        '## Tour feedback\n\nNo additional notes.',
        true,
      );
    });
  });

  it('keeps the briefing in the main reading area and avoids rerendering stable diagrams', async () => {
    const mermaid = (await import('mermaid')).default;
    vi.mocked(mermaid.render).mockClear();
    const { rerender } = render(
      <TourPanel
        tour={tour}
        resolvedTheme="dark"
        uiScale={1}
        onClose={vi.fn()}
        refreshTour={vi.fn(async () => tour)}
        saveTourDraft={vi.fn(async () => tour)}
        askTour={vi.fn(async () => tour)}
        submitTour={vi.fn(async () => tour)}
      />,
    );

    expect(document.querySelector('.tour-panel__main .tour-panel__summary')).toBeInTheDocument();
    expect(document.querySelector('.tour-panel__rail .tour-panel__summary')).not.toBeInTheDocument();
    await waitFor(() => expect(mermaid.render).toHaveBeenCalledTimes(1));

    rerender(
      <TourPanel
        tour={{ ...tour, drafts: [{ path: 'main.go', reviewed: true, note: '', annotation_replies: [], line_comments: [] }] }}
        resolvedTheme="dark"
        uiScale={1}
        onClose={vi.fn()}
        refreshTour={vi.fn(async () => tour)}
        saveTourDraft={vi.fn(async () => tour)}
        askTour={vi.fn(async () => tour)}
        submitTour={vi.fn(async () => tour)}
      />,
    );

    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(mermaid.render).toHaveBeenCalledTimes(1);
  });

  it('passes the custom UI scale through to diagrams and diff content', async () => {
    const mermaid = (await import('mermaid')).default;
    vi.mocked(mermaid.initialize).mockClear();
    render(
      <TourPanel
        tour={tour}
        resolvedTheme="dark"
        uiScale={1.5}
        onClose={vi.fn()}
        refreshTour={vi.fn(async () => tour)}
        saveTourDraft={vi.fn(async () => tour)}
        askTour={vi.fn(async () => tour)}
        submitTour={vi.fn(async () => tour)}
      />,
    );

    expect(screen.getByTestId('tour-diff')).toHaveAttribute('data-font-size', '19.5');
    await waitFor(() => {
      expect(mermaid.initialize).toHaveBeenCalledWith(expect.objectContaining({
        themeVariables: expect.objectContaining({ fontSize: '20px' }),
      }));
    });
  });
});
