/**
 * AnnotationLayer — the UI surfaces wired to the real engine over a real
 * rendered reader DOM:
 * - toolbar appears on a pending selection, Delete redlines instantly (E6),
 *   Escape/cancel unpaints the provisional highlight (E4);
 * - type-to-comment opens the seeded popover, Cmd+Enter creates the comment
 *   (E9→E10 flow);
 * - ⚡ opens the picker; a row click creates a structured quick-label (E17);
 * - global comments flow from the sidebar header button (E13);
 * - sidebar: collapsed rail at 0 annotations, auto-open on the first one,
 *   focus glow + just-created scroll skip (E19), clear-all tombstones (E21);
 * - code-block hover toolbar annotates the whole block (E27, jsdom half).
 */

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { useRef } from 'react';
import { MarkdownReader } from '../index';
import { AnnotationLayer } from './AnnotationLayer';
import type { SelectionLike } from './selection';
import type { MarkdownAnnotationsTransport } from './transport';
import type { WireAnnotation } from './types';
import { QUICK_LABELS } from './quickLabels';
import { useAnnotations, type UseAnnotationsApi } from './useAnnotations';

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
  '```js',
  'const a = 1;',
  '```',
  '',
].join('\n');

function makeTransport() {
  const calls = {
    save: [] as { annotations: WireAnnotation[]; generation: number }[],
    clear: [] as number[],
  };
  const transport: MarkdownAnnotationsTransport = {
    getMarkdownAnnotations: async () => ({ annotations: [], generation: 0 }),
    saveMarkdownAnnotations: async (_p, annotations, generation) => {
      calls.save.push({ annotations, generation });
      return { stale: false };
    },
    clearMarkdownAnnotations: async (_p, generation) => {
      calls.clear.push(generation);
      return { generation };
    },
    submitMarkdownAnnotations: async () => ({ status: 'delivered' }),
  };
  return { transport, calls };
}

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
  const api = useAnnotations({ rootRef, content, path, enabled: true, transport });
  apiRef.current = api;
  return (
    <div ref={rootRef}>
      <MarkdownReader content={content} path={path} allowLocalTargets />
      <AnnotationLayer api={api} rootRef={rootRef} path={path} />
    </div>
  );
}

async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

let pathSeq = 0;

async function mount(content = DOC) {
  const { transport, calls } = makeTransport();
  const apiRef: { current: UseAnnotationsApi | null } = { current: null };
  // Unique path per mount: the popover draft store is module-level and keyed
  // by path — tests must not leak drafts into each other.
  const path = `/tmp/project/layer-${pathSeq++}.md`;
  const view = render(<Harness content={content} path={path} transport={transport} apiRef={apiRef} />);
  await flush();
  return { ...view, apiRef, calls, path };
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

function beginSelection(view: Awaited<ReturnType<typeof mount>>, needle: string) {
  act(() => {
    view.apiRef.current!.handleSelectionChange(selectNeedle(view.container, needle));
  });
}

function toolbar(): HTMLElement | null {
  return document.querySelector<HTMLElement>('.md-selection-toolbar');
}

function popoverTextarea(): HTMLTextAreaElement {
  return document.querySelector<HTMLTextAreaElement>('.md-popover-textarea')!;
}

beforeEach(() => {
  vi.restoreAllMocks();
});

describe('AnnotationLayer', () => {
  it('shows the toolbar and provisional highlight for a pending prose selection (E1)', async () => {
    const view = await mount();
    expect(toolbar()).toBeNull();

    beginSelection(view, 'target words');
    expect(toolbar()).not.toBeNull();
    expect(toolbar()!.classList.contains('md-selection-toolbar--centered')).toBe(true);
    const pendingMark = view.container.querySelector('[data-md-mark="md-pending-selection"]');
    expect(pendingMark?.textContent).toBe('target words');
  });

  it('toolbar Delete creates a deletion instantly — no popover (E6)', async () => {
    const view = await mount();
    beginSelection(view, 'target words');
    fireEvent.click(screen.getByTitle('Delete'));

    const api = view.apiRef.current!;
    expect(api.annotations).toHaveLength(1);
    expect(api.annotations[0].type).toBe('deletion');
    expect(api.annotations[0].anchor?.exact).toBe('target words');
    expect(document.querySelector('.md-annotation-popover')).toBeNull();
    expect(toolbar()).toBeNull(); // pending consumed, toolbar gone
    expect(view.container.querySelector('[data-md-mark="md-pending-selection"]')).toBeNull();
  });

  it('Escape closes the toolbar and unpaints the provisional highlight (E4)', async () => {
    const view = await mount();
    beginSelection(view, 'target words');
    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    });
    expect(toolbar()).toBeNull();
    expect(view.container.querySelector('[data-md-mark="md-pending-selection"]')).toBeNull();
    expect(view.apiRef.current!.pending).toBeNull();
  });

  it('type-to-comment opens the popover seeded with the char; Cmd+Enter creates the comment (E9→E10)', async () => {
    const view = await mount();
    beginSelection(view, 'target words');
    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'h', bubbles: true }));
    });

    expect(toolbar()).toBeNull(); // popover replaces the toolbar
    const textarea = popoverTextarea();
    expect(textarea.value).toBe('h');

    fireEvent.change(textarea, { target: { value: 'hmm, rephrase this' } });
    fireEvent.keyDown(textarea, { key: 'Enter', metaKey: true });

    const api = view.apiRef.current!;
    expect(api.annotations).toHaveLength(1);
    expect(api.annotations[0]).toMatchObject({ type: 'comment', text: 'hmm, rephrase this' });
    expect(api.annotations[0].anchor?.exact).toBe('target words');
    expect(document.querySelector('.md-annotation-popover')).toBeNull();
  });

  it('a document click while composing does not strand the popover (pending survives)', async () => {
    const view = await mount();
    beginSelection(view, 'target words');
    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'x', bubbles: true }));
    });
    fireEvent.change(popoverTextarea(), { target: { value: 'x plus more' } });

    // A stray mouseup in the document (collapsed selection) while composing.
    const prose = findTextNode(view.container, 'plain prose').node.parentElement!;
    act(() => {
      prose.dispatchEvent(new Event('mouseup', { bubbles: true }));
    });
    expect(document.querySelector('.md-annotation-popover')).not.toBeNull();
    expect(view.apiRef.current!.pending).not.toBeNull();

    fireEvent.keyDown(popoverTextarea(), { key: 'Enter', metaKey: true });
    expect(view.apiRef.current!.annotations).toHaveLength(1);
  });

  it('⚡ opens the picker; a row click creates a structured quick-label (E14/E17)', async () => {
    const view = await mount();
    beginSelection(view, 'target words');
    fireEvent.click(screen.getByTitle('Quick label'));
    expect(document.querySelector('.md-quick-label-picker')).not.toBeNull();

    const label = QUICK_LABELS.find((l) => l.tip !== undefined)!;
    fireEvent.click(screen.getByText(label.text));

    const api = view.apiRef.current!;
    expect(api.annotations).toHaveLength(1);
    expect(api.annotations[0]).toMatchObject({
      type: 'comment',
      quickLabelId: label.id,
      quickLabelTip: label.tip,
    });
    expect(api.annotations[0].text).toBeUndefined(); // never "emoji label" text
    expect(document.querySelector('.md-quick-label-picker')).toBeNull();
  });

  it('sidebar: collapsed rail at 0 annotations, auto-opens on the first one', async () => {
    const view = await mount();
    expect(document.querySelector('.md-annotations-sidebar')).toBeNull();
    expect(document.querySelector('.md-sidebar-rail')).not.toBeNull();

    beginSelection(view, 'target words');
    fireEvent.click(screen.getByTitle('Delete'));

    expect(document.querySelector('.md-annotations-sidebar')).not.toBeNull();
    expect(document.querySelector('.md-sidebar-rail')).toBeNull();
    expect(document.querySelector('.md-sidebar-count')!.textContent).toBe('1');

    // Collapse via the count pill; the rail returns with the count.
    fireEvent.click(screen.getByTitle('Collapse annotations sidebar'));
    expect(document.querySelector('.md-annotations-sidebar')).toBeNull();
    expect(document.querySelector('.md-sidebar-rail-count')!.textContent).toBe('1');
  });

  it('global comment flows from the sidebar header button (E13)', async () => {
    const view = await mount();
    beginSelection(view, 'target words');
    fireEvent.click(screen.getByTitle('Delete')); // sidebar now open

    fireEvent.click(screen.getByTitle('Add a document-wide comment'));
    expect(screen.getByText('Global Comment')).not.toBeNull();
    fireEvent.change(popoverTextarea(), { target: { value: 'overall: tighten scope' } });
    fireEvent.keyDown(popoverTextarea(), { key: 'Enter', ctrlKey: true });

    const api = view.apiRef.current!;
    const global = api.annotations.find((a) => a.type === 'global')!;
    expect(global.text).toBe('overall: tighten scope');
    expect(global.anchor).toBeUndefined();
  });

  it('clear-all tombstones via the daemon clear command (E21)', async () => {
    const view = await mount();
    beginSelection(view, 'target words');
    fireEvent.click(screen.getByTitle('Delete'));
    expect(view.apiRef.current!.annotations).toHaveLength(1);

    fireEvent.click(screen.getByText('Clear all'));
    fireEvent.click(screen.getByText('Confirm?'));
    await flush();

    expect(view.apiRef.current!.annotations).toHaveLength(0);
    expect(view.calls.clear.length).toBeGreaterThan(0);
    expect(view.calls.save).toHaveLength(0); // tombstoned, never save-[]
    expect(view.container.querySelector('[data-md-mark]')).toBeNull();
  });

  it('sidebar card click paints the focus glow; just-created skips the scroll (E19)', async () => {
    const scrollSpy = vi.fn();
    HTMLElement.prototype.scrollIntoView = scrollSpy;
    const view = await mount();
    beginSelection(view, 'target words');
    fireEvent.click(screen.getByTitle('Delete'));

    // First click right after creation: glow, no scroll.
    fireEvent.click(document.querySelector('.md-annotation-card')!);
    expect(document.querySelector('[data-md-mark="md-focus-glow"]')).not.toBeNull();
    expect(scrollSpy).not.toHaveBeenCalled();

    // Second click: the just-created latch has been consumed — scroll happens.
    fireEvent.click(document.querySelector('.md-annotation-card')!);
    expect(scrollSpy).toHaveBeenCalledTimes(1);
    expect(view.apiRef.current!.selectedId).toBe(view.apiRef.current!.annotations[0].id);
  });

  it('hovering a code block shows the top-right toolbar; Delete annotates the whole block (E27)', async () => {
    const view = await mount();
    const pre = view.container.querySelector('.md-codeblock pre')!;
    act(() => {
      pre.dispatchEvent(new Event('pointerover', { bubbles: true }));
    });

    const bar = toolbar()!;
    expect(bar).not.toBeNull();
    expect(bar.classList.contains('md-selection-toolbar--centered')).toBe(false); // top-right

    fireEvent.click(screen.getByTitle('Delete'));
    const api = view.apiRef.current!;
    expect(api.annotations).toHaveLength(1);
    expect(api.annotations[0].type).toBe('deletion');
    expect(api.annotations[0].anchor?.exact).toBe('const a = 1;');
    expect(api.annotations[0].anchor?.start).toBe(0);
  });

  it('in-code text selection still creates a normal ranged annotation (E27)', async () => {
    const view = await mount();
    beginSelection(view, 'const a');
    expect(view.apiRef.current!.pending?.isCodeBlock).toBe(true);
    const bar = toolbar()!;
    expect(bar.classList.contains('md-selection-toolbar--centered')).toBe(false); // top-right mode

    fireEvent.click(screen.getByTitle('Delete'));
    expect(view.apiRef.current!.annotations[0].anchor?.exact).toBe('const a');
  });
});
