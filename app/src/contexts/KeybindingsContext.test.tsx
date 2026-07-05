import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { SettingsProvider } from './SettingsContext';
import { KeybindingsProvider, useKeybindings } from './KeybindingsContext';
import {
  KEYBINDINGS_SETTING_KEY,
  DEFAULT_DOCK_ITEMS,
  setShortcutOverrides,
} from '../shortcuts/resolver';

afterEach(() => setShortcutOverrides({}));

function Consumer() {
  const kb = useKeybindings();
  return (
    <div>
      <span data-testid="collapsed">{String(kb.dock.collapsed)}</span>
      <span data-testid="items">{kb.dock.items.join(',')}</span>
      <span data-testid="indock-new">{String(kb.isInDock('session.new'))}</span>
      <button onClick={() => kb.setDockCollapsed(true)}>collapse</button>
      <button onClick={() => kb.setDockCollapsed(false)}>expand</button>
      <button onClick={() => kb.setInDock('session.new', true)}>add-new</button>
      <button onClick={() => kb.moveDockItem('dock.attention', 1)}>move-attention-down</button>
    </div>
  );
}

function renderConsumer(initial: Record<string, string> = {}) {
  const setSetting = vi.fn();
  render(
    <SettingsProvider settings={initial} setSetting={setSetting}>
      <KeybindingsProvider>
        <Consumer />
      </KeybindingsProvider>
    </SettingsProvider>,
  );
  return { setSetting };
}

function lastConfig(setSetting: ReturnType<typeof vi.fn>) {
  const calls = setSetting.mock.calls.filter(([k]) => k === KEYBINDINGS_SETTING_KEY);
  return JSON.parse(calls[calls.length - 1][1]);
}

function dockSetting(initial: { collapsed?: boolean; items: string[] }) {
  return {
    [KEYBINDINGS_SETTING_KEY]: JSON.stringify({
      version: 1,
      overrides: {},
      dock: { collapsed: initial.collapsed ?? false, items: initial.items },
    }),
  };
}

describe('KeybindingsContext dock', () => {
  it('exposes the default dock when no config is persisted', () => {
    renderConsumer();
    expect(screen.getByTestId('collapsed').textContent).toBe('false');
    expect(screen.getByTestId('items').textContent).toBe(DEFAULT_DOCK_ITEMS.join(','));
  });

  it('persists collapse without disturbing membership', () => {
    const { setSetting } = renderConsumer(dockSetting({ items: ['dock.attention'] }));
    fireEvent.click(screen.getByText('collapse'));

    const cfg = lastConfig(setSetting);
    expect(cfg.dock.collapsed).toBe(true);
    expect(cfg.dock.items).toEqual(['dock.attention']);
    expect(screen.getByTestId('collapsed').textContent).toBe('true');
  });

  it('does not write when collapse state is unchanged', () => {
    const { setSetting } = renderConsumer(dockSetting({ collapsed: false, items: ['dock.attention'] }));
    fireEvent.click(screen.getByText('expand')); // already expanded
    expect(setSetting.mock.calls.filter(([k]) => k === KEYBINDINGS_SETTING_KEY)).toHaveLength(0);
  });

  it('adds a shortcut to the dock and reflects membership', () => {
    const { setSetting } = renderConsumer(dockSetting({ items: ['dock.attention'] }));
    expect(screen.getByTestId('indock-new').textContent).toBe('false');

    fireEvent.click(screen.getByText('add-new'));

    expect(lastConfig(setSetting).dock.items).toEqual(['dock.attention', 'session.new']);
    expect(screen.getByTestId('indock-new').textContent).toBe('true');
  });

  it('reorders dock items and persists the new order', () => {
    const { setSetting } = renderConsumer(dockSetting({ items: ['dock.attention', 'terminal.splitVertical'] }));
    fireEvent.click(screen.getByText('move-attention-down'));
    expect(lastConfig(setSetting).dock.items).toEqual(['terminal.splitVertical', 'dock.attention']);
  });

  it('preserves the dock when only overrides change (restoreDefaults aside)', () => {
    // Editing a keybinding must not wipe a customized dock.
    const { setSetting } = renderConsumer(dockSetting({ items: ['dock.attention'] }));
    fireEvent.click(screen.getByText('add-new'));
    const cfg = lastConfig(setSetting);
    expect(cfg.dock.items).toEqual(['dock.attention', 'session.new']);
    expect(cfg.overrides).toEqual({});
  });
});
