/**
 * domRange walk logic — pure DOM tests (happy-dom): boundary placement,
 * split-text-node tolerance, chrome skipping, UTF-16 offsets, null on
 * DOM/text-model disagreement.
 */

import { describe, expect, it } from 'vitest';
import { blockDomText, resolveDomRange } from './domRange';

function block(html: string): HTMLElement {
  const el = document.createElement('div');
  el.setAttribute('data-block-id', 'b0-paragraph');
  el.innerHTML = html;
  document.body.appendChild(el);
  return el;
}

describe('blockDomText', () => {
  it('concatenates all text nodes in tree order', () => {
    const el = block('hello <b>bold</b> world');
    expect(blockDomText(el)).toBe('hello bold world');
  });

  it('skips data-md-chrome subtrees entirely', () => {
    const el = block(
      '<span data-md-chrome="1">Note<svg></svg></span>alpha <em>beta<span data-md-chrome="1">x</span></em> gamma',
    );
    expect(blockDomText(el)).toBe('alpha beta gamma');
  });
});

describe('resolveDomRange', () => {
  it('resolves within a single text node', () => {
    const el = block('hello world');
    const range = resolveDomRange(el, 6, 11);
    expect(range).not.toBeNull();
    expect(range!.toString()).toBe('world');
  });

  it('spans element boundaries across multiple text nodes', () => {
    const el = block('hello <b>bold</b> world');
    const range = resolveDomRange(el, 3, 13);
    expect(range!.toString()).toBe('lo bold wo');
  });

  it('tolerates split text nodes (shiki-style spans)', () => {
    const el = block('');
    // Simulate a highlighter that split "const x = 1" into many nodes.
    for (const piece of ['con', 'st ', 'x', ' = ', '1']) {
      el.appendChild(document.createTextNode(piece));
    }
    const range = resolveDomRange(el, 6, 9);
    expect(range!.toString()).toBe('x =');
  });

  it('attaches a seam start to the LATER node at offset 0', () => {
    const el = block('abc<b>def</b>');
    const range = resolveDomRange(el, 3, 6);
    expect(range!.toString()).toBe('def');
    expect(range!.startContainer.textContent).toBe('def');
    expect(range!.startOffset).toBe(0);
  });

  it('attaches a seam end to the EARLIER node at its end', () => {
    const el = block('abc<b>def</b>');
    const range = resolveDomRange(el, 0, 3);
    expect(range!.toString()).toBe('abc');
    expect(range!.endContainer.textContent).toBe('abc');
    expect(range!.endOffset).toBe(3);
  });

  it('handles block start (0) and block end (length) boundaries', () => {
    const el = block('one <i>two</i> three');
    const text = blockDomText(el);
    const range = resolveDomRange(el, 0, text.length);
    expect(range!.toString()).toBe('one two three');
  });

  it('skips chrome text when accumulating offsets', () => {
    const el = block('<span data-md-chrome="1">CHROME</span>alpha <em>beta</em>');
    // Offsets are into the chrome-skipped text 'alpha beta'.
    const range = resolveDomRange(el, 6, 10);
    expect(range!.toString()).toBe('beta');
  });

  it('counts UTF-16 code units (surrogate pairs are 2 units)', () => {
    const el = block('👍👍 ok');
    // '👍👍 ' is 5 UTF-16 units.
    const range = resolveDomRange(el, 5, 7);
    expect(range!.toString()).toBe('ok');
  });

  it('returns null when the DOM text is shorter than end', () => {
    const el = block('short');
    expect(resolveDomRange(el, 0, 6)).toBeNull();
  });

  it('returns null for degenerate ranges', () => {
    const el = block('hello');
    expect(resolveDomRange(el, 2, 2)).toBeNull();
    expect(resolveDomRange(el, 3, 2)).toBeNull();
    expect(resolveDomRange(el, -1, 2)).toBeNull();
  });
});
