// app/src/components/ShortcutEditorModal.tsx
// Editor for rebinding keyboard shortcuts. Every shortcut is rebindable; a
// protected few can't be left unbound. Conflicts are resolved VSCode-style:
// binding a taken combo asks to reassign, unbinding the previous holder.
// Saves immediately on each edit. Chords and "show in dock" land in later PRs.

import { useMemo, useState } from 'react';
import FocusTrap from 'focus-trap-react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { SHORTCUTS, ShortcutId, ShortcutDef, bindingsConflict } from '../shortcuts/registry';
import {
  SHORTCUT_META,
  SHORTCUT_CATEGORY_ORDER,
  SHORTCUT_CATEGORY_LABELS,
  ShortcutCategory,
} from '../shortcuts/metadata';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { useKeybindings } from '../contexts/KeybindingsContext';
import { KeyCaptureInput } from './KeyCaptureInput';
import './ShortcutEditorModal.css';

interface ShortcutEditorModalProps {
  isOpen: boolean;
  onClose: () => void;
}

// A requested change to a shortcut: an explicit combo, or 'default' to restore
// the registry default. Both flow through the same conflict check.
type BindingChange = ShortcutDef | 'default';

interface PendingReassign {
  id: ShortcutId;
  change: BindingChange;
  combo: ShortcutDef; // the effective combo, for display + conflict messaging
  conflictId: ShortcutId;
}

const REGISTRY_ORDER = Object.keys(SHORTCUTS) as ShortcutId[];

function groupedByCategory(): Record<ShortcutCategory, ShortcutId[]> {
  const groups = {
    sessions: [], panes: [], review: [], app: [],
  } as Record<ShortcutCategory, ShortcutId[]>;
  for (const id of REGISTRY_ORDER) {
    groups[SHORTCUT_META[id].category].push(id);
  }
  return groups;
}

function effectiveCombo(id: ShortcutId, change: BindingChange): ShortcutDef {
  return change === 'default' ? SHORTCUTS[id] : change;
}

// The value to persist: undefined (drop override → default) when the change is
// a reset or resolves to the default combo, otherwise the explicit combo.
function overrideValue(id: ShortcutId, change: BindingChange): ShortcutDef | undefined {
  if (change === 'default') return undefined;
  return bindingsConflict(change, SHORTCUTS[id]) ? undefined : change;
}

export function ShortcutEditorModal({ isOpen, onClose }: ShortcutEditorModalProps) {
  const kb = useKeybindings();
  const groups = useMemo(groupedByCategory, []);

  const [recordingId, setRecordingId] = useState<ShortcutId | null>(null);
  const [pending, setPending] = useState<PendingReassign | null>(null);
  const [rowError, setRowError] = useState<{ id: ShortcutId; message: string } | null>(null);

  useEscapeStack(onClose, isOpen && recordingId === null && pending === null);

  if (!isOpen) return null;

  const clearTransient = () => {
    setRecordingId(null);
    setPending(null);
    setRowError(null);
  };

  // Single entry point for any binding change (rebind or reset-to-default).
  // Conflict detection runs for both, so restoring a default that another
  // action has since claimed triggers the same reassign flow as a rebind
  // instead of silently creating a duplicate binding.
  const applyBinding = (id: ShortcutId, change: BindingChange) => {
    setRecordingId(null);
    setRowError(null);
    setPending(null);

    const combo = effectiveCombo(id, change);
    const conflictId = kb.findConflict(combo, id);
    if (conflictId) {
      if (kb.isProtected(conflictId)) {
        setRowError({
          id,
          message: `${formatShortcut(combo)} is reserved by “${SHORTCUT_META[conflictId].label}” and can’t be reassigned.`,
        });
        return;
      }
      setPending({ id, change, combo, conflictId });
      return;
    }
    kb.applyOverrides({ [id]: overrideValue(id, change) });
  };

  const confirmReassign = () => {
    if (!pending) return;
    kb.applyOverrides({
      [pending.id]: overrideValue(pending.id, pending.change),
      [pending.conflictId]: null,
    });
    setPending(null);
  };

  return (
    <div className="shortcut-editor-overlay" onClick={onClose}>
      <FocusTrap focusTrapOptions={{ allowOutsideClick: true, escapeDeactivates: false }}>
        <div
          className="shortcut-editor-modal"
          onClick={(e) => e.stopPropagation()}
          role="dialog"
          aria-modal="true"
          aria-labelledby="shortcut-editor-title"
        >
          <div className="shortcut-editor-header">
            <div>
              <h2 id="shortcut-editor-title">Customize Shortcuts</h2>
              <p className="shortcut-editor-subtitle">
                Click a shortcut to rebind it. Changes save automatically.
              </p>
            </div>
            <button
              className="shortcut-editor-close"
              onClick={onClose}
              aria-label="Close shortcut editor"
              type="button"
            >
              ×
            </button>
          </div>

          <div className="shortcut-editor-body">
            {SHORTCUT_CATEGORY_ORDER.map((category) => (
              <section className="shortcut-editor-category" key={category}>
                <h3 className="shortcut-editor-category-title">
                  {SHORTCUT_CATEGORY_LABELS[category]}
                </h3>
                <div className="shortcut-editor-rows">
                  {groups[category].map((id) => {
                    const binding = kb.resolve(id);
                    const customized = kb.isCustomized(id);
                    const isProtected = kb.isProtected(id);
                    const isPending = pending?.id === id;
                    return (
                      <div className="shortcut-editor-row" key={id}>
                        <span className="shortcut-editor-row-label">
                          {SHORTCUT_META[id].label}
                          {customized && (
                            <span className="shortcut-editor-badge">Customized</span>
                          )}
                          {isProtected && (
                            <span className="shortcut-editor-badge shortcut-editor-badge--locked">
                              Required
                            </span>
                          )}
                        </span>

                        {isPending ? (
                          <span className="shortcut-editor-reassign">
                            <span className="shortcut-editor-reassign-text">
                              {formatShortcut(pending!.combo)} is “{SHORTCUT_META[pending!.conflictId].label}”.
                            </span>
                            <button
                              type="button"
                              className="shortcut-editor-btn shortcut-editor-btn--primary"
                              onClick={confirmReassign}
                            >
                              Reassign
                            </button>
                            <button
                              type="button"
                              className="shortcut-editor-btn"
                              onClick={() => setPending(null)}
                            >
                              Cancel
                            </button>
                          </span>
                        ) : (
                          <span className="shortcut-editor-controls">
                            {rowError?.id === id && (
                              <span className="shortcut-editor-row-error">{rowError.message}</span>
                            )}
                            <KeyCaptureInput
                              binding={binding}
                              recording={recordingId === id}
                              onStart={() => {
                                setRowError(null);
                                setPending(null);
                                setRecordingId(id);
                              }}
                              onCapture={(def) => applyBinding(id, def)}
                              onCancel={() => setRecordingId(null)}
                            />
                            {customized && (
                              <button
                                type="button"
                                className="shortcut-editor-icon-btn"
                                title="Reset to default"
                                onClick={() => applyBinding(id, 'default')}
                              >
                                ↺
                              </button>
                            )}
                            {!isProtected && binding && (
                              <button
                                type="button"
                                className="shortcut-editor-icon-btn"
                                title="Unbind"
                                onClick={() => kb.applyOverrides({ [id]: null })}
                              >
                                ✕
                              </button>
                            )}
                          </span>
                        )}
                      </div>
                    );
                  })}
                </div>
              </section>
            ))}
          </div>

          <div className="shortcut-editor-footer">
            <button
              type="button"
              className="shortcut-editor-btn"
              onClick={() => {
                clearTransient();
                kb.restoreDefaults();
              }}
            >
              Restore Defaults
            </button>
            <button
              type="button"
              className="shortcut-editor-btn shortcut-editor-btn--primary"
              onClick={onClose}
            >
              Done
            </button>
          </div>
        </div>
      </FocusTrap>
    </div>
  );
}
