// A tiny module-level registry so the UI automation bridge can introspect the
// automation editor's live buffer without a reference into the conditionally-
// mounted AutomationEditor. AutomationEditor publishes a handle while it's
// mounted and clears it on unmount; the bridge reads through
// getAutomationEditorAutomationHandle(). This is a test affordance only — it
// exists because AutomationYamlEditor is CodeMirror-backed and CodeMirror
// virtualizes long documents, so scraping `.cm-content`/`.cm-line` from the
// DOM can silently truncate the buffer text this seam is meant to prove (a
// save/reload round-trip preserving YAML comments). getState() reads real
// component state directly; setText() replaces the buffer the same way the
// user's own typing would (see AutomationEditor.tsx's registration effect).
export interface AutomationEditorAutomationState {
  present: true;
  mode: 'create' | 'edit';
  definitionId: string | null;
  revision: number;
  status: 'loading' | 'ready' | 'load-error';
  loadError: string;
  text: string;
  validation: { state: 'idle' | 'checking' | 'ok' | 'error'; message: string };
  saving: boolean;
  saveError: string;
  reloading: boolean;
  reloadError: string;
  reloadOffered: boolean;
}

export interface AutomationEditorAutomationHandle {
  getState(): AutomationEditorAutomationState;
  setText(next: string): void;
}

let handle: AutomationEditorAutomationHandle | null = null;

export function setAutomationEditorAutomationHandle(next: AutomationEditorAutomationHandle | null): void {
  handle = next;
}

export function getAutomationEditorAutomationHandle(): AutomationEditorAutomationHandle | null {
  return handle;
}

// State when the editor isn't mounted — so callers get a stable shape either way.
export const INACTIVE_AUTOMATION_EDITOR_STATE: { present: false } = { present: false };
