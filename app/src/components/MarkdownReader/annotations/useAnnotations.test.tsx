/**
 * useAnnotations — engine behavior over a real rendered reader DOM:
 * hydrate+paint, debounced generation-counted saves, empty→clear tombstone,
 * stale-save re-hydration, rebase-on-content-change re-persist, orphan
 * flagging, flush-on-unmount, and creation handler shapes.
 *
 * happy-dom has no CSS.highlights, so the painter runs in MarkPainter mode —
 * paint state is assertable as `[data-md-mark="<id>"]` spans.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render } from '@testing-library/react';
import { useRef } from 'react';
import { MarkdownReader } from '../index';
import { createAnchor, extractBlockTexts } from '../anchoring';
import type { SelectionLike } from './selection';
import type { MarkdownAnnotationsTransport } from './transport';
import { annotationToWire, type Annotation, type WireAnnotation } from './types';
import { QUICK_LABELS } from './quickLabels';
import {
  ANNOTATION_HYDRATE_RETRY_MS,
  ANNOTATION_SAVE_DEBOUNCE_MS,
  ANNOTATION_SAVE_RETRY_MS,
  useAnnotations,
  type UseAnnotationsApi,
} from './useAnnotations';
import {
  getMarkdownAnnotationsAutomationHandle,
  registerMarkdownAnnotationsAutomationHandle,
  type MarkdownAnnotationsAutomationHandle,
} from './annotationsAutomation';

vi.mock('@tauri-apps/plugin-opener', () => ({
  openUrl: vi.fn(async () => {}),
}));

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async () => ({ svg: '<svg data-testid="mermaid-svg"></svg>' })),
  },
}));

const shikiMock = vi.hoisted(() => ({
  codeToHtml: vi.fn(async (code: string) =>
    code
      .split('\n')
      .map((line) => `<span>${line}</span>`)
      .join('<br>')),
}));
vi.mock('shiki', () => shikiMock);

const DOC = [
  'First paragraph with target words inside it.',
  '',
  'Second block of plain prose here.',
  '',
].join('\n');
const PATH = '/tmp/project/README.md';

// ---- mock transport --------------------------------------------------------

interface SaveCall {
  path: string;
  annotations: WireAnnotation[];
  generation: number;
}

function makeTransport(seed: { annotations: WireAnnotation[]; generation: number } = { annotations: [], generation: 0 }) {
  const calls = {
    get: [] as string[],
    save: [] as SaveCall[],
    clear: [] as { path: string; generation: number }[],
  };
  const state = { seed, saveResult: { stale: false } };
  const transport: MarkdownAnnotationsTransport = {
    getMarkdownAnnotations: async (path) => {
      calls.get.push(path);
      return { annotations: state.seed.annotations, generation: state.seed.generation };
    },
    saveMarkdownAnnotations: async (path, annotations, generation) => {
      calls.save.push({ path, annotations, generation });
      return state.saveResult;
    },
    clearMarkdownAnnotations: async (path, generation) => {
      calls.clear.push({ path, generation });
      return { generation };
    },
    submitMarkdownAnnotations: async () => ({ status: 'delivered' }),
  };
  return { transport, calls, state };
}

// ---- harness ----------------------------------------------------------------

function Harness({
  content,
  path,
  transport,
  apiRef,
}: {
  content: string;
  path: string;
  transport: MarkdownAnnotationsTransport | null;
  apiRef: { current: UseAnnotationsApi | null };
}) {
  const rootRef = useRef<HTMLDivElement>(null);
  apiRef.current = useAnnotations({ rootRef, content, path, enabled: true, transport });
  return (
    <div ref={rootRef}>
      <MarkdownReader content={content} path={path} allowLocalTargets />
    </div>
  );
}

async function mount(transport: MarkdownAnnotationsTransport | null, content = DOC) {
  const apiRef: { current: UseAnnotationsApi | null } = { current: null };
  const view = render(
    <Harness content={content} path={PATH} transport={transport} apiRef={apiRef} />,
  );
  await flush(); // let hydration settle
  return { ...view, apiRef };
}

/** Flush pending microtasks (hydration/save promise chains) inside act. */
async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

async function advanceDebounce() {
  await act(async () => {
    vi.advanceTimersByTime(ANNOTATION_SAVE_DEBOUNCE_MS);
  });
  await flush();
}

function findTextNode(scope: Element, needle: string): { node: Text; index: number } {
  const walker = document.createTreeWalker(scope, NodeFilter.SHOW_TEXT);
  let node: Node | null;
  while ((node = walker.nextNode())) {
    const text = node as Text;
    const index = text.data.indexOf(needle);
    if (index >= 0) {
      return { node: text, index };
    }
  }
  throw new Error(`text node containing ${JSON.stringify(needle)} not found`);
}

function selectNeedle(container: HTMLElement, needle: string): SelectionLike {
  const { node, index } = findTextNode(container, needle);
  const range = document.createRange();
  range.setStart(node, index);
  range.setEnd(node, index + needle.length);
  return {
    isCollapsed: false,
    rangeCount: 1,
    anchorNode: range.startContainer,
    focusNode: range.endContainer,
    toString: () => range.toString(),
    getRangeAt: () => range,
  };
}

function storedAnnotation(needle: string, overrides: Partial<Annotation> = {}): WireAnnotation {
  const blocks = extractBlockTexts(DOC);
  const block = blocks.find((b) => b.text.includes(needle))!;
  const start = block.text.indexOf(needle);
  const anchor = createAnchor(DOC, block.blockId, start, start + needle.length, blocks)!;
  return annotationToWire({
    id: 'stored-1',
    type: 'comment',
    text: 'stored comment',
    anchor,
    createdAt: 1,
    ...overrides,
  });
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

// ---- tests -------------------------------------------------------------------

describe('useAnnotations', () => {
  it('hydrates the stored draft on mount and paints it', async () => {
    const { transport, calls } = makeTransport({
      annotations: [storedAnnotation('target words')],
      generation: 3,
    });
    const { container, apiRef } = await mount(transport);

    expect(calls.get).toEqual([PATH]);
    expect(apiRef.current!.annotations).toHaveLength(1);
    expect(apiRef.current!.annotations[0].text).toBe('stored comment');
    expect(apiRef.current!.painterMode).toBe('mark');

    const mark = container.querySelector('[data-md-mark="stored-1"]');
    expect(mark).not.toBeNull();
    expect(mark!.textContent).toBe('target words');
    // Hydration alone never writes back.
    expect(calls.save).toHaveLength(0);
    expect(calls.clear).toHaveLength(0);
  });

  it('debounces saves and pre-increments the generation past the hydrated floor', async () => {
    const { transport, calls } = makeTransport({ annotations: [], generation: 7 });
    const { container, apiRef } = await mount(transport);
    const api = apiRef.current!;

    act(() => {
      api.handleSelectionChange(selectNeedle(container, 'target words'));
      api.submitComment('first');
    });
    expect(calls.save).toHaveLength(0); // debounced, not immediate
    await advanceDebounce();
    expect(calls.save).toHaveLength(1);
    expect(calls.save[0]).toMatchObject({ path: PATH, generation: 8 });
    expect(calls.save[0].annotations).toHaveLength(1);
    expect(calls.save[0].annotations[0].type).toBe('comment');
    expect(calls.save[0].annotations[0].text).toBe('first');
    expect(calls.save[0].annotations[0].anchor?.exact).toBe('target words');

    act(() => {
      api.handleSelectionChange(selectNeedle(container, 'plain prose'));
      api.addDeletion();
    });
    await advanceDebounce();
    expect(calls.save).toHaveLength(2);
    expect(calls.save[1].generation).toBe(9); // strictly increasing
    expect(calls.save[1].annotations).toHaveLength(2);
  });

  it('coalesces rapid edits into one save per debounce window', async () => {
    const { transport, calls } = makeTransport();
    const { container, apiRef } = await mount(transport);
    const api = apiRef.current!;

    act(() => {
      api.handleSelectionChange(selectNeedle(container, 'target words'));
      api.submitComment('a');
    });
    act(() => {
      vi.advanceTimersByTime(200);
      api.handleSelectionChange(selectNeedle(container, 'plain prose'));
      api.submitComment('b');
    });
    await advanceDebounce();
    expect(calls.save).toHaveLength(1);
    expect(calls.save[0].annotations).toHaveLength(2);
  });

  it('tombstone-clears instead of saving [] when the last annotation is deleted (E20)', async () => {
    const { transport, calls } = makeTransport({
      annotations: [storedAnnotation('target words')],
      generation: 5,
    });
    const { container, apiRef } = await mount(transport);

    act(() => {
      apiRef.current!.deleteAnnotation('stored-1');
    });
    expect(container.querySelector('[data-md-mark="stored-1"]')).toBeNull();
    await advanceDebounce();
    expect(calls.save).toHaveLength(0);
    expect(calls.clear).toEqual([{ path: PATH, generation: 6 }]);
  });

  it('clearAll clears immediately (no debounce) and unpaints everything', async () => {
    const { transport, calls } = makeTransport({
      annotations: [storedAnnotation('target words')],
      generation: 2,
    });
    const { container, apiRef } = await mount(transport);

    act(() => {
      apiRef.current!.clearAll();
    });
    await flush();
    expect(calls.clear).toEqual([{ path: PATH, generation: 3 }]);
    expect(apiRef.current!.annotations).toHaveLength(0);
    expect(container.querySelector('[data-md-mark]')).toBeNull();
  });

  it('re-hydrates when a save comes back stale (tombstone race)', async () => {
    const { transport, calls, state } = makeTransport({ annotations: [], generation: 0 });
    const { container, apiRef } = await mount(transport);
    state.saveResult = { stale: true };

    act(() => {
      apiRef.current!.handleSelectionChange(selectNeedle(container, 'target words'));
      apiRef.current!.submitComment('doomed');
    });
    // The authoritative draft the re-hydrate will find.
    state.seed = { annotations: [storedAnnotation('plain prose')], generation: 12 };
    await advanceDebounce();

    expect(calls.get).toEqual([PATH, PATH]); // mount + stale re-hydrate
    expect(apiRef.current!.annotations.map((a) => a.id)).toEqual(['stored-1']);
    // Counter seeded past the tombstone: the next save must clear the bar.
    act(() => {
      apiRef.current!.deleteAnnotation('stored-1');
      apiRef.current!.addGlobalComment('after');
    });
    await advanceDebounce();
    expect(calls.save[calls.save.length - 1].generation).toBeGreaterThan(12);
  });

  it('rebases anchors on content change and re-persists the rebased record', async () => {
    const { transport, calls } = makeTransport({
      annotations: [storedAnnotation('target words')],
      generation: 1,
    });
    const view = await mount(transport);
    const original = view.apiRef.current!.annotations[0].anchor!;

    const edited = `A brand new intro paragraph.\n\n${DOC}`;
    await act(async () => {
      view.rerender(
        <Harness content={edited} path={PATH} transport={transport} apiRef={view.apiRef} />,
      );
    });
    await flush();

    const rebased = view.apiRef.current!.annotations[0].anchor!;
    expect(view.apiRef.current!.orphans.size).toBe(0);
    expect(rebased.exact).toBe('target words');
    expect(rebased.startLine).toBeGreaterThan(original.startLine);
    expect(rebased.contentHash).not.toBe(original.contentHash);
    // Still painted at the new position.
    const mark = view.container.querySelector('[data-md-mark="stored-1"]');
    expect(mark?.textContent).toBe('target words');

    // The re-baselined record persists (plan §Anchoring).
    await advanceDebounce();
    expect(calls.save).toHaveLength(1);
    expect(calls.save[0].generation).toBe(2);
    expect(calls.save[0].annotations[0].anchor?.content_hash).toBe(rebased.contentHash);
  });

  it('flags an orphan when the anchored text disappears, keeps it listed, paints nothing', async () => {
    const { transport } = makeTransport({
      annotations: [storedAnnotation('target words')],
      generation: 1,
    });
    const view = await mount(transport);
    expect(view.container.querySelector('[data-md-mark="stored-1"]')).not.toBeNull();

    const edited = 'Completely different first paragraph.\n\nSecond block of plain prose here.\n';
    await act(async () => {
      view.rerender(
        <Harness content={edited} path={PATH} transport={transport} apiRef={view.apiRef} />,
      );
    });
    await flush();

    const api = view.apiRef.current!;
    expect(api.annotations).toHaveLength(1); // orphans stay listed + sendable
    expect(api.orphans.get('stored-1')).toBe('text-not-found');
    expect(view.container.querySelector('[data-md-mark="stored-1"]')).toBeNull();
  });

  it('flushes a pending debounced save on unmount', async () => {
    const { transport, calls } = makeTransport();
    const { container, apiRef, unmount } = await mount(transport);

    act(() => {
      apiRef.current!.handleSelectionChange(selectNeedle(container, 'target words'));
      apiRef.current!.submitComment('almost lost');
    });
    expect(calls.save).toHaveLength(0);
    unmount();
    expect(calls.save).toHaveLength(1);
    expect(calls.save[0].annotations[0].text).toBe('almost lost');
  });

  it('never saves before hydration has completed', async () => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const calls = { save: 0 };
    const transport: MarkdownAnnotationsTransport = {
      getMarkdownAnnotations: async () => {
        await gate;
        return { annotations: [], generation: 0 };
      },
      saveMarkdownAnnotations: async () => {
        calls.save += 1;
        return { stale: false };
      },
      clearMarkdownAnnotations: async (_p, generation) => ({ generation }),
      submitMarkdownAnnotations: async () => ({ status: 'delivered' }),
    };
    const { container, apiRef } = await mount(transport);

    act(() => {
      apiRef.current!.handleSelectionChange(selectNeedle(container, 'target words'));
      apiRef.current!.submitComment('too early');
    });
    await advanceDebounce();
    expect(calls.save).toBe(0); // still un-hydrated: save suppressed

    release();
    await flush();
    // The annotation created pre-hydration was replaced by the authoritative
    // (empty) draft — hydration is the source of truth.
    expect(apiRef.current!.annotations).toHaveLength(0);
  });

  it('builds the right shapes for deletion, quick-label, and global annotations', async () => {
    const { transport } = makeTransport();
    const { container, apiRef } = await mount(transport);
    const api = apiRef.current!;

    let deletion: Annotation | null = null;
    let quick: Annotation | null = null;
    let global: Annotation | null = null;
    act(() => {
      api.handleSelectionChange(selectNeedle(container, 'target words'));
      deletion = api.addDeletion();
    });
    // missing-overview: a label WITH a tip, so the snapshot behavior is visible.
    const label = QUICK_LABELS.find((l) => l.tip !== undefined)!;
    act(() => {
      api.handleSelectionChange(selectNeedle(container, 'plain prose'));
      quick = api.applyQuickLabel(label);
    });
    act(() => {
      global = api.addGlobalComment('whole-doc note');
    });

    expect(deletion!).toMatchObject({ type: 'deletion' });
    expect(deletion!.text).toBeUndefined();
    expect(deletion!.anchor?.exact).toBe('target words');
    // Deletion paints with the deletion kind (strikethrough styling).
    expect(
      container.querySelector(`[data-md-mark="${deletion!.id}"]`)?.classList.contains('md-mark-deletion'),
    ).toBe(true);

    expect(quick!).toMatchObject({
      type: 'comment',
      quickLabelId: label.id,
      quickLabelTip: label.tip,
      // Display text snapshotted at creation — the PR6 daemon formatter
      // renders this without needing the frontend label table.
      quickLabelText: `${label.emoji} ${label.text}`,
    });
    expect(quick!.text).toBeUndefined(); // structured, never baked into text

    expect(global!).toMatchObject({ type: 'global', text: 'whole-doc note' });
    expect(global!.anchor).toBeUndefined();
    expect(container.querySelector(`[data-md-mark="${global!.id}"]`)).toBeNull(); // no paint

    // Creating from pending consumed the pending selection each time.
    let noPending: ReturnType<UseAnnotationsApi['handleSelectionChange']> = null;
    act(() => {
      noPending = api.handleSelectionChange(null);
    });
    expect(noPending).toBeNull();
    expect(apiRef.current!.pending).toBeNull();
    expect(apiRef.current!.annotations).toHaveLength(3);

    // Empty global comments are refused.
    let refused: Annotation | null = null;
    act(() => {
      refused = api.addGlobalComment('   ');
    });
    expect(refused).toBeNull();
  });

  it('paints the pending selection provisionally and clears it on demand (E5)', async () => {
    const { transport } = makeTransport();
    const { container, apiRef } = await mount(transport);
    const api = apiRef.current!;

    let pendingResult: ReturnType<UseAnnotationsApi['handleSelectionChange']> = null;
    act(() => {
      pendingResult = api.handleSelectionChange(selectNeedle(container, 'target words'));
    });
    expect(pendingResult).not.toBeNull();
    expect(apiRef.current!.pending?.anchor.exact).toBe('target words');
    const pendingMark = container.querySelector('[data-md-mark="md-pending-selection"]');
    expect(pendingMark?.textContent).toBe('target words');

    act(() => {
      api.clearPendingSelection();
    });
    expect(apiRef.current!.pending).toBeNull();
    expect(container.querySelector('[data-md-mark="md-pending-selection"]')).toBeNull();
  });

  it('a selection mouseup claims keyboard focus for the reader root (type-to-comment)', async () => {
    // In macOS WebKit, mousedown on non-focusable content does NOT move
    // focus, so after a drag-select the terminal's hidden input can remain
    // document.activeElement — the toolbar's editable-element guard then
    // swallows type-to-comment. The mouseup path must claim focus itself.
    const { transport } = makeTransport();
    const { container, apiRef } = await mount(transport);
    const root = container.firstElementChild as HTMLElement;
    root.tabIndex = -1; // the real reader root sets tabIndex via MarkdownReader

    // A decoy editable element holds focus, standing in for the terminal.
    const decoy = document.createElement('textarea');
    document.body.appendChild(decoy);
    decoy.focus();
    expect(document.activeElement).toBe(decoy);

    const { node, index } = findTextNode(container, 'target words');
    const range = document.createRange();
    range.setStart(node, index);
    range.setEnd(node, index + 'target words'.length);
    const sel = window.getSelection()!;
    sel.removeAllRanges();
    sel.addRange(range);
    act(() => {
      root.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }));
    });

    expect(apiRef.current!.pending?.anchor.exact).toBe('target words');
    expect(document.activeElement).toBe(root);
    decoy.remove();
  });

  it('rejects selections on non-paintable (mermaid) blocks', async () => {
    const mermaidDoc = 'Intro paragraph here.\n\n```mermaid\ngraph TD; A-->B;\n```\n';
    const { transport } = makeTransport();
    const { apiRef } = await mount(transport, mermaidDoc);

    const blocks = extractBlockTexts(mermaidDoc);
    const mermaidBlock = blocks.find((b) => b.nonPaintable)!;
    expect(mermaidBlock).toBeDefined();
    let result: ReturnType<UseAnnotationsApi['beginBlockSelection']> = null;
    act(() => {
      result = apiRef.current!.beginBlockSelection(mermaidBlock.blockId);
    });
    expect(result).toBeNull();

    // But a paintable code block CAN begin a whole-block selection.
    const codeDoc = 'Intro paragraph here.\n\n```js\nconst a = 1;\n```\n';
    const codeView = await mount(transport, codeDoc);
    const codeBlock = extractBlockTexts(codeDoc).find((b) => b.text.includes('const a'))!;
    let codePending: ReturnType<UseAnnotationsApi['beginBlockSelection']> = null;
    act(() => {
      codePending = codeView.apiRef.current!.beginBlockSelection(codeBlock.blockId);
    });
    expect(codePending).not.toBeNull();
    expect(codePending!.isCodeBlock).toBe(true);
    expect(codePending!.anchor.exact).toBe(codeBlock.text);
  });

  it('keeps saves locked after a failed hydration and retries until it succeeds', async () => {
    // The failed-hydration wipe: if a failed hydrate unlocked saves, the
    // first save would go out at generation 1, come back stale against any
    // stored floor, and the stale re-hydrate would wipe the user's fresh
    // annotations. Saves must stay suppressed until a hydrate SUCCEEDS.
    let failNext = true;
    const getCalls: number[] = [];
    const saves: SaveCall[] = [];
    const transport: MarkdownAnnotationsTransport = {
      getMarkdownAnnotations: async () => {
        getCalls.push(getCalls.length);
        if (failNext) {
          throw new Error('Superseded by a newer request');
        }
        return { annotations: [], generation: 9 };
      },
      saveMarkdownAnnotations: async (path, annotations, generation) => {
        saves.push({ path, annotations, generation });
        return { stale: false };
      },
      clearMarkdownAnnotations: async (_p, generation) => ({ generation }),
      submitMarkdownAnnotations: async () => ({ status: 'delivered' }),
    };
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { container, apiRef } = await mount(transport);
    expect(getCalls).toHaveLength(1);
    expect(warn).toHaveBeenCalled();

    // Annotations created while un-hydrated must NOT produce a save.
    act(() => {
      apiRef.current!.handleSelectionChange(selectNeedle(container, 'target words'));
      apiRef.current!.submitComment('made during outage');
    });
    await advanceDebounce();
    expect(saves).toHaveLength(0);

    // The retry timer re-hydrates; success seeds the generation floor.
    failNext = false;
    await act(async () => {
      vi.advanceTimersByTime(ANNOTATION_HYDRATE_RETRY_MS);
    });
    await flush();
    expect(getCalls).toHaveLength(2);

    act(() => {
      apiRef.current!.addGlobalComment('after recovery');
    });
    await advanceDebounce();
    expect(saves).toHaveLength(1);
    expect(saves[0].generation).toBe(10); // seeded past the stored floor of 9
  });

  it('retries a failed save instead of silently dropping the draft', async () => {
    let fail = true;
    const saves: SaveCall[] = [];
    const transport: MarkdownAnnotationsTransport = {
      getMarkdownAnnotations: async () => ({ annotations: [], generation: 0 }),
      saveMarkdownAnnotations: async (path, annotations, generation) => {
        saves.push({ path, annotations, generation });
        if (fail) {
          throw new Error('WebSocket not connected');
        }
        return { stale: false };
      },
      clearMarkdownAnnotations: async (_p, generation) => ({ generation }),
      submitMarkdownAnnotations: async () => ({ status: 'delivered' }),
    };
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { container, apiRef } = await mount(transport);

    act(() => {
      apiRef.current!.handleSelectionChange(selectNeedle(container, 'target words'));
      apiRef.current!.submitComment('keep me');
    });
    await advanceDebounce();
    expect(saves).toHaveLength(1);
    await flush();
    expect(warn).toHaveBeenCalled();

    fail = false;
    await act(async () => {
      vi.advanceTimersByTime(ANNOTATION_SAVE_RETRY_MS);
    });
    await flush();
    expect(saves).toHaveLength(2);
    expect(saves[1].annotations[0].text).toBe('keep me');
    expect(saves[1].generation).toBeGreaterThan(saves[0].generation);
  });

  it('clicking a painted mark selects the annotation (mark-fallback hit-test, E28)', async () => {
    const { transport } = makeTransport({
      annotations: [storedAnnotation('target words')],
      generation: 1,
    });
    const { container, apiRef } = await mount(transport);
    const mark = container.querySelector('[data-md-mark="stored-1"]')!;
    act(() => {
      mark.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
    expect(apiRef.current!.selectedId).toBe('stored-1');
  });

  it('automation registry: closing one tile does not blind the bridge to another', () => {
    const stateA = { available: true } as ReturnType<MarkdownAnnotationsAutomationHandle['getState']>;
    const a: MarkdownAnnotationsAutomationHandle = { getState: () => stateA };
    const b: MarkdownAnnotationsAutomationHandle = { getState: () => stateA };
    const offA = registerMarkdownAnnotationsAutomationHandle(a);
    const offB = registerMarkdownAnnotationsAutomationHandle(b);
    expect(getMarkdownAnnotationsAutomationHandle()).toBe(b); // last-mounted wins
    offA(); // closing tile A must not null tile B's live handle
    expect(getMarkdownAnnotationsAutomationHandle()).toBe(b);
    offB();
    expect(getMarkdownAnnotationsAutomationHandle()).toBeNull();
  });

  // ---- PR6 send-flow primitives (E16/E17) --------------------------------

  it('flushPendingSave persists an armed debounced edit immediately (E16)', async () => {
    const { transport, calls } = makeTransport({ annotations: [], generation: 0 });
    const { container, apiRef } = await mount(transport);

    act(() => {
      apiRef.current!.handleSelectionChange(selectNeedle(container, 'target words'));
      apiRef.current!.submitComment('last keystroke');
    });
    expect(calls.save).toHaveLength(0); // debounce armed, not fired yet

    await act(async () => {
      await apiRef.current!.flushPendingSave();
    });
    expect(calls.save).toHaveLength(1);
    expect(calls.save[0].annotations[0].text).toBe('last keystroke');

    // Nothing pending: resolves as a no-op, no duplicate save.
    await act(async () => {
      await apiRef.current!.flushPendingSave();
    });
    expect(calls.save).toHaveLength(1);
  });

  it('applyDeliveredClear empties locally without a daemon clear and seeds the floor (E16)', async () => {
    const { transport, calls } = makeTransport({
      annotations: [storedAnnotation('target words')],
      generation: 5,
    });
    const { container, apiRef } = await mount(transport);
    expect(apiRef.current!.annotations).toHaveLength(1);

    act(() => {
      apiRef.current!.applyDeliveredClear(9);
    });
    expect(apiRef.current!.annotations).toHaveLength(0);
    expect(container.querySelector('[data-md-mark]')).toBeNull();
    // Local-only mirror: the daemon already tombstoned at delivery time.
    expect(calls.clear).toHaveLength(0);
    expect(calls.save).toHaveLength(0);

    // The next edit's save counts up from the delivered floor, not the old
    // hydrated generation.
    act(() => {
      apiRef.current!.handleSelectionChange(selectNeedle(container, 'plain prose'));
      apiRef.current!.submitComment('after send');
    });
    await advanceDebounce();
    expect(calls.save).toHaveLength(1);
    expect(calls.save[0].generation).toBe(10);
  });

  it('a straggler stale save after a delivered clear cannot resurrect the draft (E17)', async () => {
    // Hold the save's resolution so applyDeliveredClear lands mid-flight —
    // the debounced-save-races-past-Send scenario.
    let resolveSave: ((r: { stale: boolean }) => void) | null = null;
    const calls = { get: 0 };
    const transport: MarkdownAnnotationsTransport = {
      getMarkdownAnnotations: async () => {
        calls.get += 1;
        // Post-tombstone the daemon serves an empty draft.
        return { annotations: [], generation: calls.get === 1 ? 0 : 4 };
      },
      saveMarkdownAnnotations: () =>
        new Promise((resolve) => {
          resolveSave = resolve;
        }),
      clearMarkdownAnnotations: async (_path, generation) => ({ generation }),
      submitMarkdownAnnotations: async () => ({ status: 'delivered' }),
    };
    const { container, apiRef } = await mount(transport);

    act(() => {
      apiRef.current!.handleSelectionChange(selectNeedle(container, 'target words'));
      apiRef.current!.submitComment('raced comment');
    });
    await advanceDebounce(); // save is now in flight, unresolved
    expect(resolveSave).not.toBeNull();

    act(() => {
      apiRef.current!.applyDeliveredClear(3); // Send delivered while the save raced
    });
    expect(apiRef.current!.annotations).toHaveLength(0);

    // The daemon tombstoned past the straggler's generation → stale. The
    // stale path re-hydrates, which returns the empty post-clear draft.
    await act(async () => {
      resolveSave!({ stale: true });
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(apiRef.current!.annotations).toHaveLength(0); // no resurrection
    expect(container.querySelector('[data-md-mark]')).toBeNull();
  });

  it('flushPendingSave awaits a save that already left the debounce window', async () => {
    // The debounce fired and the save request is mid-round-trip: a Send in
    // that window must wait for it, or the daemon reads the draft WITHOUT the
    // edit and the delivered clear tombstones the late save undelivered.
    let resolveSave: ((r: { stale: boolean }) => void) | null = null;
    const transport: MarkdownAnnotationsTransport = {
      getMarkdownAnnotations: async () => ({ annotations: [], generation: 0 }),
      saveMarkdownAnnotations: () =>
        new Promise((resolve) => {
          resolveSave = resolve;
        }),
      clearMarkdownAnnotations: async (_path, generation) => ({ generation }),
      submitMarkdownAnnotations: async () => ({ status: 'delivered' }),
    };
    const { container, apiRef } = await mount(transport);

    act(() => {
      apiRef.current!.handleSelectionChange(selectNeedle(container, 'target words'));
      apiRef.current!.submitComment('mid-flight edit');
    });
    await advanceDebounce(); // save is now on the wire, unresolved
    expect(resolveSave).not.toBeNull();

    let flushed = false;
    await act(async () => {
      void apiRef.current!.flushPendingSave().then(() => {
        flushed = true;
      });
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(flushed).toBe(false); // still awaiting the in-flight save

    await act(async () => {
      resolveSave!({ stale: false });
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(flushed).toBe(true);
  });

  it('applyDeliveredClear marks the hook hydrated and stops the failed-hydrate retry loop', async () => {
    // Hydrate fails → saves are suppressed while the user can still annotate.
    // A delivered Send's clear IS authoritative daemon state, so it must
    // unblock persistence (and cancel the retry), or every later annotation
    // is silently never saved for the life of the tile.
    const calls = { get: 0, save: [] as SaveCall[] };
    const transport: MarkdownAnnotationsTransport = {
      getMarkdownAnnotations: async () => {
        calls.get += 1;
        throw new Error('daemon down');
      },
      saveMarkdownAnnotations: async (path, annotations, generation) => {
        calls.save.push({ path, annotations, generation });
        return { stale: false };
      },
      clearMarkdownAnnotations: async (_path, generation) => ({ generation }),
      submitMarkdownAnnotations: async () => ({ status: 'delivered' }),
    };
    const { container, apiRef } = await mount(transport);
    expect(calls.get).toBe(1);
    expect(apiRef.current!.isHydrated()).toBe(false);

    // Annotating still works locally, but the save is suppressed (unhydrated).
    act(() => {
      apiRef.current!.handleSelectionChange(selectNeedle(container, 'target words'));
      apiRef.current!.submitComment('pre-send note');
    });
    await advanceDebounce();
    expect(calls.save).toHaveLength(0);

    act(() => {
      apiRef.current!.applyDeliveredClear(5);
    });
    expect(apiRef.current!.isHydrated()).toBe(true);

    // The failed-hydrate retry was cancelled: no further get fires.
    await act(async () => {
      vi.advanceTimersByTime(ANNOTATION_HYDRATE_RETRY_MS * 2);
    });
    await flush();
    expect(calls.get).toBe(1);

    // Persistence is unblocked and counts up from the delivered floor.
    act(() => {
      apiRef.current!.handleSelectionChange(selectNeedle(container, 'plain prose'));
      apiRef.current!.submitComment('post-send note');
    });
    await advanceDebounce();
    expect(calls.save).toHaveLength(1);
    expect(calls.save[0].generation).toBe(6);
  });

  it('works local-only with a null transport (no daemon socket)', async () => {
    const { container, apiRef } = await mount(null);
    const api = apiRef.current!;
    act(() => {
      api.handleSelectionChange(selectNeedle(container, 'target words'));
      api.submitComment('local note');
    });
    await advanceDebounce();
    expect(apiRef.current!.annotations).toHaveLength(1);
    expect(container.querySelector(`[data-md-mark="${apiRef.current!.annotations[0].id}"]`)).not.toBeNull();
  });
});
