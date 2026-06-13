import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useShortcut } from './useShortcut';
import { setShortcutOverrides } from './resolver';
import { cancelLeader, isLeaderPending } from './chordState';

function ShortcutHarness(props: {
  onSessionClose: () => void;
  onTerminalClose: () => void;
  onToggleZoom?: () => void;
  onSelectWorkspace?: () => void;
  terminalEnabled?: boolean;
}) {
  useShortcut('session.close', props.onSessionClose, true);
  useShortcut('terminal.close', props.onTerminalClose, props.terminalEnabled ?? true);
  useShortcut('terminal.toggleZoom', props.onToggleZoom ?? (() => {}), props.onToggleZoom !== undefined);
  useShortcut('workspace.select1', props.onSelectWorkspace ?? (() => {}), props.onSelectWorkspace !== undefined);

  return (
    <div>
      <button data-testid="plain-target" type="button">Plain</button>
      <div data-testid="terminal-target" className="terminal-container" />
      <div className="session-terminal-workspace">
        <input aria-label="Browser address" />
      </div>
    </div>
  );
}

describe('useShortcut close priority', () => {
  it('prefers terminal.close for Cmd+W when a terminal-close handler is active', () => {
    const onSessionClose = vi.fn();
    const onTerminalClose = vi.fn();

    render(
      <ShortcutHarness
        onSessionClose={onSessionClose}
        onTerminalClose={onTerminalClose}
      />,
    );

    fireEvent.keyDown(screen.getByTestId('terminal-target'), { key: 'w', metaKey: true });

    expect(onTerminalClose).toHaveBeenCalledTimes(1);
    expect(onSessionClose).not.toHaveBeenCalled();
  });

  it('falls through to session.close for Cmd+W when no terminal-close handler is active', () => {
    const onSessionClose = vi.fn();
    const onTerminalClose = vi.fn();

    render(
      <ShortcutHarness
        onSessionClose={onSessionClose}
        onTerminalClose={onTerminalClose}
        terminalEnabled={false}
      />,
    );

    fireEvent.keyDown(screen.getByTestId('terminal-target'), { key: 'w', metaKey: true });

    expect(onSessionClose).toHaveBeenCalledTimes(1);
    expect(onTerminalClose).not.toHaveBeenCalled();
  });

  it('uses session.close for Cmd+W outside terminal targets', () => {
    const onSessionClose = vi.fn();
    const onTerminalClose = vi.fn();

    render(
      <ShortcutHarness
        onSessionClose={onSessionClose}
        onTerminalClose={onTerminalClose}
      />,
    );

    fireEvent.keyDown(screen.getByTestId('plain-target'), { key: 'w', metaKey: true });

    expect(onSessionClose).toHaveBeenCalledTimes(1);
    expect(onTerminalClose).not.toHaveBeenCalled();
  });

  it('routes native Cmd+W through the existing pane-first close shortcut', () => {
    const onSessionClose = vi.fn();
    const onTerminalClose = vi.fn();

    render(
      <ShortcutHarness
        onSessionClose={onSessionClose}
        onTerminalClose={onTerminalClose}
      />,
    );

    window.dispatchEvent(new CustomEvent('attn:native-shortcut', { detail: 'session.close' }));

    expect(onSessionClose).toHaveBeenCalledTimes(1);
    expect(onTerminalClose).not.toHaveBeenCalled();
  });

  it('leaves standard text shortcuts alone in non-terminal editable controls', () => {
    const onToggleZoom = vi.fn();

    render(
      <ShortcutHarness
        onSessionClose={vi.fn()}
        onTerminalClose={vi.fn()}
        onToggleZoom={onToggleZoom}
      />,
    );

    const allowed = fireEvent.keyDown(screen.getByRole('textbox', { name: 'Browser address' }), {
      key: 'z',
      metaKey: true,
      shiftKey: true,
    });

    expect(allowed).toBe(true);
    expect(onToggleZoom).not.toHaveBeenCalled();
  });

  it('suppresses an editableTarget:native native-shortcut while a non-terminal input is focused', () => {
    const onToggleZoom = vi.fn();

    render(
      <ShortcutHarness
        onSessionClose={vi.fn()}
        onTerminalClose={vi.fn()}
        onToggleZoom={onToggleZoom}
      />,
    );

    // The native macOS "Zoom Pane" menu item (which replaced Redo on ⇧⌘Z)
    // forwards terminal.toggleZoom through the bridge regardless of focus, so the
    // bridge must defer to a focused text input just like the keydown path does.
    screen.getByRole('textbox', { name: 'Browser address' }).focus();
    window.dispatchEvent(new CustomEvent('attn:native-shortcut', { detail: 'terminal.toggleZoom' }));

    expect(onToggleZoom).not.toHaveBeenCalled();
  });

  it('fires an editableTarget:native native-shortcut when no editable input is focused', () => {
    const onToggleZoom = vi.fn();

    render(
      <ShortcutHarness
        onSessionClose={vi.fn()}
        onTerminalClose={vi.fn()}
        onToggleZoom={onToggleZoom}
      />,
    );

    window.dispatchEvent(new CustomEvent('attn:native-shortcut', { detail: 'terminal.toggleZoom' }));

    expect(onToggleZoom).toHaveBeenCalledTimes(1);
  });

  it('keeps unrelated app shortcuts active in non-terminal editable controls', () => {
    const onSelectWorkspace = vi.fn();

    render(
      <ShortcutHarness
        onSessionClose={vi.fn()}
        onTerminalClose={vi.fn()}
        onSelectWorkspace={onSelectWorkspace}
      />,
    );

    const allowed = fireEvent.keyDown(screen.getByRole('textbox', { name: 'Browser address' }), {
      key: '1',
      code: 'Digit1',
      metaKey: true,
    });

    expect(allowed).toBe(false);
    expect(onSelectWorkspace).toHaveBeenCalledTimes(1);
  });
});

describe('useShortcut leader-key chords', () => {
  afterEach(() => {
    setShortcutOverrides({});
    cancelLeader();
  });

  it('arms a leader (consuming it) and fires the bound action on the follow key', () => {
    setShortcutOverrides({ 'terminal.toggleZoom': { leader: { key: 'y', meta: true }, then: { key: 'z' } } });
    const onToggleZoom = vi.fn();
    render(<ShortcutHarness onSessionClose={vi.fn()} onTerminalClose={vi.fn()} onToggleZoom={onToggleZoom} />);

    const armed = fireEvent.keyDown(screen.getByTestId('plain-target'), { key: 'y', metaKey: true });
    expect(armed).toBe(false); // leader consumed (preventDefault)
    expect(isLeaderPending()).toBe(true);
    expect(onToggleZoom).not.toHaveBeenCalled();

    const fired = fireEvent.keyDown(screen.getByTestId('plain-target'), { key: 'z' });
    expect(fired).toBe(false); // follow consumed
    expect(onToggleZoom).toHaveBeenCalledTimes(1);
    expect(isLeaderPending()).toBe(false);
  });

  it('does not let the follow key also fire a single combo on the same keystroke', () => {
    setShortcutOverrides({ 'terminal.toggleZoom': { leader: { key: 'y', meta: true }, then: { key: '1', meta: true } } });
    const onToggleZoom = vi.fn();
    const onSelectWorkspace = vi.fn();
    render(
      <ShortcutHarness
        onSessionClose={vi.fn()}
        onTerminalClose={vi.fn()}
        onToggleZoom={onToggleZoom}
        onSelectWorkspace={onSelectWorkspace}
      />,
    );

    fireEvent.keyDown(screen.getByTestId('plain-target'), { key: 'y', metaKey: true });
    fireEvent.keyDown(screen.getByTestId('plain-target'), { key: '1', code: 'Digit1', metaKey: true });
    expect(onToggleZoom).toHaveBeenCalledTimes(1); // chord fired
    expect(onSelectWorkspace).not.toHaveBeenCalled(); // ⌘1 combo did NOT also fire
  });

  it('consumes a bound leader even when its follow action has no handler', () => {
    // toggleZoom is bound to a chord but not registered (no handler).
    setShortcutOverrides({ 'terminal.toggleZoom': { leader: { key: 'y', meta: true }, then: { key: 'z' } } });
    render(<ShortcutHarness onSessionClose={vi.fn()} onTerminalClose={vi.fn()} />);

    const armed = fireEvent.keyDown(screen.getByTestId('plain-target'), { key: 'y', metaKey: true });
    expect(armed).toBe(false); // consumed, no default leak
    expect(isLeaderPending()).toBe(false); // nothing armed
  });

  it('does not arm a chord leader inside a non-terminal editable target', () => {
    setShortcutOverrides({ 'terminal.toggleZoom': { leader: { key: 'y', meta: true }, then: { key: 'z' } } });
    const onToggleZoom = vi.fn();
    render(<ShortcutHarness onSessionClose={vi.fn()} onTerminalClose={vi.fn()} onToggleZoom={onToggleZoom} />);

    const allowed = fireEvent.keyDown(screen.getByRole('textbox', { name: 'Browser address' }), {
      key: 'y',
      metaKey: true,
    });
    expect(allowed).toBe(true); // passed through to the input
    expect(isLeaderPending()).toBe(false);
    expect(onToggleZoom).not.toHaveBeenCalled();
  });
});
