/**
 * Paint layer tests. happy-dom has no Custom Highlight API — which is exactly
 * why MarkPainter (the fallback) is the DOM-testable one; the
 * CustomHighlightPainter is tested against a stubbed CSS.highlights registry
 * asserting set contents.
 */

import { afterEach, describe, expect, it, vi } from 'vitest';
import { resolveDomRange } from './domRange';
import {
  __resetCustomHighlightPaintersForTests,
  createHighlightPainter,
  CustomHighlightPainter,
  MarkPainter,
  supportsCustomHighlights,
} from './painter';

function container(html: string): HTMLElement {
  const el = document.createElement('div');
  el.innerHTML = html;
  document.body.appendChild(el);
  return el;
}

afterEach(() => {
  __resetCustomHighlightPaintersForTests();
  vi.unstubAllGlobals();
  document.body.innerHTML = '';
});

class FakeHighlight {
  ranges: Range[];
  constructor(...ranges: Range[]) {
    this.ranges = ranges;
  }
}

function stubHighlightRegistry(): Map<string, FakeHighlight> {
  const registry = new Map<string, FakeHighlight>();
  vi.stubGlobal('Highlight', FakeHighlight);
  vi.stubGlobal('CSS', { highlights: registry });
  return registry;
}

describe('feature detection', () => {
  it('falls back to MarkPainter without CSS.highlights (happy-dom)', () => {
    expect(supportsCustomHighlights()).toBe(false);
    expect(createHighlightPainter(container('x')).mode).toBe('mark');
  });

  it('prefers CustomHighlightPainter when CSS.highlights exists', () => {
    stubHighlightRegistry();
    expect(supportsCustomHighlights()).toBe(true);
    expect(createHighlightPainter(container('x')).mode).toBe('custom-highlight');
  });
});

describe('MarkPainter', () => {
  it('paints a multi-node range without changing rendered text', () => {
    const el = container('<p>hello <b>bold</b> world</p>');
    const painter = new MarkPainter(el);
    // Rendered text: 'hello bold world'; cover 'lo bold wo'.
    const range = resolveDomRange(el, 3, 13);
    painter.paint('a1', range!, 'comment');

    const spans = [...el.querySelectorAll('[data-md-mark="a1"]')];
    expect(spans.length).toBeGreaterThan(1); // one wrap per covered segment
    expect(spans.every((s) => s.className === 'md-mark md-mark-comment')).toBe(true);
    expect(spans.map((s) => s.textContent).join('')).toBe('lo bold wo');
    expect(el.textContent).toBe('hello bold world');
  });

  it('uses the kind-specific class for deletions', () => {
    const el = container('<p>strike this</p>');
    const painter = new MarkPainter(el);
    painter.paint('d1', resolveDomRange(el, 7, 11)!, 'deletion');
    expect(el.querySelector('[data-md-mark="d1"]')!.className).toBe('md-mark md-mark-deletion');
  });

  it('clear() restores a normalize()d DOM identical to the original', () => {
    const el = container('<p>hello <b>bold</b> world</p>');
    const original = el.innerHTML;
    const painter = new MarkPainter(el);
    painter.paint('a1', resolveDomRange(el, 3, 13)!, 'comment');
    expect(el.innerHTML).not.toBe(original);

    painter.clear('a1');
    expect(el.innerHTML).toBe(original);
    // Text nodes merged back: the <p> has exactly its original 3 children.
    expect(el.querySelector('p')!.childNodes.length).toBe(3);
  });

  it('repainting an existing id replaces its previous spans', () => {
    const el = container('<p>alpha beta gamma</p>');
    const painter = new MarkPainter(el);
    painter.paint('a1', resolveDomRange(el, 0, 5)!, 'comment');
    painter.paint('a1', resolveDomRange(el, 6, 10)!, 'comment');
    const spans = [...el.querySelectorAll('[data-md-mark="a1"]')];
    expect(spans.map((s) => s.textContent).join('')).toBe('beta');
  });

  it('never wraps chrome text when a range passes through inline chrome', () => {
    const el = container(
      '<p>before <span data-md-chrome="1">[blocked image: x]</span> after</p>',
    );
    const painter = new MarkPainter(el);
    // Anchor text-space (chrome skipped): 'before  after'; cover 'ore  af'.
    const range = resolveDomRange(el, 3, 10);
    painter.paint('a1', range!, 'comment');

    const chrome = el.querySelector('[data-md-chrome]')!;
    expect(chrome.querySelector('[data-md-mark]')).toBeNull();
    expect(chrome.textContent).toBe('[blocked image: x]');
    const spans = [...el.querySelectorAll('[data-md-mark="a1"]')];
    expect(spans.map((s) => s.textContent).join('')).toBe('ore  af');
  });

  it('clearAll() removes every mark and restores the DOM', () => {
    const el = container('<p>one two three</p>');
    const original = el.innerHTML;
    const painter = new MarkPainter(el);
    painter.paint('a1', resolveDomRange(el, 0, 3)!, 'comment');
    painter.paint('a2', resolveDomRange(el, 8, 13)!, 'deletion');
    painter.clearAll();
    expect(el.innerHTML).toBe(original);
  });
});

describe('CustomHighlightPainter (stubbed registry)', () => {
  it('maintains one registry entry per kind, rebuilt on every mutation', () => {
    const registry = stubHighlightRegistry();
    const el = container('<p>alpha beta gamma</p>');
    const painter = new CustomHighlightPainter();

    painter.paint('a1', resolveDomRange(el, 0, 5)!, 'comment');
    painter.paint('a2', resolveDomRange(el, 6, 10)!, 'comment');
    painter.paint('d1', resolveDomRange(el, 11, 16)!, 'deletion');

    expect(registry.get('attn-md-comment')!.ranges).toHaveLength(2);
    expect(registry.get('attn-md-deletion')!.ranges).toHaveLength(1);
    expect(registry.get('attn-md-deletion')!.ranges[0].toString()).toBe('gamma');
  });

  it('clear(id) drops the range; the emptied kind leaves the registry', () => {
    const registry = stubHighlightRegistry();
    const el = container('<p>alpha beta</p>');
    const painter = new CustomHighlightPainter();
    painter.paint('a1', resolveDomRange(el, 0, 5)!, 'comment');
    painter.paint('a2', resolveDomRange(el, 6, 10)!, 'comment');

    painter.clear('a1');
    expect(registry.get('attn-md-comment')!.ranges).toHaveLength(1);
    painter.clear('a2');
    expect(registry.has('attn-md-comment')).toBe(false);
  });

  it('repainting an id replaces its range instead of accumulating', () => {
    const registry = stubHighlightRegistry();
    const el = container('<p>alpha beta</p>');
    const painter = new CustomHighlightPainter();
    painter.paint('a1', resolveDomRange(el, 0, 5)!, 'comment');
    painter.paint('a1', resolveDomRange(el, 6, 10)!, 'comment');
    expect(registry.get('attn-md-comment')!.ranges).toHaveLength(1);
    expect(registry.get('attn-md-comment')!.ranges[0].toString()).toBe('beta');
  });

  it('clearAll() empties both registry entries', () => {
    const registry = stubHighlightRegistry();
    const el = container('<p>alpha beta</p>');
    const painter = new CustomHighlightPainter();
    painter.paint('a1', resolveDomRange(el, 0, 5)!, 'comment');
    painter.paint('d1', resolveDomRange(el, 6, 10)!, 'deletion');
    painter.clearAll();
    expect(registry.size).toBe(0);
  });

  it('unions ranges across instances: one tile repainting never clobbers another', () => {
    const registry = stubHighlightRegistry();
    const tileA = container('<p>alpha beta</p>');
    const tileB = container('<p>gamma delta</p>');
    const a = new CustomHighlightPainter();
    const b = new CustomHighlightPainter();

    // Same entry id in both tiles (spike marker keys collide across tiles).
    a.paint('comment:x#0', resolveDomRange(tileA, 0, 5)!, 'comment');
    b.paint('comment:x#0', resolveDomRange(tileB, 0, 5)!, 'comment');
    expect(registry.get('attn-md-comment')!.ranges).toHaveLength(2);

    // Tile B's live-reload path: clearAll + repaint. Tile A must survive both.
    b.clearAll();
    expect(registry.get('attn-md-comment')!.ranges).toHaveLength(1);
    expect(registry.get('attn-md-comment')!.ranges[0].toString()).toBe('alpha');
    b.paint('comment:x#0', resolveDomRange(tileB, 6, 11)!, 'comment');
    expect(
      registry.get('attn-md-comment')!.ranges.map((r) => r.toString()).sort(),
    ).toEqual(['alpha', 'delta']);

    a.clearAll();
    b.clearAll();
    expect(registry.size).toBe(0);
  });
});
