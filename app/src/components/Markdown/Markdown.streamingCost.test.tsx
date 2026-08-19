import { afterEach, describe, expect, it } from 'vitest';
import { act, cleanup, render } from '@testing-library/react';
import { Markdown } from './index';
import mdLong from '../ConversationPane/__recordings__/md-long.jsonl?raw';

/**
 * What the settled/tail split buys, measured on the real recording.
 *
 * `streaming` off is the naive baseline: react-markdown reparses the whole
 * message on every delta, which is what a straight reuse of the shared
 * component would do. The numbers are printed rather than asserted — happy-dom
 * is not a browser and its absolute cost means little — but the RATIO between
 * the two runs is the same work in the same place, and that is the receipt.
 */

function prefixes(): string[] {
  const rows = mdLong.trim().split('\n').map((line: string) => JSON.parse(line));
  const out: string[] = [];
  let text = '';
  for (const row of rows) {
    if (row.envelope.kind === 'message_start') text = '';
    if (row.envelope.kind === 'message_delta') { text += row.envelope.body.text; out.push(text); }
  }
  return out;
}

function replay(streaming: boolean, texts: string[]): number[] {
  const { rerender } = render(<Markdown streaming={streaming}>{texts[0]}</Markdown>);
  const samples: number[] = [];
  for (const text of texts.slice(1)) {
    const started = performance.now();
    act(() => { rerender(<Markdown streaming={streaming}>{text}</Markdown>); });
    samples.push(performance.now() - started);
  }
  return samples.sort((a, b) => a - b);
}

describe('streaming markdown cost', () => {
  afterEach(cleanup);

  // Two full replays of a 1,364-delta recording; the naive leg alone is ~12s.
  it('reparses only the open tail', { timeout: 120_000 }, () => {
    const texts = prefixes();
    const quantile = (s: number[], p: number) => s[Math.min(s.length - 1, Math.floor(s.length * p))];
    const report = (label: string, s: number[]) =>
      `${label} p50 ${quantile(s, 0.5).toFixed(2)}ms p90 ${quantile(s, 0.9).toFixed(2)}ms p99 ${quantile(s, 0.99).toFixed(2)}ms max ${quantile(s, 1).toFixed(2)}ms`;

    const naive = replay(false, texts);
    cleanup();
    const split = replay(true, texts);
    console.log(`[md-long ${texts.length} deltas → ${texts[texts.length - 1].length} chars]`);
    console.log(`  ${report('whole-document reparse:', naive)}`);
    console.log(`  ${report('settled/tail split:    ', split)}`);
    console.log(`  p50 ratio ${(quantile(naive, 0.5) / quantile(split, 0.5)).toFixed(1)}x`);

    expect(quantile(split, 0.5)).toBeLessThan(quantile(naive, 0.5));
  });
});
