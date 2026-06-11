import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '../test/utils';
import type { DaemonTour } from '../hooks/useDaemonSocket';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';
import { TourConnectionState, TourStatus } from '../types/generated';
import { TourPanel } from './TourPanel';

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async () => ({ svg: '<svg><text>diagram</text></svg>' })),
  },
}));

vi.mock('./DiffView', () => ({
  DiffView: ({
    comments,
    expandUnchanged,
    fontSize,
    onAddComment,
  }: {
    comments: Array<{ content: string }>;
    expandUnchanged: boolean;
    fontSize?: number;
    onAddComment: (lineStart: number, lineEnd: number, content: string) => void;
  }) => (
    <div
      data-testid="tour-diff"
      data-comment-count={comments.length}
      data-expand-unchanged={expandUnchanged}
      data-font-size={fontSize}
    >
      {comments.map((comment) => comment.content).join(' ')}
      <button type="button" onClick={() => onAddComment(2, 2, 'Inline review note')}>
        Add mock line comment
      </button>
    </div>
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
  const saveTourDraft = vi.fn(async () => tour);
  const onClose = vi.fn();
  render(
    <TourPanel
      tour={tour}
      resolvedTheme="dark"
      uiScale={1}
      onClose={onClose}
      refreshTour={vi.fn(async () => tour)}
      saveTourDraft={saveTourDraft}
      askTour={askTour}
      submitTour={submitTour}
    />,
  );
  return { askTour, onClose, saveTourDraft, submitTour };
}

describe('TourPanel', () => {
  beforeEach(() => {
    window.localStorage.clear();
    _resetEscapeStackForTest();
  });

  it('sends a Tour-wide question without tying it to the selected file', async () => {
    const { askTour, submitTour } = renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'Start reading' }));
    fireEvent.click(screen.getByRole('button', { name: 'Conversation' }));
    fireEvent.change(screen.getByPlaceholderText('Ask about this Tour'), {
      target: { value: 'Why does this call change?' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Ask agent' }));

    await waitFor(() => {
      expect(askTour).toHaveBeenCalledWith('tour-1', 'Why does this call change?', {
        source: 'tour',
        path: '',
      });
    });
    expect(submitTour).not.toHaveBeenCalled();
    expect(screen.getByPlaceholderText('Ask about this Tour')).toHaveValue('');
  });

  it('ends explicitly from the Tour header', async () => {
    const { submitTour } = renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'Start reading' }));
    fireEvent.click(screen.getByRole('button', { name: 'End tour' }));
    await waitFor(() => {
      expect(submitTour).toHaveBeenCalledWith(
        'tour-1',
        '## Tour feedback\n\nNo additional notes.',
        true,
      );
    });
  });

  it('keeps review submission visible while the conversation drawer is closed', async () => {
    const { submitTour } = renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'Start reading' }));

    expect(screen.queryByLabelText('Tour conversation')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Send review to agent' }));

    await waitFor(() => {
      expect(submitTour).toHaveBeenCalledWith(
        'tour-1',
        '## Tour feedback\n\nNo additional notes.',
        false,
      );
    });
    expect(screen.getByRole('button', { name: 'Send review to agent' })).toHaveTextContent('Review sent');
  });

  it('keeps file annotations inline and leaves the conversation free of annotation forms', () => {
    const annotatedTour: DaemonTour = {
      ...tour,
      files: [{
        ...tour.files[0],
        annotations: [{
          id: 'listener-default',
          line_start: 2,
          line_end: 2,
          comments: [{ author: 'agent', body: 'Listening is the default.' }],
        }],
      }],
    };
    window.localStorage.setItem('attn.tour.briefing.tour-1', '1');
    render(
      <TourPanel
        tour={annotatedTour}
        resolvedTheme="dark"
        uiScale={1}
        onClose={vi.fn()}
        refreshTour={vi.fn(async () => annotatedTour)}
        saveTourDraft={vi.fn(async () => annotatedTour)}
        askTour={vi.fn(async () => annotatedTour)}
        submitTour={vi.fn(async () => annotatedTour)}
      />,
    );

    expect(screen.getByTestId('tour-diff')).toHaveAttribute('data-comment-count', '1');
    expect(screen.getByTestId('tour-diff')).toHaveTextContent('Listening is the default.');
    fireEvent.click(screen.getByRole('button', { name: 'Conversation' }));
    expect(screen.queryByPlaceholderText('Feedback on this file')).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText('Reply to this annotation')).not.toBeInTheDocument();
    expect(screen.getByPlaceholderText('Ask about this Tour')).toBeInTheDocument();
  });

  it('shows the briefing on first open and avoids rerendering stable diagrams', async () => {
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

    expect(document.querySelector('.tour-panel__briefing')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'How to read this change' })).toBeInTheDocument();
    await waitFor(() => expect(mermaid.render).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 0));
    const initialRenderCount = vi.mocked(mermaid.render).mock.calls.length;
    expect(screen.getByRole('button', { name: 'Reset diagram zoom' })).toHaveTextContent('100%');
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in diagram' }));
    expect(screen.getByRole('button', { name: 'Reset diagram zoom' })).toHaveTextContent('125%');

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
    expect(mermaid.render).toHaveBeenCalledTimes(initialRenderCount);
  });

  it('closes the topmost Tour layer with Escape before dismissing fullscreen', () => {
    const { onClose } = renderPanel();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByRole('heading', { name: 'How to read this change' })).not.toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Conversation' }));
    expect(screen.getByLabelText('Tour conversation')).toBeInTheDocument();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByLabelText('Tour conversation')).not.toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('does not repeat a briefing already seen for the same Tour', () => {
    window.localStorage.setItem('attn.tour.briefing.tour-1', '1');
    renderPanel();
    expect(screen.queryByRole('heading', { name: 'How to read this change' })).not.toBeInTheDocument();
  });

  it('groups large Tours into chapters and filters authored hotspots', () => {
    const largeTour: DaemonTour = {
      ...tour,
      files: [
        {
          ...tour.files[0],
          chapter_id: 'protocol',
          chapter_title: 'Protocol and persistence',
          chapter_summary: 'Establish the durable contract.',
          risk_note: 'Verify old clients reject this protocol shape.',
        },
        {
          ...tour.files[0],
          path: 'app/src/Tour.tsx',
          chapter_id: 'experience',
          chapter_title: 'Reader experience',
          chapter_summary: 'Follow the fullscreen review flow.',
          note: 'Read the interaction model.',
        },
      ],
    };
    window.localStorage.setItem('attn.tour.briefing.tour-1', '1');
    render(
      <TourPanel
        tour={largeTour}
        resolvedTheme="dark"
        uiScale={1}
        onClose={vi.fn()}
        refreshTour={vi.fn(async () => largeTour)}
        saveTourDraft={vi.fn(async () => largeTour)}
        askTour={vi.fn(async () => largeTour)}
        submitTour={vi.fn(async () => largeTour)}
      />,
    );

    expect(screen.getByText('Protocol and persistence')).toBeInTheDocument();
    expect(screen.getByText('Reader experience')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Hotspots' }));
    expect(screen.getAllByText('main.go').length).toBeGreaterThan(0);
    expect(screen.queryByText('Tour.tsx')).not.toBeInTheDocument();
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
        themeVariables: expect.objectContaining({ fontSize: '24px' }),
      }));
    });
  });

  it('renders Markdown files by default and can switch back to the diff', () => {
    const markdownTour: DaemonTour = {
      ...tour,
      files: [{
        ...tour.files[0],
        path: 'docs/review.md',
        modified: '# Rendered review\n\nThis is **formatted** Markdown.',
      }],
    };
    window.localStorage.setItem('attn.tour.briefing.tour-1', '1');
    render(
      <TourPanel
        tour={markdownTour}
        resolvedTheme="dark"
        uiScale={1}
        onClose={vi.fn()}
        refreshTour={vi.fn(async () => markdownTour)}
        saveTourDraft={vi.fn(async () => markdownTour)}
        askTour={vi.fn(async () => markdownTour)}
        submitTour={vi.fn(async () => markdownTour)}
      />,
    );

    expect(screen.getByRole('heading', { name: 'Rendered review' })).toBeInTheDocument();
    expect(screen.getByText('formatted')).toHaveProperty('tagName', 'STRONG');
    expect(screen.queryByTestId('tour-diff')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Changes' }));
    expect(screen.getByTestId('tour-diff')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Rendered review' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Rendered' }));
    expect(screen.getByRole('heading', { name: 'Rendered review' })).toBeInTheDocument();
  });

  it('uses the line-numbered diff surface for Markdown source mode', () => {
    const markdownTour: DaemonTour = {
      ...tour,
      files: [{
        ...tour.files[0],
        path: 'docs/review.md',
        view: 'content',
        original: '',
        modified: '# Rendered review\n\nThis is **formatted** Markdown.',
        annotations: [{
          id: 'markdown-note',
          line_start: 3,
          line_end: 3,
          comments: [{ author: 'agent', body: 'Check the wording.' }],
        }],
      }],
    };
    window.localStorage.setItem('attn.tour.briefing.tour-1', '1');
    render(
      <TourPanel
        tour={markdownTour}
        resolvedTheme="dark"
        uiScale={1}
        onClose={vi.fn()}
        refreshTour={vi.fn(async () => markdownTour)}
        saveTourDraft={vi.fn(async () => markdownTour)}
        askTour={vi.fn(async () => markdownTour)}
        submitTour={vi.fn(async () => markdownTour)}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Source' }));
    expect(screen.getByTestId('tour-diff')).toHaveAttribute('data-expand-unchanged', 'true');
    expect(screen.getByTestId('tour-diff')).toHaveAttribute('data-comment-count', '1');
    expect(screen.queryByText('# Rendered review', { selector: 'code' })).not.toBeInTheDocument();
  });

  it('supports Jaunt-style navigation without firing shortcuts while typing', async () => {
    const keyboardTour: DaemonTour = {
      ...tour,
      files: [
        tour.files[0],
        {
          ...tour.files[0],
          path: 'second.go',
          note: 'Read this second.',
        },
      ],
    };
    window.localStorage.setItem('attn.tour.briefing.tour-1', '1');
    const saveTourDraft = vi.fn(async () => keyboardTour);
    render(
      <TourPanel
        tour={keyboardTour}
        resolvedTheme="dark"
        uiScale={1}
        onClose={vi.fn()}
        refreshTour={vi.fn(async () => keyboardTour)}
        saveTourDraft={saveTourDraft}
        askTour={vi.fn(async () => keyboardTour)}
        submitTour={vi.fn(async () => keyboardTour)}
      />,
    );

    const panel = screen.getByRole('dialog', { name: 'Code Tour: Listener tour' });
    fireEvent.keyDown(panel, { key: 'j' });
    expect(screen.getByRole('heading', { name: 'second.go' })).toBeInTheDocument();
    fireEvent.keyDown(panel, { key: 'k' });
    expect(screen.getByRole('heading', { name: 'main.go' })).toBeInTheDocument();
    fireEvent.keyDown(panel, { key: 'r' });
    await waitFor(() => {
      expect(saveTourDraft).toHaveBeenCalledWith(
        'tour-1',
        expect.objectContaining({ path: 'main.go', reviewed: true }),
      );
    });

    fireEvent.keyDown(panel, { key: 'a' });
    const question = screen.getByPlaceholderText('Ask about this Tour');
    await waitFor(() => expect(question).toHaveFocus());
    fireEvent.keyDown(question, { key: 'j' });
    expect(screen.getByRole('heading', { name: 'main.go' })).toBeInTheDocument();
  });

  it('recaptures focus and blocks Tour keystrokes from a background terminal', () => {
    window.localStorage.setItem('attn.tour.briefing.tour-1', '1');
    const terminalKeyDown = vi.fn();
    render(
      <>
        <div
          className="terminal-container"
          data-testid="background-terminal"
          onKeyDown={terminalKeyDown}
          tabIndex={0}
        />
        <TourPanel
          tour={tour}
          resolvedTheme="dark"
          uiScale={1}
          onClose={vi.fn()}
          refreshTour={vi.fn(async () => tour)}
          saveTourDraft={vi.fn(async () => tour)}
          askTour={vi.fn(async () => tour)}
          submitTour={vi.fn(async () => tour)}
        />
      </>,
    );

    const terminal = screen.getByTestId('background-terminal');
    terminal.focus();
    fireEvent.keyDown(terminal, { key: 'j' });

    expect(terminalKeyDown).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog', { name: 'Code Tour: Listener tour' })).toHaveFocus();
  });
});
