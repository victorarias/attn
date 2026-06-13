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

  it('runs conflict detection when resetting a shortcut whose default is now claimed', () => {
    // session.new rebound off ⌘N; dock.diff has taken ⌘N. Resetting session.new
    // to its default (⌘N) must not silently duplicate — it should offer reassign.
    const { setSetting } = renderEditor({
      [KEYBINDINGS_SETTING_KEY]: JSON.stringify({
        version: 1,
        overrides: {
          'session.new': { key: 'j', meta: true },
          'dock.diff': { key: 'n', meta: true },
        },
      }),
    });

    fireEvent.click(within(row('New session in this workspace')).getByTitle('Reset to default'));

    // Reassign prompt appears naming the current ⌘N holder (Diff panel).
    const reassignBtn = screen.getByText('Reassign');
    expect(within(row('New session in this workspace')).getByText(/Diff panel/)).toBeInTheDocument();
    fireEvent.click(reassignBtn);

    const cfg = lastConfig(setSetting);
    // session.new back to default (override dropped), dock.diff freed.
    expect('session.new' in cfg.overrides).toBe(false);
    expect(cfg.overrides['dock.diff']).toBeNull();
  });

  it('pins a shortcut to the dock from its row star', () => {
    const { setSetting } = renderEditor();
    const newSession = row('New session in this workspace');
    // Not in the default dock -> star offers to add.
    fireEvent.click(within(newSession).getByLabelText('Add to dock'));

    expect(lastConfig(setSetting).dock.items).toContain('session.new');
    // Star now reflects membership (same row node, re-rendered in place).
    expect(within(newSession).getByLabelText('Remove from dock')).toBeInTheDocument();
  });

  it('reorders dock items with the up/down controls', () => {
    const { setSetting } = renderEditor({
      [KEYBINDINGS_SETTING_KEY]: JSON.stringify({
        version: 1,
        overrides: {},
        dock: { collapsed: false, items: ['dock.diff', 'dock.attention'] },
      }),
    });

    // First item can't move up; move it down instead.
    fireEvent.click(screen.getByLabelText('Move Diff panel down'));

    expect(lastConfig(setSetting).dock.items).toEqual(['dock.attention', 'dock.diff']);
  });

  it('removes a dock item from the dock section', () => {
    const { setSetting } = renderEditor({
      [KEYBINDINGS_SETTING_KEY]: JSON.stringify({
        version: 1,
        overrides: {},
        dock: { collapsed: false, items: ['dock.diff', 'dock.attention'] },
      }),
    });

    fireEvent.click(screen.getByLabelText('Remove Diff panel from dock'));

    expect(lastConfig(setSetting).dock.items).toEqual(['dock.attention']);
  });

  function recordChord(label: string, leader: KeyboardEventInit, follow: KeyboardEventInit) {
    fireEvent.click(within(row(label)).getByLabelText('Record a chord'));
    fireEvent.keyDown(window, leader);
    fireEvent.keyDown(window, follow);
  }

  it('records a chord on a row and persists it as the override', () => {
    const { setSetting } = renderEditor();
    recordChord('New session in this workspace', { key: 'y', metaKey: true }, { key: 'd' });
    expect(lastConfig(setSetting).overrides['session.new']).toEqual({
      leader: { key: 'y', meta: true },
      then: { key: 'd' },
    });
  });

  it('persists a chord whose leader equals the row’s own default combo', () => {
    // Regression: ⌘K-then-D on "Action menu" (default ⌘K) must not be silently
    // dropped as if it resolved to the default.
    const { setSetting } = renderEditor();
    recordChord('Action menu', { key: 'k', metaKey: true }, { key: 'd' });
    expect(lastConfig(setSetting).overrides['ui.actionMenu']).toEqual({
      leader: { key: 'k', meta: true },
      then: { key: 'd' },
    });
  });

  it('resets in-flight recording when the editor closes and reopens', () => {
    const setSetting = vi.fn();
    const tree = (open: boolean) => (
      <SettingsProvider settings={{}} setSetting={setSetting}>
        <KeybindingsProvider>
          <ShortcutEditorModal isOpen={open} onClose={() => {}} />
        </KeybindingsProvider>
      </SettingsProvider>
    );
    const { rerender } = render(tree(true));

    // Start a chord recording and capture the leader (now awaiting the follow key).
    fireEvent.click(within(row('Action menu')).getByLabelText('Record a chord'));
    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    expect(within(row('Action menu')).queryByLabelText('Record a chord')).toBeNull();

    rerender(tree(false));
    rerender(tree(true));
    // The row is no longer stuck recording.
    expect(within(row('Action menu')).getByLabelText('Record a chord')).toBeInTheDocument();
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
