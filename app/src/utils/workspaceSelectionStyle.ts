export type WorkspaceSelectionStyle = 'dim' | 'rail' | 'spotlight';

export const WORKSPACE_SELECTION_STYLE_STORAGE_KEY = 'attn.workspace.selectionStyle';

export function readWorkspaceSelectionStyle(): WorkspaceSelectionStyle {
  try {
    const stored = window.localStorage.getItem(WORKSPACE_SELECTION_STYLE_STORAGE_KEY);
    return stored === 'dim' || stored === 'spotlight' ? stored : 'rail';
  } catch {
    return 'rail';
  }
}

export function persistWorkspaceSelectionStyle(style: WorkspaceSelectionStyle): void {
  try {
    window.localStorage.setItem(WORKSPACE_SELECTION_STYLE_STORAGE_KEY, style);
  } catch (err) {
    console.warn('[App] Failed to persist workspace-selection style:', err);
  }
}
