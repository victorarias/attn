import { render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { AppErrorBoundary } from './AppErrorBoundary';

function Broken(): ReactNode {
  throw new Error('render exploded');
}

describe('AppErrorBoundary', () => {
  it('keeps a visible diagnostic fallback when React rendering fails', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    render(<AppErrorBoundary><Broken /></AppErrorBoundary>);

    expect(screen.getByRole('alert')).toHaveTextContent('attn hit a UI error');
    expect(screen.getByRole('alert')).toHaveTextContent('render exploded');
  });
});
