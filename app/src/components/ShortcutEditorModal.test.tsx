import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';
import { ShortcutEditorModal } from './ShortcutEditorModal';
import { SettingsProvider } from '../contexts/SettingsContext';
import { KeybindingsProvider } from '../contexts/KeybindingsContext';
import { KEYBINDINGS_SETTING_KEY, setShortcutOverrides } from '../shortcuts/resolver';

afterEach(() => setShortcutOverrides({}));

function renderEditor(initial: Record<string, string> = {}) {
  const setSetting = vi.fn();
  const onClose = vi.fn();
  render(
    <SettingsProvider settings={initial} setSetting={setSetting}>
      <KeybindingsProvider>
        <ShortcutEditorModal isOpen onClose={onClose} />
      </KeybindingsProvider>
    </SettingsProvider>,
  );
  return { setSetting, onClose };
}

function row(label: string): HTMLElement {
  const el = screen.getByText(label).closest('.shortcut-editor-row');
  if (!el) throw new Error(`row not found: ${label}`);
  return el as HTMLElement;
}

function lastConfig(setSetting: ReturnType<typeof vi.fn>) {
  const calls = setSetting.mock.calls.filter(([k]) => k === KEYBINDINGS_SETTING_KEY);
  return JSON.parse(calls[calls.length - 1][1]);
}

describe('ShortcutEditorModal', () => {
  it('renders categories and current bindings', () => {
    renderEditor();
    expect(screen.getByRole('dialog', { name: 'Customize Shortcuts' })).toBeInTheDocument();
    expect(screen.getByText('Workspaces & Sessions')).toBeInTheDocument();
    expect(screen.getByText('Panes & Terminals')).toBeInTheDocument();

    const newSession = row('New session in this workspace');
    expect(newSession.textContent).toContain('⌘');
    expect(newSession.textContent).toContain('N');
  });

  it('marks protected shortcuts as required and hides their unbind control', () => {
    renderEditor();
    const settings = row('Settings');
    expect(within(settings).getByText('Required')).toBeInTheDocument();
    expect(within(settings).queryByTitle('Unbind')).toBeNull();

    const newSession = row('New session in this workspace');
    expect(within(newSession).getByTitle('Unbind')).toBeInTheDocument();
  });

  it('unbinds a shortcut and persists the override', () => {
    const { setSetting } = renderEditor();
    fireEvent.click(within(row('New session in this workspace')).getByTitle('Unbind'));

    expect(lastConfig(setSetting).overrides['session.new']).toBeNull();
    expect(row('New session in this workspace').textContent).toContain('Unassigned');
  });

  it('reassigns a conflicting combo, unbinding the previous holder', () => {
    const { setSetting } = renderEditor();

    const newSession = row('New session in this workspace');
    fireEvent.click(newSession.querySelector('.key-capture-button')!);
    // ⌘⇧G already belongs to "Diff panel" (dock.diff).
    fireEvent.keyDown(window, { key: 'g', code: 'KeyG', metaKey: true, shiftKey: true });

    // The reassign prompt appears inline on this row, naming the current holder.
    const reassignBtn = screen.getByText('Reassign');
    expect(within(newSession).getByText(/Diff panel/)).toBeInTheDocument();
    fireEvent.click(reassignBtn);

    const cfg = lastConfig(setSetting);
    expect(cfg.overrides['session.new']).toEqual({ key: 'g', meta: true, shift: true });
    expect(cfg.overrides['dock.diff']).toBeNull();
  });

  it('restores defaults', () => {
    const { setSetting } = renderEditor({
      [KEYBINDINGS_SETTING_KEY]: JSON.stringify({
        version: 1,
        overrides: { 'session.new': { key: 'm', meta: true } },
      }),
    });
    // Starts customized.
    expect(within(row('New session in this workspace')).getByText('Customized')).toBeInTheDocument();

    fireEvent.click(screen.getByText('Restore Defaults'));
    expect(lastConfig(setSetting).overrides).toEqual({});
  });
});
