// A tiny module-level registry so the UI automation bridge can introspect and
// drive the Settings modal without a reference into the conditionally-mounted
// SettingsModal. SettingsModal publishes a handle while it's mounted and
// clears it on unmount; the bridge reads through getSettingsAutomationHandle().
// This is a test affordance only — it exposes read state and section
// selection, reusing the same code path as a real nav-item click.

const SETTINGS_SECTION_IDS = [
  'general',
  'connectivity',
  'plugins',
  'agents',
  'review',
  'hygiene',
  'backgroundTasks',
] as const;

export type SettingsAutomationSectionID = (typeof SETTINGS_SECTION_IDS)[number];

export interface SettingsAutomationState {
  open: boolean;
  activeSection: string;
  search: string;
}

export interface SettingsAutomationHandle {
  getState(): SettingsAutomationState;
  selectSection(sectionId: string): void;
}

let handle: SettingsAutomationHandle | null = null;

export function setSettingsAutomationHandle(next: SettingsAutomationHandle | null): void {
  handle = next;
}

export function getSettingsAutomationHandle(): SettingsAutomationHandle | null {
  return handle;
}

// State when the settings modal isn't mounted/open — so callers get a stable
// shape either way.
export const INACTIVE_SETTINGS_STATE: SettingsAutomationState = {
  open: false,
  activeSection: '',
  search: '',
};

// Validates a raw sectionId against the real SettingsSectionID union, throwing
// a clear, actionable error for an unknown id.
export function assertValidSettingsSectionID(
  sectionId: string,
): asserts sectionId is SettingsAutomationSectionID {
  if (!(SETTINGS_SECTION_IDS as readonly string[]).includes(sectionId)) {
    throw new Error(
      `unknown settings section "${sectionId}"; valid ids: ${SETTINGS_SECTION_IDS.join(', ')}`,
    );
  }
}
