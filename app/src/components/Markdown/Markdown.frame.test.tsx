import { describe, expect, it, vi } from 'vitest';
import { render, waitFor } from '@testing-library/react';
import { Markdown, ReaderPresentation } from './index';

const shikiMock = vi.hoisted(() => ({
  codeToHtml: vi.fn(async (code: string) => `<span>${code}</span>`),
}));
vi.mock('shiki', () => shikiMock);

// Rendering a diagram for real costs a dynamic import whose async tail outlives
// the test that started it; only its shape matters here.
const mermaidMock = vi.hoisted(() => ({
  initialize: vi.fn(),
  render: vi.fn(async () => ({ svg: '<svg data-stub="diagram"></svg>' })),
}));
vi.mock('mermaid', () => ({ default: mermaidMock }));

const FENCE = '```ts\nconst answer = 42;\n```\n';

describe('block chrome', () => {
  it('frames a code fence with its language and a copy action', async () => {
    const { container, getByRole } = render(
      <ReaderPresentation><Markdown>{FENCE}</Markdown></ReaderPresentation>,
    );
    const frame = container.querySelector('.markdown-code-frame');
    expect(frame).not.toBeNull();
    expect(frame).toHaveAttribute('data-language', 'ts');
    expect(container.querySelector('.markdown-code-frame-language')?.textContent).toBe('ts');
    expect(getByRole('button', { name: 'Copy code' })).toBeInTheDocument();
    // The frame owns the edge; the pre it wraps is still the code itself.
    expect(frame?.querySelector('pre code')?.textContent).toContain('const answer = 42;');
  });

  /**
   * Ticket descriptions, comments and Present summaries render through the same
   * component. Reading chrome is opt-in precisely so a polish pass on the
   * transcript cannot restyle every markdown surface in the app.
   */
  it('leaves a fence bare on a static surface', async () => {
    const { container } = render(<Markdown>{FENCE}</Markdown>);
    await waitFor(() => {
      expect(container.querySelector('pre')).not.toBeNull();
    });
    expect(container.querySelector('.markdown-code-frame')).toBeNull();
  });

  it('does not frame a diagram as code', async () => {
    const { container } = render(
      <ReaderPresentation>
        <Markdown>{'```mermaid\nflowchart LR\n  A --> B\n```\n'}</Markdown>
      </ReaderPresentation>,
    );
    await waitFor(() => {
      expect(container.querySelector('.markdown-mermaid-frame')).not.toBeNull();
    });
    expect(container.querySelector('.markdown-code-frame')).toBeNull();
  });

  /**
   * mermaid's parse error quotes the line it choked on. Rendered as prose that
   * quote reads as markdown the agent wrote — the harness's criterion 1 caught
   * it as raw syntax reaching the reader — and the caret it points with lands
   * nowhere near the column it accuses.
   */
  it('reports a failed diagram as source, not as prose', async () => {
    mermaidMock.render.mockRejectedValueOnce(
      new Error('Parse error on line 5:\n...    E -->|[| C[CSI Entry]\n-------------^\nExpecting NODE_STRING'),
    );
    const { container } = render(
      <ReaderPresentation>
        <Markdown>{'```mermaid\nflowchart LR\n  E -->|[| C\n```\n'}</Markdown>
      </ReaderPresentation>,
    );
    await waitFor(() => {
      expect(container.querySelector('.markdown-mermaid-error-detail')).not.toBeNull();
    });
    expect(container.querySelector('.markdown-mermaid-error-detail')?.tagName).toBe('PRE');
    // The reader's own test: what is left once every preformatted subtree is
    // taken away carries no markdown syntax.
    const prose = container.innerHTML
      .replace(/<pre[\s\S]*?<\/pre>/g, ' ')
      .replace(/<code[\s\S]*?<\/code>/g, ' ')
      .replace(/<[^>]+>/g, '\n');
    expect(prose).not.toMatch(/\|[^\n|]*\|/);
    expect(container.querySelector('.markdown-mermaid-frame--error')).not.toBeNull();
  });

  it('gives an ordinary diagram the same header an oversized one gets', async () => {
    const { container } = render(
      <ReaderPresentation>
        <Markdown>{'```mermaid\nflowchart LR\n  A --> B\n```\n'}</Markdown>
      </ReaderPresentation>,
    );
    await waitFor(() => {
      expect(container.querySelector('.markdown-mermaid-label')?.textContent).toBe('diagram');
    });
    // Nothing claims it is large, because in jsdom it has no measured size.
    expect(container.querySelector('.markdown-mermaid-status')).toBeNull();
  });
});
