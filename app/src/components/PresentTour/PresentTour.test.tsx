import { forwardRef, useImperativeHandle } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PresentTour, type PresentTourFile, type PresentTourProps } from './index';
import type { ReviewComment } from '../../types/generated';

// The real @pierre/diffs CodeView renders into a shadow-DOM custom element
// (Shiki highlighting, its own virtualized scroller) that jsdom can't
// exercise — see PresentRoot.test.tsx's module doc, which mocks PresentTour
// entirely for that reason. This file instead mocks only CodeView (keeping
// parseDiffFromFile and everything else in @pierre/diffs real), so PresentTour's
// OWN annotation-grouping/outside-diff/N-P logic — the actual subject of this
// slice — gets exercised for real, with a plain div standing in for the
// library's rendering surface.
// Mermaid diagrams are async, and the diagram-layout-change fix is exercised
// against the mocked mermaid module below (jsdom cannot run real mermaid —
// see Markdown.test.tsx's module doc for the same reasoning).
const mermaidMock = vi.hoisted(() => ({
  render: vi.fn(async () => ({ svg: '<svg data-testid="mermaid-svg"></svg>' })),
  initialize: vi.fn(),
}));

vi.mock('mermaid', () => ({
  default: {
    initialize: mermaidMock.initialize,
    render: mermaidMock.render,
  },
}));

// Wraps the real `parseDiffFromFile` in a spy so the per-file item cache's
// "never re-parses an unchanged file" claim is observable, without faking the
// parser itself (everything else in @pierre/diffs stays real, same as the
// react-entry mock below only replaces CodeView).
const parseDiffFromFileSpy = vi.hoisted(() => ({ fn: null as ReturnType<typeof vi.fn> | null }));
vi.mock('@pierre/diffs', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@pierre/diffs')>();
  const spy = vi.fn(actual.parseDiffFromFile);
  parseDiffFromFileSpy.fn = spy;
  return { ...actual, parseDiffFromFile: spy };
});

// Every render's `items` array is captured here so tests can assert on the
// `version` CodeView receives, without CodeView itself needing to be real.
const codeViewRenders = vi.hoisted(() => ({ calls: [] as Array<Array<Record<string, unknown>>> }));

// The latest full props object CodeView received, so scroll tests can invoke
// `props.onScroll(scrollTop)` directly — the same callback PresentTour wires
// up as `handleScroll` — without needing the real library's scroll machinery.
const codeViewProps = vi.hoisted(() => ({ latest: null as Record<string, unknown> | null }));

vi.mock('@pierre/diffs/react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@pierre/diffs/react')>();
  const MockCodeView = forwardRef((props: Record<string, unknown>, ref) => {
    codeViewProps.latest = props;
    useImperativeHandle(ref, () => ({
      // Derived from the mock's own rendered DOM (each `[data-item-id]` div
      // below) rather than hardcoded — in jsdom `getBoundingClientRect()`
      // returns all zeros, so the first item's `top` always lands at 0,
      // which is <= handleScroll's 80px threshold and makes it `bestPath`.
      // That's what lets these tests exercise the real bestPath/nearestPath
      // loop instead of just the pre-loop early returns.
      getInstance: () => ({
        getRenderedItems: () => {
          const container = document.querySelector('[data-testid="mock-codeview"]');
          if (!container) return [];
          return Array.from(container.querySelectorAll<HTMLElement>('[data-item-id]')).map((el) => ({
            id: el.getAttribute('data-item-id') as string,
            element: el,
          }));
        },
      }),
      scrollTo: () => {},
      getItem: () => undefined,
      updateItem: () => false,
      updateItemId: () => false,
      addItems: () => {},
      setSelectedLines: () => {},
      getSelectedLines: () => null,
      clearSelectedLines: () => {},
    }));
    const items = props.items as Array<Record<string, unknown>>;
    codeViewRenders.calls.push(items);
    const renderAnnotation = props.renderAnnotation as (annotation: unknown, item: unknown) => React.ReactNode;
    const renderHeaderPrefix = props.renderHeaderPrefix as ((item: unknown) => React.ReactNode) | undefined;
    const renderHeaderMetadata = props.renderHeaderMetadata as ((item: unknown) => React.ReactNode) | undefined;
    return (
      <div
        ref={props.containerRef as React.Ref<HTMLDivElement>}
        className={props.className as string}
        data-testid="mock-codeview"
      >
        {items.map((item) => (
          <div key={item.id as string} data-item-id={item.id as string}>
            {renderHeaderPrefix?.(item)}
            {renderHeaderMetadata?.(item)}
            {((item.annotations as unknown[]) ?? []).map((annotation: any) => (
              <div key={annotation.metadata.anchorKey}>{renderAnnotation(annotation, item)}</div>
            ))}
          </div>
        ))}
      </div>
    );
  });
  return { ...actual, CodeView: MockCodeView };
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  codeViewRenders.calls = [];
  codeViewProps.latest = null;
});

const noop = () => {};

function baseProps(overrides: Partial<PresentTourProps> = {}): PresentTourProps {
  return {
    files: [],
    comments: [],
    editingCommentId: null,
    readOnlyCommentIds: new Set(),
    onAddComment: noop,
    onEditComment: noop,
    onStartEdit: noop,
    onCancelEdit: noop,
    onResolveComment: noop,
    onDeleteComment: noop,
    reviewedPaths: new Set(),
    onToggleReviewed: noop,
    ...overrides,
  };
}

// A 10-line file with only line 10 changed. Verified against the real
// @pierre/diffs parser: this produces one hunk with a visible additions range
// of [6, 10] (a few lines of leading context plus the change) — lines 1-5 are
// real file content but outside any hunk, giving a genuine "annotation
// targets unchanged code far from the diff" case to test the fallback
// against, rather than guessing at the library's context-line count.
function tinyFile(path: string): PresentTourFile {
  const lines = Array.from({ length: 10 }, (_, i) => `line ${i + 1}`);
  const original = lines.join('\n') + '\n';
  const modified = original.replace('line 10', 'LINE 10');
  return { path, diff: { loading: false, original, modified } };
}

function annotationComment(overrides: Partial<ReviewComment>): ReviewComment {
  return {
    id: 'annot:x',
    content: 'a note',
    filepath: 'src/foo.ts',
    line_start: 8,
    line_end: 8,
    author: 'agent',
    resolved: false,
    created_at: '',
    review_id: '',
    ...overrides,
  };
}

async function waitForSettled() {
  await waitFor(() => {
    expect(screen.getByTestId('mock-codeview')).toBeInTheDocument();
  });
}

describe('PresentTour annotations', () => {
  it('renders an annotation as a read-only thread at its line, author shown as Claude', async () => {
    const comment = annotationComment({ id: 'annot:1', content: 'why this line?' });
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          comments: [comment],
          readOnlyCommentIds: new Set([comment.id]),
          annotationCommentIds: new Set([comment.id]),
        })}
      />
    );
    await waitForSettled();

    const thread = screen.getByTestId('diff-comment-thread');
    expect(thread.textContent).toContain('why this line?');
    expect(thread.textContent).toContain('Claude');
    expect(thread.querySelector('.edit-btn')).toBeNull();
    expect(thread.querySelector('.delete-btn')).toBeNull();
    expect(thread.querySelector('.resolve-btn')).toBeNull();
  });

  it('renders annotation comments before reviewer comments at a shared anchor', async () => {
    const annotation = annotationComment({ id: 'annot:1', content: 'author note', line_start: 8, line_end: 8 });
    const reply: ReviewComment = {
      id: 'reply-1',
      content: 'reviewer reply',
      filepath: 'src/foo.ts',
      line_start: 8,
      line_end: 8,
      author: 'user',
      resolved: false,
      created_at: '',
      review_id: '',
    };
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          comments: [annotation, reply],
          readOnlyCommentIds: new Set([annotation.id]),
          annotationCommentIds: new Set([annotation.id]),
        })}
      />
    );
    await waitForSettled();

    const thread = screen.getByTestId('diff-comment-thread');
    const bodies = Array.from(thread.querySelectorAll('.diff-comment-content')).map((el) => el.textContent);
    expect(bodies).toEqual([expect.stringContaining('author note'), expect.stringContaining('reviewer reply')]);
  });

  it('shows a Reply button on a read-only annotation thread; clicking opens a draft that submits on the same anchor', async () => {
    const comment = annotationComment({ id: 'annot:1', content: 'why this line?', line_start: 8, line_end: 8 });
    const onAddComment = vi.fn();
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          comments: [comment],
          readOnlyCommentIds: new Set([comment.id]),
          annotationCommentIds: new Set([comment.id]),
          onAddComment,
        })}
      />
    );
    await waitForSettled();

    const replyBtn = screen.getByRole('button', { name: 'Reply' });
    act(() => {
      replyBtn.click();
    });

    const form = await screen.findByTestId('diff-comment-form');
    const textarea = form.querySelector('textarea')!;
    fireEvent.change(textarea, { target: { value: 'my reply' } });
    const saveBtn = screen.getByRole('button', { name: 'Save' });
    act(() => {
      saveBtn.click();
    });

    expect(onAddComment).toHaveBeenCalledWith('src/foo.ts', 8, 8, 'my reply');
  });

  it('re-anchors an out-of-hunk annotation to the nearest visible line, with a caption', async () => {
    // line 1 is real file content but outside tinyFile's visible additions
    // range ([6, 10]) — the annotation must still render, re-anchored to the
    // nearest visible line (6).
    const comment = annotationComment({ id: 'annot:1', content: 'off in the weeds', line_start: 1, line_end: 1 });
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          comments: [comment],
          readOnlyCommentIds: new Set([comment.id]),
          annotationCommentIds: new Set([comment.id]),
        })}
      />
    );
    await waitForSettled();

    const thread = screen.getByTestId('diff-comment-thread');
    expect(thread.textContent).toContain('off in the weeds');
    expect(thread.textContent).toContain('refers to line 1, outside the visible diff');
  });

  it('still drops a non-annotation comment anchored outside the visible diff', async () => {
    const comment: ReviewComment = {
      id: 'reply-1',
      content: 'stray reply',
      filepath: 'src/foo.ts',
      line_start: 1,
      line_end: 1,
      author: 'user',
      resolved: false,
      created_at: '',
      review_id: '',
    };
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          comments: [comment],
          readOnlyCommentIds: new Set([comment.id]),
          annotationCommentIds: new Set(),
        })}
      />
    );
    await waitForSettled();

    expect(screen.queryByTestId('diff-comment-thread')).toBeNull();
  });

  it('reports annotation anchors in document order (files, then line) and dedupes shared anchors', async () => {
    const onAnnotationAnchorsChange = vi.fn();
    const fileB = tinyFile('src/b.ts');
    const commentsA = [
      annotationComment({ id: 'a1', filepath: 'src/foo.ts', line_start: 8, line_end: 8, content: 'a1' }),
    ];
    const commentsB = [
      annotationComment({ id: 'b1', filepath: 'src/b.ts', line_start: 8, line_end: 8, content: 'b1' }),
      annotationComment({ id: 'b2', filepath: 'src/b.ts', line_start: 8, line_end: 8, content: 'b2' }), // shares b1's anchor
    ];
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts'), fileB],
          comments: [...commentsA, ...commentsB],
          readOnlyCommentIds: new Set(['a1', 'b1', 'b2']),
          annotationCommentIds: new Set(['a1', 'b1', 'b2']),
          onAnnotationAnchorsChange,
        })}
      />
    );
    await waitForSettled();

    await waitFor(() => {
      expect(onAnnotationAnchorsChange).toHaveBeenCalled();
    });
    const lastCall = onAnnotationAnchorsChange.mock.calls[onAnnotationAnchorsChange.mock.calls.length - 1][0];
    expect(lastCall).toEqual([
      { path: 'src/foo.ts', anchorKey: 'src/foo.ts:additions:8' },
      { path: 'src/b.ts', anchorKey: 'src/b.ts:additions:8' },
    ]);
  });

  it('renders a file note as the first annotation on the first visible line, suppressing the header fallback', async () => {
    const file = tinyFile('src/foo.ts');
    file.note = 'a note about this file';
    const comment = annotationComment({ id: 'annot:1', content: 'a comment', line_start: 8, line_end: 8 });
    render(
      <PresentTour
        {...baseProps({
          files: [file],
          comments: [comment],
          readOnlyCommentIds: new Set([comment.id]),
          annotationCommentIds: new Set([comment.id]),
        })}
      />
    );
    await waitForSettled();

    const latestItems = codeViewRenders.calls[codeViewRenders.calls.length - 1];
    const item = latestItems[0] as { annotations: Array<{ metadata: Record<string, unknown> }> };
    // tinyFile's visible additions range starts at line 6 (see its doc comment).
    expect(item.annotations[0].metadata).toMatchObject({ kind: 'note', side: 'additions', lineNumber: 6 });

    // Rendered exactly once — the header fallback did not also render it.
    expect(screen.getAllByText('a note about this file')).toHaveLength(1);
  });

  it('excludes the file note from the N/P annotation-anchors payload', async () => {
    const onAnnotationAnchorsChange = vi.fn();
    const file = tinyFile('src/foo.ts');
    file.note = 'file note text';
    const comment = annotationComment({ id: 'annot:1', content: 'real annotation', line_start: 8, line_end: 8 });
    render(
      <PresentTour
        {...baseProps({
          files: [file],
          comments: [comment],
          readOnlyCommentIds: new Set([comment.id]),
          annotationCommentIds: new Set([comment.id]),
          onAnnotationAnchorsChange,
        })}
      />
    );
    await waitForSettled();

    await waitFor(() => {
      expect(onAnnotationAnchorsChange).toHaveBeenCalled();
    });
    const lastCall = onAnnotationAnchorsChange.mock.calls[onAnnotationAnchorsChange.mock.calls.length - 1][0];
    expect(lastCall).toEqual([{ path: 'src/foo.ts', anchorKey: 'src/foo.ts:additions:8' }]);
  });

  it('falls back to the header for a file note when its diff has no visible line to anchor to (errored diff)', async () => {
    const file: PresentTourFile = { path: 'src/broken.ts', note: 'note on a broken file', diff: { loading: false, error: 'boom' } };
    render(<PresentTour {...baseProps({ files: [file] })} />);
    await waitForSettled();

    expect(screen.getByText('note on a broken file')).toBeInTheDocument();
    const latestItems = codeViewRenders.calls[codeViewRenders.calls.length - 1];
    expect((latestItems[0] as { annotations?: unknown[] }).annotations).toBeUndefined();
  });
});

describe('PresentTour summary fold', () => {
  it('renders expanded (no collapsed class, body aria-hidden=false) when summaryVisible is omitted or true', async () => {
    render(<PresentTour {...baseProps({ files: [tinyFile('src/foo.ts')], summary: 'The summary text' })} />);
    await waitForSettled();

    const summaryEl = screen.getByTestId('present-tour-summary');
    expect(summaryEl).not.toHaveClass('collapsed');
    const bodyEl = screen.getByTestId('present-tour-summary-body');
    expect(bodyEl).toHaveAttribute('aria-hidden', 'false');
    expect(bodyEl.textContent).toContain('The summary text');
    // The toggle stays present and clickable while expanded.
    expect(screen.getByTestId('present-tour-summary-toggle')).toBeEnabled();
  });

  it('applies the collapsed class and body aria-hidden=true when summaryVisible is false, without unmounting the card', async () => {
    render(
      <PresentTour
        {...baseProps({ files: [tinyFile('src/foo.ts')], summary: 'The summary text', summaryVisible: false })}
      />
    );
    await waitForSettled();

    const summaryEl = screen.getByTestId('present-tour-summary');
    expect(summaryEl).toHaveClass('collapsed');
    const bodyEl = screen.getByTestId('present-tour-summary-body');
    expect(bodyEl).toHaveAttribute('aria-hidden', 'true');
    // Stays mounted (not removed) so the fold can animate.
    expect(bodyEl.textContent).toContain('The summary text');
    // The toggle stays present and clickable while collapsed.
    expect(screen.getByTestId('present-tour-summary-toggle')).toBeEnabled();
  });

  it('clicking the toggle calls onSummaryVisibleChange with the opposite of summaryVisible', async () => {
    const onSummaryVisibleChange = vi.fn();
    const { rerender } = render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          summary: 'The summary text',
          summaryVisible: true,
          onSummaryVisibleChange,
        })}
      />
    );
    await waitForSettled();

    fireEvent.click(screen.getByTestId('present-tour-summary-toggle'));
    expect(onSummaryVisibleChange).toHaveBeenCalledWith(false);

    onSummaryVisibleChange.mockClear();
    rerender(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          summary: 'The summary text',
          summaryVisible: false,
          onSummaryVisibleChange,
        })}
      />
    );
    fireEvent.click(screen.getByTestId('present-tour-summary-toggle'));
    expect(onSummaryVisibleChange).toHaveBeenCalledWith(true);
  });

  it('wheel-down over an at-bottom (no further scroll) card body collapses; wheel-up does not', async () => {
    const onSummaryVisibleChange = vi.fn();
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          summary: 'The summary text',
          summaryVisible: true,
          onSummaryVisibleChange,
        })}
      />
    );
    await waitForSettled();

    const summaryEl = screen.getByTestId('present-tour-summary');
    // jsdom gives every element zero geometry (scrollTop/clientHeight/
    // scrollHeight all 0), so scrollTop + clientHeight >= scrollHeight holds
    // by default — this exercises the "no more content to scroll" case
    // without needing to fake non-zero geometry.
    fireEvent.wheel(summaryEl, { deltaY: -50 });
    expect(onSummaryVisibleChange).not.toHaveBeenCalled();

    fireEvent.wheel(summaryEl, { deltaY: 50 });
    expect(onSummaryVisibleChange).toHaveBeenCalledWith(false);
  });

  it('wheel-down does not collapse while the card body still has content to scroll', async () => {
    const onSummaryVisibleChange = vi.fn();
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          summary: 'The summary text',
          summaryVisible: true,
          onSummaryVisibleChange,
        })}
      />
    );
    await waitForSettled();

    const body = screen.getByTestId('present-tour-summary-body');
    Object.defineProperty(body, 'scrollHeight', { value: 800, configurable: true });
    Object.defineProperty(body, 'clientHeight', { value: 200, configurable: true });
    Object.defineProperty(body, 'scrollTop', { value: 0, configurable: true });

    fireEvent.wheel(screen.getByTestId('present-tour-summary'), { deltaY: 50 });
    expect(onSummaryVisibleChange).not.toHaveBeenCalled();
  });
});

// Regression test for the root-cause bug: the takeover/cold-window-pin
// listener effect used to have `[]` deps, so it ran once against
// `containerRef.current === null` while the first render's `allSettled` was
// still false (diffs load async over the daemon WS in the live app) and then
// never ran again once CodeView actually mounted. Passive scroll tracking
// never armed, `handleScroll` returned early forever, and neither the
// summary fold nor the cold-window scroll pin ever worked outside tests
// (which, unlike the live app, feed pre-loaded diffs so `allSettled` is true
// on the very first render). This must fail on the pre-fix `[]` deps and
// pass once the effect depends on `allSettled`.
describe('PresentTour listener re-attach on deferred load (regression)', () => {
  it('arms passive scroll tracking once CodeView mounts after starting in the loading state', async () => {
    const onActivePathChange = vi.fn();
    const loadingFile: PresentTourFile = { path: 'src/foo.ts', diff: { loading: true } };
    const { rerender } = render(
      <PresentTour {...baseProps({ files: [loadingFile], onActivePathChange })} />
    );

    expect(screen.getByText('Loading tour…')).toBeInTheDocument();

    rerender(<PresentTour {...baseProps({ files: [tinyFile('src/foo.ts')], onActivePathChange })} />);
    await waitForSettled();

    fireEvent.wheel(screen.getByTestId('mock-codeview'));
    act(() => {
      (codeViewProps.latest!.onScroll as (scrollTop: number) => void)(0);
    });

    expect(onActivePathChange).toHaveBeenCalledWith('src/foo.ts');
  });
});

// These exercise handleScroll's new explicit-only Summary semantics: passive
// scroll tracking (the mock's onScroll callback, captured via codeViewProps)
// never reports null, and it is suppressed both before the user's first real
// gesture and while a programmatic scroll (rail/j-k) is still settling.
describe('PresentTour summary fold vs scroll', () => {
  function latestOnScroll(): (scrollTop: number) => void {
    return codeViewProps.latest!.onScroll as (scrollTop: number) => void;
  }

  it('does not report scroll position before any user gesture (mount/cold-window-pin noise)', async () => {
    const onActivePathChange = vi.fn();
    render(
      <PresentTour
        {...baseProps({ files: [tinyFile('src/foo.ts'), tinyFile('src/bar.ts')], onActivePathChange })}
      />
    );
    await waitForSettled();

    act(() => {
      latestOnScroll()(0);
    });
    act(() => {
      latestOnScroll()(50);
    });

    expect(onActivePathChange).not.toHaveBeenCalled();
  });

  it('reports the first file (never null) at the top of the scroller once the user has taken over', async () => {
    const onActivePathChange = vi.fn();
    render(
      <PresentTour
        {...baseProps({ files: [tinyFile('src/foo.ts'), tinyFile('src/bar.ts')], onActivePathChange })}
      />
    );
    await waitForSettled();

    // A real user gesture on the scroller (the takeover listeners live on
    // containerRef, which is this mock's root div) enables passive tracking.
    fireEvent.wheel(screen.getByTestId('mock-codeview'));
    act(() => {
      latestOnScroll()(0);
    });

    expect(onActivePathChange).toHaveBeenCalledWith('src/foo.ts');
    expect(onActivePathChange).not.toHaveBeenCalledWith(null);
  });

  it('suppresses passive reporting while a programmatic scroll settles; user takeover restores it immediately', async () => {
    const onActivePathChange = vi.fn();
    const files = [tinyFile('src/foo.ts'), tinyFile('src/bar.ts')];
    const { rerender } = render(
      <PresentTour {...baseProps({ files, onActivePathChange, scrollToPath: null, scrollNonce: 0 })} />
    );
    await waitForSettled();

    const container = screen.getByTestId('mock-codeview');
    // Arm passive tracking first so an unsuppressed onScroll below would
    // otherwise report — isolating the assertion to suppression, not to the
    // pre-gesture guard covered by the previous test.
    fireEvent.wheel(container);

    // Simulate the rail/j-k path: scrollToPath + an advanced scrollNonce.
    rerender(
      <PresentTour {...baseProps({ files, onActivePathChange, scrollToPath: 'src/bar.ts', scrollNonce: 1 })} />
    );

    act(() => {
      latestOnScroll()(120);
    });
    expect(onActivePathChange).not.toHaveBeenCalled();

    // A real user gesture takes over immediately, even mid-settle.
    fireEvent.wheel(container);
    act(() => {
      latestOnScroll()(120);
    });
    expect(onActivePathChange).toHaveBeenCalled();
  });

  it('clears suppression after the quiet window elapses with no further scroll events', async () => {
    const onActivePathChange = vi.fn();
    const files = [tinyFile('src/foo.ts'), tinyFile('src/bar.ts')];
    const { rerender } = render(
      <PresentTour {...baseProps({ files, onActivePathChange, scrollToPath: null, scrollNonce: 0 })} />
    );
    await waitForSettled();

    fireEvent.wheel(screen.getByTestId('mock-codeview'));
    rerender(<PresentTour {...baseProps({ files, onActivePathChange, scrollToPath: 'src/bar.ts', scrollNonce: 1 })} />);

    act(() => {
      latestOnScroll()(120);
    });
    expect(onActivePathChange).not.toHaveBeenCalled();

    // Real ~250ms wait past the 200ms quiet window — fake timers fight this
    // suite's use of testing-library's `waitFor` (see waitForSettled), so
    // this is a genuine wall-clock wait rather than `vi.advanceTimersByTime`.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 250));
    });

    act(() => {
      latestOnScroll()(80);
    });
    expect(onActivePathChange).toHaveBeenCalled();
  });
});

// Exercises the per-file item cache added to fix the perf bug: a keystroke or
// a state change on ONE file must not re-parse or re-version every OTHER
// file's item. `codeViewRenders.calls` captures every `items` array PresentTour
// hands to CodeView; `parseDiffFromFileSpy` counts real parses.
describe('PresentTour per-file item caching', () => {
  function openDraftOn(path: string, line = 6) {
    const onGutterUtilityClick = codeViewProps.latest!.options as { onGutterUtilityClick: (range: unknown, ctx: { item: { id: string } } ) => void };
    act(() => {
      onGutterUtilityClick.onGutterUtilityClick(
        { side: 'additions', start: line, end: line },
        { item: { id: path } }
      );
    });
  }

  function latestItemsByPath(): Map<string, Record<string, unknown>> {
    const latest = codeViewRenders.calls[codeViewRenders.calls.length - 1];
    return new Map(latest.map((item) => [item.id as string, item]));
  }

  it('typing in a draft does not rebuild items or re-parse', async () => {
    const files = [tinyFile('src/a.ts'), tinyFile('src/b.ts'), tinyFile('src/c.ts')];
    render(<PresentTour {...baseProps({ files })} />);
    await waitForSettled();

    openDraftOn('src/a.ts');
    const form = await screen.findByTestId('diff-comment-form');
    const textarea = form.querySelector('textarea')!;

    const parseCountBefore = parseDiffFromFileSpy.fn!.mock.calls.length;
    const itemsBefore = latestItemsByPath();

    fireEvent.change(textarea, { target: { value: 'h' } });
    fireEvent.change(textarea, { target: { value: 'he' } });
    fireEvent.change(textarea, { target: { value: 'hel' } });
    fireEvent.change(textarea, { target: { value: 'hello' } });

    expect(parseDiffFromFileSpy.fn!.mock.calls.length).toBe(parseCountBefore);
    const itemsAfter = latestItemsByPath();
    for (const [path, before] of itemsBefore) {
      expect(itemsAfter.get(path)).toBe(before); // same object reference — no rebuild at all
    }
    expect(textarea).toHaveValue('hello');
  });

  it('reviewed toggle bumps only that file', async () => {
    const files = [tinyFile('src/a.ts'), tinyFile('src/b.ts'), tinyFile('src/c.ts')];
    const { rerender } = render(<PresentTour {...baseProps({ files })} />);
    await waitForSettled();

    const parseCountBefore = parseDiffFromFileSpy.fn!.mock.calls.length;
    const itemsBefore = latestItemsByPath();

    rerender(<PresentTour {...baseProps({ files, reviewedPaths: new Set(['src/b.ts']) })} />);

    const itemsAfter = latestItemsByPath();
    expect(itemsAfter.get('src/b.ts')).not.toBe(itemsBefore.get('src/b.ts'));
    expect((itemsAfter.get('src/b.ts') as { version: number }).version).not.toBe(
      (itemsBefore.get('src/b.ts') as { version: number }).version
    );
    expect(itemsAfter.get('src/a.ts')).toBe(itemsBefore.get('src/a.ts'));
    expect(itemsAfter.get('src/c.ts')).toBe(itemsBefore.get('src/c.ts'));
    expect(parseDiffFromFileSpy.fn!.mock.calls.length).toBe(parseCountBefore); // cache hit — no re-parse
  });

  it('opening a draft bumps only its file', async () => {
    const files = [tinyFile('src/a.ts'), tinyFile('src/b.ts')];
    render(<PresentTour {...baseProps({ files })} />);
    await waitForSettled();

    const itemsBefore = latestItemsByPath();
    openDraftOn('src/a.ts');
    const itemsAfter = latestItemsByPath();

    expect(itemsAfter.get('src/a.ts')).not.toBe(itemsBefore.get('src/a.ts'));
    expect(itemsAfter.get('src/b.ts')).toBe(itemsBefore.get('src/b.ts'));
  });

  it("editing a comment's content bumps only its file", async () => {
    const commentB = annotationComment({ id: 'b1', filepath: 'src/b.ts', line_start: 8, line_end: 8, content: 'original' });
    const files = [tinyFile('src/a.ts'), tinyFile('src/b.ts')];
    const { rerender } = render(
      <PresentTour
        {...baseProps({
          files,
          comments: [commentB],
          readOnlyCommentIds: new Set([commentB.id]),
          annotationCommentIds: new Set([commentB.id]),
        })}
      />
    );
    await waitForSettled();

    const parseCountBefore = parseDiffFromFileSpy.fn!.mock.calls.length;
    const itemsBefore = latestItemsByPath();

    const updatedCommentB = { ...commentB, content: 'changed' };
    rerender(
      <PresentTour
        {...baseProps({
          files,
          comments: [updatedCommentB],
          readOnlyCommentIds: new Set([commentB.id]),
          annotationCommentIds: new Set([commentB.id]),
        })}
      />
    );

    const itemsAfter = latestItemsByPath();
    expect(itemsAfter.get('src/b.ts')).not.toBe(itemsBefore.get('src/b.ts'));
    expect(itemsAfter.get('src/a.ts')).toBe(itemsBefore.get('src/a.ts'));
    expect(parseDiffFromFileSpy.fn!.mock.calls.length).toBe(parseCountBefore); // only comment metadata changed, not file content
  });

  it('draft content survives an item rebuild (remount insurance)', async () => {
    // CodeView's real virtualization can unmount an annotation slot (scroll
    // far away and back) and remount it later against a fresh item — the
    // mock's DOM-presence-follows-`items` behavior reproduces exactly that
    // when a file temporarily leaves and re-enters `files`. This is the
    // scenario draftContentsRef exists for (see its declaration): the
    // CommentForm that remounts must re-seed from the ref, not lose the
    // typed content, even though the draft's own React state (`drafts`) never
    // depended on `files` and survives the round-trip regardless.
    const fileA = tinyFile('src/a.ts');
    const fileB = tinyFile('src/b.ts');
    const { rerender } = render(<PresentTour {...baseProps({ files: [fileA, fileB] })} />);
    await waitForSettled();

    openDraftOn('src/a.ts');
    const form = await screen.findByTestId('diff-comment-form');
    const textarea = form.querySelector('textarea')!;
    fireEvent.change(textarea, { target: { value: 'typed text' } });
    expect(textarea).toHaveValue('typed text');

    // src/a.ts leaves the manifest — its rendered annotation slot (and
    // CommentForm) unmounts entirely; its file-item cache entry is pruned.
    rerender(<PresentTour {...baseProps({ files: [fileB] })} />);
    expect(screen.queryByTestId('diff-comment-form')).toBeNull();

    // src/a.ts returns — a brand new item/annotation/CommentForm mounts for
    // it (guaranteed cache miss since the entry was pruned), seeded from
    // draftContentsRef rather than any now-absent React state.
    rerender(<PresentTour {...baseProps({ files: [fileA, fileB] })} />);
    const remountedForm = await screen.findByTestId('diff-comment-form');
    expect(remountedForm.querySelector('textarea')).toHaveValue('typed text');
  });
});

describe('PresentTour diagram layout invalidation', () => {
  // CodeView caches item layout keyed by `version` (see the module doc in
  // PresentTour/index.tsx): a mermaid diagram settling asynchronously grows
  // an item's rendered height without CodeView ever learning about it unless
  // something bumps `version`. These tests exercise the fix — a settling
  // diagram (in a file note or an annotation body) must force a version bump,
  // and a version bump must never itself remount the diagram (which would
  // re-fire the async render and bump again, forever).
  const mermaidNote = '```mermaid\ngraph TD;\nA-->B;\n```';

  it('bumps the items version CodeView receives when a file-note diagram finishes rendering', async () => {
    const file = tinyFile('src/foo.ts');
    file.note = mermaidNote;
    render(<PresentTour {...baseProps({ files: [file] })} />);
    await waitForSettled();

    const versionBefore = codeViewRenders.calls[0][0].version;

    await waitFor(() => {
      expect(screen.getByTestId('mermaid-svg')).toBeInTheDocument();
    });
    await waitFor(() => {
      const latest = codeViewRenders.calls[codeViewRenders.calls.length - 1];
      expect(latest[0].version).not.toBe(versionBefore);
    });
  });

  it('bumps the items version CodeView receives when an annotation-body diagram finishes rendering', async () => {
    const comment = annotationComment({ id: 'annot:1', content: mermaidNote, line_start: 8, line_end: 8 });
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          comments: [comment],
          readOnlyCommentIds: new Set([comment.id]),
          annotationCommentIds: new Set([comment.id]),
        })}
      />
    );
    await waitForSettled();

    const versionBefore = codeViewRenders.calls[0][0].version;

    await waitFor(() => {
      expect(screen.getByTestId('mermaid-svg')).toBeInTheDocument();
    });
    await waitFor(() => {
      const latest = codeViewRenders.calls[codeViewRenders.calls.length - 1];
      expect(latest[0].version).not.toBe(versionBefore);
    });
  });

  it('does not remount the diagram when a version bump re-renders CodeView (no infinite loop)', async () => {
    const file = tinyFile('src/foo.ts');
    file.note = mermaidNote;
    render(<PresentTour {...baseProps({ files: [file] })} />);
    await waitForSettled();

    await waitFor(() => {
      expect(screen.getByTestId('mermaid-svg')).toBeInTheDocument();
    });
    await waitFor(() => {
      const versionBefore = codeViewRenders.calls[0][0].version;
      const latest = codeViewRenders.calls[codeViewRenders.calls.length - 1];
      expect(latest[0].version).not.toBe(versionBefore);
    });

    // mermaid.render is called once per mount of a given diagram; a version
    // bump that remounted MermaidDiagram would call it again (and, since the
    // mock always resolves, would bump the version again, and so on forever).
    expect(mermaidMock.render).toHaveBeenCalledTimes(1);
  });
});
