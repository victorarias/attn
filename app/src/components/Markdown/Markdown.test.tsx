import { useState } from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Markdown } from './index';

// jsdom cannot run real mermaid (it needs a canvas/layout engine), so the
// diagram-rendering path is mocked here; the mermaid renderer itself is
// exercised manually / by the Playwright harness.
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

describe('Markdown', () => {
  it('renders plain markdown including a GFM table', () => {
    render(
      <Markdown>{`# Title\n\n| a | b |\n| - | - |\n| 1 | 2 |\n`}</Markdown>
    );

    expect(screen.getByRole('heading', { name: 'Title' })).toBeInTheDocument();
    expect(screen.getByRole('table')).toBeInTheDocument();
    expect(screen.getByText('1')).toBeInTheDocument();
  });

  it('renders a non-mermaid code fence as a normal code element', () => {
    render(<Markdown>{'```ts\nconst x = 1;\n```'}</Markdown>);

    const code = screen.getByText('const x = 1;');
    expect(code.tagName).toBe('CODE');
    expect(code.className).toContain('language-ts');
    expect(screen.queryByTestId('mermaid-svg')).not.toBeInTheDocument();
  });

  it('leaves inline code unaffected', () => {
    render(<Markdown>{'some `inline` code'}</Markdown>);

    const code = screen.getByText('inline');
    expect(code.tagName).toBe('CODE');
    expect(code.className).toBe('');
  });

  it('renders a mermaid fence as the mocked diagram svg', async () => {
    render(<Markdown>{'```mermaid\ngraph TD;\nA-->B;\n```'}</Markdown>);

    await waitFor(() => {
      expect(screen.getByTestId('mermaid-svg')).toBeInTheDocument();
    });
    expect(mermaidMock.render).toHaveBeenCalled();
  });

  it('falls back to the raw code on a mermaid render error, without crashing', async () => {
    mermaidMock.render.mockRejectedValueOnce(new Error('boom'));

    render(<Markdown>{'```mermaid\nnot valid\n```'}</Markdown>);

    await waitFor(() => {
      expect(screen.getByText(/Diagram failed to render/)).toBeInTheDocument();
    });
    expect(screen.getByText('not valid')).toBeInTheDocument();
  });

  it('applies a caller components override', () => {
    render(
      <Markdown components={{ a: (props) => <a {...props} data-testid="custom-link" /> }}>
        {'[link](https://example.com)'}
      </Markdown>
    );

    expect(screen.getByTestId('custom-link')).toBeInTheDocument();
  });

  it('renders a single newline as a <br> when breaks is set', () => {
    const { container } = render(<Markdown breaks>{'a\nb'}</Markdown>);

    expect(container.querySelector('br')).toBeInTheDocument();
    expect(container.textContent).toContain('a');
    expect(container.textContent).toContain('b');
  });

  it('collapses a single newline into a space by default (no breaks)', () => {
    const { container } = render(<Markdown>{'a\nb'}</Markdown>);

    expect(container.querySelector('br')).not.toBeInTheDocument();
  });

  it('fires onDiagramLayoutChange once when a diagram finishes rendering', async () => {
    const onDiagramLayoutChange = vi.fn();
    render(
      <Markdown onDiagramLayoutChange={onDiagramLayoutChange}>
        {'```mermaid\ngraph TD;\nA-->B;\n```'}
      </Markdown>
    );

    await waitFor(() => {
      expect(screen.getByTestId('mermaid-svg')).toBeInTheDocument();
    });
    expect(onDiagramLayoutChange).toHaveBeenCalledTimes(1);
  });

  it('fires onDiagramLayoutChange once on a mermaid render error', async () => {
    mermaidMock.render.mockRejectedValueOnce(new Error('boom'));
    const onDiagramLayoutChange = vi.fn();

    render(
      <Markdown onDiagramLayoutChange={onDiagramLayoutChange}>
        {'```mermaid\nnot valid\n```'}
      </Markdown>
    );

    await waitFor(() => {
      expect(screen.getByText(/Diagram failed to render/)).toBeInTheDocument();
    });
    expect(onDiagramLayoutChange).toHaveBeenCalledTimes(1);
  });

  it('does not re-fire onDiagramLayoutChange on an unrelated parent re-render', async () => {
    const onDiagramLayoutChange = vi.fn();
    // Simulates PresentTour re-rendering Markdown with a fresh callback
    // identity after a version-bump-driven re-render — the callback identity
    // churns, but the diagram itself is not remounted and does not re-settle.
    function Harness() {
      const [tick, setTick] = useState(0);
      return (
        <div>
          <button onClick={() => setTick((t) => t + 1)}>bump</button>
          <span data-testid="tick">{tick}</span>
          <Markdown onDiagramLayoutChange={() => onDiagramLayoutChange(tick)}>
            {'```mermaid\ngraph TD;\nA-->B;\n```'}
          </Markdown>
        </div>
      );
    }

    render(<Harness />);

    await waitFor(() => {
      expect(screen.getByTestId('mermaid-svg')).toBeInTheDocument();
    });
    expect(onDiagramLayoutChange).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByText('bump'));
    expect(screen.getByTestId('tick').textContent).toBe('1');
    // The mermaid diagram already settled and the parent re-render carries no
    // new content, so no additional layout-change notification should fire.
    expect(onDiagramLayoutChange).toHaveBeenCalledTimes(1);
  });
});
