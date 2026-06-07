import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useShortcut } from './useShortcut';

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
