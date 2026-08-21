import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import { ConversationPane } from './index';
import { Markdown } from '../Markdown';
import { DaemonApiProvider, type DaemonApi } from '../../contexts/DaemonApiContext';
import { useConversationsStore } from '../../store/conversations';
import mdTour from './__recordings__/md-tour.jsonl?raw';
import mdLong from './__recordings__/md-long.jsonl?raw';

// These tests are about STRUCTURE — what the parser made of the text — so the
// lazy shiki import is intercepted with a mock that never answers: no highlight
// state is ever written, and a replay of 1,364 deltas neither pays for
// highlighting nor resolves promises outside act(). Markdown.highlight.test.tsx
// is where highlighting itself is tested.
const shikiMock = vi.hoisted(() => ({
  codeToHtml: vi.fn(() => new Promise<string>(() => {})),
}));
vi.mock('shiki', () => shikiMock);

/**
 * The pane, driven by envelope streams RECORDED FROM REAL nisse SESSIONS.
 *
 * `__recordings__/*.jsonl` were captured off the real host's fd 3 against a
 * real model (2026-08-19): md-tour is 7,845 chars over 317 deltas in 13.8 s,
 * md-long 27,540 chars over 1,364 deltas in 65 s. Replaying them is the whole
 * point — an invented lorem stream has none of the half-open constructs that
 * make streaming markdown hard.
 */

const SESSION = 'sess-md';

// Each test below replays a whole recording delta by delta — 317 or 1,364 of
// them through the real pane. Vitest's 5s default is a fixture-size limit here,
// and one these clear alone but not beside 278 other files on a loaded machine.
const REPLAY_TIMEOUT_MS = 60_000;

interface Recorded { at: number; envelope: { seq: number; kind: string; body: Record<string, unknown> } }

const RECORDINGS: Record<string, string> = { 'md-tour': mdTour, 'md-long': mdLong };

function recording(name: string): Recorded[] {
  return RECORDINGS[name].trim().split('\n').map((line: string) => JSON.parse(line) as Recorded);
}

function renderPane() {
  const api = {
    sendAgentPrompt: vi.fn(),
    sendAgentToolDetail: vi.fn(),
    sendAgentClearQueue: vi.fn(),
    sendAgentAttach: vi.fn(),
    sendAgentHistory: vi.fn(),
    sendAgentSetModel: vi.fn(),
  } as unknown as DaemonApi;
  render(
    <DaemonApiProvider api={api}>
      <ConversationPane sessionId={SESSION} paneActive sessionState="idle" />
    </DaemonApiProvider>,
  );
}

function apply(row: Recorded) {
  act(() => {
    useConversationsStore.getState().applyEnvelope(SESSION, row.envelope.seq, row.envelope.kind, row.envelope.body);
  });
}

function messageNode(id: string): HTMLElement {
  return screen.getByTestId(`conversation-message-${id}`);
}

/** The rendered markdown of a message, with the role label stripped. */
function markdownHtml(id: string): string {
  return messageNode(id).querySelector('.conversation-message-text')?.innerHTML ?? '';
}

beforeEach(() => {
  useConversationsStore.setState({ conversations: {} });
});

describe('ConversationPane markdown, replayed from real recordings', () => {
  it.each(['md-tour', 'md-long'])(
    '%s: the streamed DOM lands exactly on the DOM of the settled text',
    (name) => {
      const rows = recording(name);
      renderPane();
      for (const row of rows) apply(row);

      const last = [...rows].reverse().find((row) => row.envelope.kind === 'message_end');
      const id = String(last!.envelope.body.id);
      const streamed = markdownHtml(id);

      // Same message, rendered once from its final text: what a reload shows.
      useConversationsStore.setState({ conversations: {} });
      const snapshot = rows.find((row) => row.envelope.kind === 'conversation_snapshot')!;
      apply({
        at: 0,
        envelope: {
          seq: 1,
          kind: 'conversation_snapshot',
          body: {
            ...snapshot.envelope.body,
            items: [{ kind: 'message', id, role: 'assistant', text: last!.envelope.body.text, streaming: false }],
          },
        },
      });
      expect(markdownHtml(id)).toBe(streamed);
    },
    REPLAY_TIMEOUT_MS,
  );

  it('renders structure, not the markdown source', { timeout: REPLAY_TIMEOUT_MS }, () => {
    const rows = recording('md-tour');
    renderPane();
    for (const row of rows) apply(row);
    const id = String([...rows].reverse().find((r) => r.envelope.kind === 'message_end')!.envelope.body.id);
    const node = messageNode(id);
    expect(node.querySelectorAll('h2, h3').length).toBeGreaterThan(0);
    expect(node.querySelectorAll('table').length).toBeGreaterThan(0);
    expect(node.querySelectorAll('pre code').length).toBeGreaterThan(0);
    expect(node.querySelectorAll('a[href]').length).toBeGreaterThan(0);
    expect(node.querySelectorAll('ul li, ol li').length).toBeGreaterThan(0);
    // Nothing that should have become structure is left standing as text.
    const visible = node.textContent ?? '';
    expect(visible).not.toContain('```');
    expect(visible).not.toContain('](');
  });

  it('never shows raw markdown syntax mid-stream', { timeout: REPLAY_TIMEOUT_MS }, () => {
    const rows = recording('md-tour');
    renderPane();
    const leaks: string[] = [];
    for (const row of rows) {
      apply(row);
      if (row.envelope.kind !== 'message_delta') continue;
      const node = document.querySelector(`[data-testid="conversation-message-${row.envelope.body.id}"]`);
      if (!node) continue;
      // Text nodes only: a backtick inside <code> is content, not syntax.
      const clone = node.cloneNode(true) as HTMLElement;
      clone.querySelectorAll('code, pre').forEach((element) => element.remove());
      const visible = clone.textContent ?? '';
      for (const [label, pattern] of [
        ['fence', /```/],
        ['table row', /\|[^\n|]*\|/],
        ['strong', /\*\*\S/],
        ['link', /\]\(/],
        ['code span', /`/],
      ] as const) {
        if (pattern.test(visible)) leaks.push(`${label} @seq ${row.envelope.seq}`);
      }
    }
    expect(leaks).toEqual([]);
  });

  it('holds a diagram until its fence closes', { timeout: REPLAY_TIMEOUT_MS }, () => {
    renderPane();
    apply({ at: 0, envelope: { seq: 1, kind: 'message_start', body: { id: 'd1', role: 'assistant' } } });
    apply({ at: 1, envelope: { seq: 2, kind: 'message_delta', body: { id: 'd1', text: '```mermaid\nflowchart TD\n  A --> ' } } });
    expect(screen.getByTestId('markdown-diagram-pending')).toBeTruthy();
    expect(document.querySelector('.markdown-mermaid')).toBeNull();

    apply({ at: 2, envelope: { seq: 3, kind: 'message_delta', body: { id: 'd1', text: 'B\n```\n' } } });
    expect(screen.queryByTestId('markdown-diagram-pending')).toBeNull();
    // MermaidDiagram draws the source while it loads mermaid, then swaps in the
    // SVG; either element means the diagram renderer has the block now.
    expect(document.querySelector('.markdown-mermaid-loading, .markdown-mermaid')).not.toBeNull();
  });

  it('reuse-as-is baseline: what the shared component leaks without the tail pass', { timeout: REPLAY_TIMEOUT_MS }, () => {
    // The same recording rendered the way a straight reuse would: no streaming
    // flag, so every prefix is parsed verbatim. This is the defect the tail
    // completion exists to remove, kept as a receipt rather than a claim.
    const rows = recording('md-tour');
    const leaks = new Map<string, number>();
    let text = '';
    const { rerender } = render(<Markdown>{''}</Markdown>);
    let deltas = 0;
    for (const row of rows) {
      if (row.envelope.kind !== 'message_delta') continue;
      text += String(row.envelope.body.text);
      deltas++;
      act(() => { rerender(<Markdown>{text}</Markdown>); });
      const clone = document.body.cloneNode(true) as HTMLElement;
      clone.querySelectorAll('code, pre').forEach((element) => element.remove());
      const visible = clone.textContent ?? '';
      for (const [label, pattern] of [
        ['table row', /\|[^\n|]*\|/],
        ['strong', /\*\*\S/],
        ['code span', /`/],
      ] as const) {
        if (pattern.test(visible)) leaks.set(label, (leaks.get(label) ?? 0) + 1);
      }
    }
    console.log(`[md-tour, no streaming pass] ${deltas} deltas: ${[...leaks].map(([k, v]) => `${k} ${v}`).join(', ') || 'none'}`);
    expect(leaks.size).toBeGreaterThan(0);
  });

  it('per-delta render cost, replaying md-long at its recorded sizes', { timeout: REPLAY_TIMEOUT_MS }, () => {
    const rows = recording('md-long');
    renderPane();
    const samples: Array<{ chars: number; ms: number }> = [];
    let chars = 0;
    for (const row of rows) {
      if (row.envelope.kind === 'message_delta') chars += String(row.envelope.body.text).length;
      const started = performance.now();
      apply(row);
      if (row.envelope.kind === 'message_delta') samples.push({ chars, ms: performance.now() - started });
    }
    const sorted = samples.map((s) => s.ms).sort((a, b) => a - b);
    const at = (p: number) => sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * p))];
    const big = samples.filter((s) => s.chars > 20_000).map((s) => s.ms).sort((a, b) => a - b);
    // happy-dom is not a browser and this number is not a frame budget — it is
    // the shape of the curve, reported so a regression is visible.
    console.log(
      `[md-long] ${samples.length} deltas, ${chars} chars: `
      + `p50 ${at(0.5).toFixed(2)}ms p90 ${at(0.9).toFixed(2)}ms p99 ${at(0.99).toFixed(2)}ms max ${at(1).toFixed(2)}ms`
      + (big.length ? ` | above 20k chars p50 ${big[Math.floor(big.length / 2)].toFixed(2)}ms` : ''),
    );
    // A tripwire, not a target: nothing healthy takes a third of a second to
    // absorb one delta, and a re-parse regression would blow straight past it.
    expect(at(0.99)).toBeLessThan(300);
  });
});
