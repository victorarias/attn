import { describe, it, expect, vi } from 'vitest';
import { useState } from 'react';
import { render, screen, fireEvent } from '../../test/utils';
import { Palette } from './Palette';

interface Row { path: string }

// A controlled host, mirroring how real callers drive the palette: the caller owns
// the query and the (already ranked) rows, the palette owns the interaction.
function Harness({
  rows,
  onPick = () => {},
  onClose = () => {},
  filter = true,
  onKeyDown,
}: {
  rows: string[];
  onPick?: (row: Row) => void;
  onClose?: () => void;
  filter?: boolean;
  onKeyDown?: (event: React.KeyboardEvent<HTMLInputElement>) => boolean;
}) {
  const [query, setQuery] = useState('');
  const items: Row[] = rows
    .filter((path) => (filter ? path.includes(query) : true))
    .map((path) => ({ path }));
  return (
    <Palette
      variant="test-palette"
      ariaLabel="Find a thing"
      placeholder="Find…"
      query={query}
      onQueryChange={setQuery}
      items={items}
      itemKey={(row) => row.path}
      renderItem={(row) => <span className="row">{row.path}</span>}
      emptyLabel="Nothing matches."
      onPick={onPick}
      onClose={onClose}
      onKeyDown={onKeyDown}
    />
  );
}

const options = () => screen.getAllByRole('option');
const input = () => screen.getByRole('combobox');

describe('Palette', () => {
  it('focuses the input on mount so typing lands in the palette', () => {
    render(<Harness rows={['a.md']} />);
    expect(input()).toHaveFocus();
  });

  it('picks the highlighted row on Enter, moving with the arrow keys', () => {
    const onPick = vi.fn();
    render(<Harness rows={['a.md', 'b.md', 'c.md']} onPick={onPick} />);

    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    fireEvent.keyDown(input(), { key: 'ArrowUp' });
    fireEvent.keyDown(input(), { key: 'Enter' });

    expect(onPick).toHaveBeenCalledWith({ path: 'b.md' });
  });

  it('clamps the highlight when the list shrinks under it', () => {
    const onPick = vi.fn();
    render(<Harness rows={['alpha.md', 'beta.md', 'alphabet.md']} onPick={onPick} />);

    // Highlight the third row, then type a query that leaves only two.
    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    fireEvent.change(input(), { target: { value: 'alpha' } });
    expect(options()).toHaveLength(2);

    // Enter must pick a real row (the query reset put the highlight back on top),
    // never a phantom index left over from the longer list.
    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(onPick).toHaveBeenCalledWith({ path: 'alpha.md' });
  });

  it('does nothing on Enter when nothing matches', () => {
    const onPick = vi.fn();
    render(<Harness rows={['a.md']} onPick={onPick} />);

    fireEvent.change(input(), { target: { value: 'zzz' } });
    expect(screen.getByText('Nothing matches.')).toBeInTheDocument();

    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(onPick).not.toHaveBeenCalled();
  });

  it('closes on Escape without letting it reach a surrounding handler', () => {
    const onClose = vi.fn();
    const outerEscape = vi.fn();
    render(
      <div onKeyDown={outerEscape}>
        <Harness rows={['a.md']} onClose={onClose} />
      </div>,
    );

    fireEvent.keyDown(input(), { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    // A workspace-level Escape handler must not also fire (it would close a pane).
    expect(outerEscape).not.toHaveBeenCalled();
  });

  it('closes on a backdrop click but not on a click inside the box', () => {
    const onClose = vi.fn();
    const { container } = render(<Harness rows={['a.md']} onClose={onClose} />);

    fireEvent.mouseDown(container.querySelector('.palette-box')!);
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.mouseDown(container.querySelector('.palette')!);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('lets a caller intercept keys the shell does not own', () => {
    const onPick = vi.fn();
    const onKeyDown = vi.fn((event: React.KeyboardEvent<HTMLInputElement>) => event.key === 'Enter');
    render(<Harness rows={['a.md']} onPick={onPick} onKeyDown={onKeyDown} />);

    // The caller claims Enter (path mode will claim Tab this way), so the shell's
    // own pick must not run.
    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(onKeyDown).toHaveBeenCalled();
    expect(onPick).not.toHaveBeenCalled();

    // Keys it declines still reach the shell.
    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    expect(options()[0]).toHaveAttribute('aria-selected', 'true');
  });

  it('namespaces classes and ARIA wiring by variant', () => {
    const { container } = render(<Harness rows={['a.md', 'b.md']} />);

    // Both the shared class (styled once) and the caller's own hook are present.
    expect(container.querySelector('.palette.test-palette')).toBeInTheDocument();
    expect(container.querySelector('.palette-option.test-palette-option')).toBeInTheDocument();

    expect(input()).toHaveAttribute('aria-controls', 'test-palette-list');
    expect(input()).toHaveAttribute('aria-activedescendant', 'test-palette-opt-0');
    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    expect(input()).toHaveAttribute('aria-activedescendant', 'test-palette-opt-1');
  });

  it('drops aria-activedescendant when there is no row to point at', () => {
    render(<Harness rows={['a.md']} />);
    fireEvent.change(input(), { target: { value: 'zzz' } });
    expect(input()).not.toHaveAttribute('aria-activedescendant');
  });
});
