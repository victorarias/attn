import { describe, expect, it } from 'vitest';
import {
  PENDING_DIAGRAM_LANGUAGE,
  prepareStreamingMarkdown,
  splitStreamingMarkdown,
} from './streaming';

describe('prepareStreamingMarkdown', () => {
  it('closes a code span that has content but no partner yet', () => {
    expect(prepareStreamingMarkdown('the range `0x1B')).toBe('the range `0x1B`');
  });

  it('holds a code-span opener that has nothing in it yet', () => {
    expect(prepareStreamingMarkdown('the range `')).toBe('the range ');
  });

  it('closes strong emphasis but not a lone asterisk', () => {
    expect(prepareStreamingMarkdown('mixed with **escape')).toBe('mixed with **escape**');
    expect(prepareStreamingMarkdown('2 * 3')).toBe('2 * 3');
  });

  it('does not treat markup inside a code span as an opener', () => {
    expect(prepareStreamingMarkdown('use `a ** b` and')).toBe('use `a ** b` and');
  });

  it('closes a link whose paren has not arrived', () => {
    expect(prepareStreamingMarkdown('see [VT100](https://vt100.net'))
      .toBe('see [VT100](https://vt100.net)');
  });

  it('gives a table header the delimiter row it is waiting for', () => {
    const prepared = prepareStreamingMarkdown('intro\n\n| State | Meaning |');
    expect(prepared).toBe('intro\n\n| State | Meaning |\n| --- | --- |');
  });

  it('widens a delimiter row still narrower than its header', () => {
    const prepared = prepareStreamingMarkdown('| a | b | c |\n|---|');
    expect(prepared).toBe('| a | b | c |\n| --- | --- | --- |');
  });

  it('leaves a delimiter row alone once it matches the header', () => {
    const text = '| a | b |\n| --- | --- |';
    expect(prepareStreamingMarkdown(text)).toBe(text);
  });

  it('holds a block marker that carries nothing yet', () => {
    expect(prepareStreamingMarkdown('done\n\n##')).toBe('done\n');
    expect(prepareStreamingMarkdown('done\n\n-')).toBe('done\n');
  });

  it('routes an unclosed mermaid fence away from the diagram renderer', () => {
    const prepared = prepareStreamingMarkdown('```mermaid\nflowchart TD\n  A -->');
    expect(prepared).toContain(`\`\`\`${PENDING_DIAGRAM_LANGUAGE}`);
    expect(prepared).not.toContain('```mermaid');
    expect(prepared.endsWith('```')).toBe(true);
  });

  it('leaves a closed mermaid fence as a diagram', () => {
    const text = '```mermaid\nflowchart TD\n  A --> B\n```';
    expect(prepareStreamingMarkdown(text)).toBe(text);
  });

  it('never touches text inside an open fence', () => {
    const prepared = prepareStreamingMarkdown('```ts\nconst a = `x` + **b');
    expect(prepared).toBe('```ts\nconst a = `x` + **b\n```');
  });

  it('only ever appends to what has arrived, or trims a contentless marker', () => {
    const text = 'a paragraph with `code` in it';
    expect(prepareStreamingMarkdown(text).startsWith(text)).toBe(true);
  });
});

describe('splitStreamingMarkdown', () => {
  it('cuts before a heading at the margin', () => {
    const { settled, tail } = splitStreamingMarkdown('# T\n\nIntro.\n\n## Next\n\nBody');
    expect(settled).toBe('# T\n\nIntro.');
    expect(tail).toBe('## Next\n\nBody');
  });

  it('cuts after a closed fence', () => {
    const { settled, tail } = splitStreamingMarkdown('Before\n\n```ts\nconst a = 1;\n```\n\nAfter');
    expect(settled).toBe('Before\n\n```ts\nconst a = 1;\n```');
    expect(tail).toBe('After');
  });

  it('never cuts inside an open fence', () => {
    const { settled } = splitStreamingMarkdown('```ts\n\n# not a heading\n\nstill code');
    expect(settled).toBe('');
  });

  it('never cuts a loose list apart', () => {
    const { settled } = splitStreamingMarkdown('- a\n\n- b\n\n- c');
    expect(settled).toBe('');
  });

  it('leaves a document with link reference definitions whole', () => {
    const { settled } = splitStreamingMarkdown('[ref]: https://x\n\n# T\n\nBody\n\n## N\n\nMore');
    expect(settled).toBe('');
  });

  it('rejoins to the original text', () => {
    const text = '# T\n\nIntro.\n\n## Next\n\nBody';
    const { settled, tail } = splitStreamingMarkdown(text);
    expect(`${settled}\n\n${tail}`).toBe(text);
  });
});
