export type WorkspaceSelectionStyle = 'rail' | 'spotlight';

export const WORKSPACE_SELECTION_STYLE_STORAGE_KEY = 'attn.workspace.selectionStyle';

export function readWorkspaceSelectionStyle(): WorkspaceSelectionStyle {
  try {
    return window.localStorage.getItem(WORKSPACE_SELECTION_STYLE_STORAGE_KEY) === 'spotlight'
      ? 'spotlight'
      : 'rail';
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
