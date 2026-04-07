import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useShortcut } from './useShortcut';

function ShortcutHarness(props: {
  onSessionClose: () => void;
  onTerminalClose: () => void;
  terminalEnabled?: boolean;
}) {
  useShortcut('session.close', props.onSessionClose, true);
  useShortcut('terminal.close', props.onTerminalClose, props.terminalEnabled ?? true);

  return (
    <div>
      <button data-testid="plain-target" type="button">Plain</button>
      <div data-testid="terminal-target" className="terminal-container" />
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
});
