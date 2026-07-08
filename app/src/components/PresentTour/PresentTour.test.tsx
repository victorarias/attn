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

// Every render's `items` array is captured here so tests can assert on the
// `version` CodeView receives, without CodeView itself needing to be real.
const codeViewRenders = vi.hoisted(() => ({ calls: [] as Array<Array<Record<string, unknown>>> }));

vi.mock('@pierre/diffs/react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@pierre/diffs/react')>();
  const MockCodeView = forwardRef((props: Record<string, unknown>, ref) => {
    useImperativeHandle(ref, () => ({
      getInstance: () => ({ getRenderedItems: () => [] }),
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
