
// Must match SettingsModal's SettingsSectionID union.
const SETTINGS_SECTION_IDS = [
  'general',
  'workspace',
  'hygiene',
  'agents',
  'keeper',
  'autoMode',
  'delegation',
  'connectivity',
  'plugins',
  'backgroundTasks',
  'eventBus',
  'data',
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

export const INACTIVE_SETTINGS_STATE: SettingsAutomationState = {
  open: false,
  activeSection: '',
  search: '',
};

export function assertValidSettingsSectionID(
  sectionId: string,
): asserts sectionId is SettingsAutomationSectionID {
  if (!(SETTINGS_SECTION_IDS as readonly string[]).includes(sectionId)) {
    throw new Error(
      `unknown settings section "${sectionId}"; valid ids: ${SETTINGS_SECTION_IDS.join(', ')}`,
    );
  }
}
