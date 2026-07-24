// A tiny module-level registry so the UI automation bridge / packaged-app
// harness can drive and introspect the structured automation form without a
// reference into the conditionally-mounted AutomationForm. Mirrors
// automationEditorAutomation.ts's pattern exactly: AutomationForm publishes a
// handle while mounted and clears it on unmount; the bridge reads/drives
// through get/setAutomationFormAutomationHandle(). This exists for testing
// only — it is not part of the component's own behavior.
import type { AutomationFormValues } from './automationFormModel';

export interface AutomationFormAutomationState {
  present: boolean;
  mode: 'create' | 'edit';
  definitionId: string | null;
  revision: number;
  status: 'loading' | 'ready' | 'load-error';
  loadError: string;
  values: AutomationFormValues;
  errors: Record<string, string>;
  saving: boolean;
  saveError: string;
  saveErrorCode: string;
  enabled: boolean | null;
  compiledSentence: string;
  deleteArmed: boolean;
}

export interface AutomationFormAutomationHandle {
  getState(): AutomationFormAutomationState;
  setValues(partial: Partial<AutomationFormValues>): void;
  submit(): void;
  reload(): void;
  armDelete(): void;
  confirmDelete(): void;
}

let handle: AutomationFormAutomationHandle | null = null;

export function setAutomationFormAutomationHandle(next: AutomationFormAutomationHandle | null): void {
  handle = next;
}

export function getAutomationFormAutomationHandle(): AutomationFormAutomationHandle | null {
  return handle;
}
