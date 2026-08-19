import type { ReactElement } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MarkdownBoundary } from './MarkdownBoundary';

function Explodes(): ReactElement {
  throw new Error('bad markdown');
}

describe('MarkdownBoundary', () => {
  it('shows the fallback instead of taking the surface down', () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});
    render(
      <MarkdownBoundary fallback={<pre data-testid="raw">the words as they arrived</pre>}>
        <Explodes />
      </MarkdownBoundary>,
    );
    expect(screen.getByTestId('raw').textContent).toBe('the words as they arrived');
    error.mockRestore();
  });

  it('re-arms when the next delta brings different text', () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});
    const { rerender } = render(
      <MarkdownBoundary fallback={<pre data-testid="raw">raw</pre>}><Explodes /></MarkdownBoundary>,
    );
    expect(screen.getByTestId('raw')).toBeTruthy();
    rerender(
      <MarkdownBoundary fallback={<pre data-testid="raw">raw</pre>}><p data-testid="ok">fine now</p></MarkdownBoundary>,
    );
    expect(screen.getByTestId('ok').textContent).toBe('fine now');
    error.mockRestore();
  });
});
