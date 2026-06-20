import type { SessionAgent } from '../types/sessionAgent';
import {
  workspaceContextKeeperModelPresets,
  type WorkspaceContextKeeperConfig,
  type WorkspaceContextKeeperModelPreset,
} from './workspaceContextKeeper';

// The keeper runs three async background duties off one durable runner. They share
// the same {agent, model} config shape (parsed/serialized by workspaceContextKeeper)
// and differ only in their persisted settings key, their model presets, and whether
// a blank config means "use a built-in default" (always-on) or "disabled" (opt-in).
// This module is the single source of truth the Settings UI reads to render one row
// per duty.

export type KeeperConfig = WorkspaceContextKeeperConfig;
export type KeeperModelPreset = WorkspaceContextKeeperModelPreset;

export {
  parseWorkspaceContextKeeperConfig as parseKeeperConfig,
  serializeWorkspaceContextKeeperConfig as serializeKeeperConfig,
} from './workspaceContextKeeper';

export type KeeperDutyKey = 'summarize' | 'narrate' | 'compact';

export interface KeeperDutyDescriptor {
  key: KeeperDutyKey;
  /** The persisted settings key the daemon reads for this duty. */
  settingKey: string;
  /** Row title. */
  title: string;
  /** One-line summary of what the duty does. */
  description: string;
  /** data-testid prefix for this row's controls. */
  testIdPrefix: string;
  /**
   * opt-in duties (compaction) treat a blank config as DISABLED and expose a
   * "Disabled" agent option plus a Disable button. always-on duties (summarize,
   * narrate) fall back to a built-in tier default when blank, so they offer no
   * Disabled option and a "Use default" reset instead.
   */
  optInOnly: boolean;
  /**
   * Human label for the built-in default an always-on duty resolves to when unset,
   * shown in the row hint. Empty for opt-in duties (blank means off, not a default).
   */
  defaultLabel: string;
  /** Per-agent model presets; the FIRST entry is the recommended default. */
  modelPresets: (agent: SessionAgent | '') => readonly KeeperModelPreset[];
}

// Summarize is the CHEAP tier (one digest per finished session); the recommended
// model is the smallest competent one. Narrate is the STRONG tier (curates the
// journal); it leans one notch stronger. Compaction reuses the shipped
// workspaceContextKeeper presets so its recommended defaults stay byte-identical to
// the pre-unification behavior (claude -> opus, codex -> gpt-5.4).
const SUMMARIZE_PRESETS: Partial<Record<SessionAgent, readonly KeeperModelPreset[]>> = {
  claude: [
    { value: 'haiku', label: 'Haiku (Recommended — cheap)' },
    { value: 'sonnet', label: 'Sonnet (Higher quality)' },
  ],
  codex: [
    { value: 'gpt-5.4-mini', label: 'gpt-5.4-mini (Recommended — cheap)' },
    { value: 'gpt-5.4', label: 'gpt-5.4 (Higher quality)' },
  ],
};

const NARRATE_PRESETS: Partial<Record<SessionAgent, readonly KeeperModelPreset[]>> = {
  claude: [
    { value: 'sonnet', label: 'Sonnet (Recommended)' },
    { value: 'opus', label: 'Opus (Higher quality)' },
  ],
  codex: [
    { value: 'gpt-5.4', label: 'gpt-5.4 (Recommended)' },
    { value: 'gpt-5.4-mini', label: 'gpt-5.4-mini (Lower cost)' },
  ],
};

function staticPresets(
  map: Partial<Record<SessionAgent, readonly KeeperModelPreset[]>>,
): (agent: SessionAgent | '') => readonly KeeperModelPreset[] {
  return (agent) => (agent ? map[agent] ?? [] : []);
}

export const KEEPER_DUTIES: readonly KeeperDutyDescriptor[] = [
  {
    key: 'summarize',
    settingKey: 'notebook.summarize_session',
    title: 'Session summaries',
    description: 'Distills each finished session into a short digest for the journal.',
    testIdPrefix: 'settings-keeper-summarize',
    optInOnly: false,
    defaultLabel: 'Claude Haiku',
    modelPresets: staticPresets(SUMMARIZE_PRESETS),
  },
  {
    key: 'narrate',
    settingKey: 'notebook.narrate_workspace',
    title: 'Journal narration',
    description: 'Curates per-workspace digests into the running work journal.',
    testIdPrefix: 'settings-keeper-narrate',
    optInOnly: false,
    defaultLabel: 'Claude Sonnet',
    modelPresets: staticPresets(NARRATE_PRESETS),
  },
  {
    key: 'compact',
    settingKey: 'workspace_keeper_compact',
    title: 'Context compaction',
    description: 'Compacts large shared workspace contexts in the background.',
    testIdPrefix: 'settings-context-keeper',
    optInOnly: true,
    defaultLabel: '',
    modelPresets: workspaceContextKeeperModelPresets,
  },
];

export const KEEPER_DUTY_BY_KEY: Record<KeeperDutyKey, KeeperDutyDescriptor> =
  KEEPER_DUTIES.reduce(
    (acc, duty) => {
      acc[duty.key] = duty;
      return acc;
    },
    {} as Record<KeeperDutyKey, KeeperDutyDescriptor>,
  );

export function defaultKeeperDutyModel(dutyKey: KeeperDutyKey, agent: SessionAgent | ''): string {
  return KEEPER_DUTY_BY_KEY[dutyKey].modelPresets(agent)[0]?.value ?? '';
}

export function isKeeperDutyModelPreset(
  dutyKey: KeeperDutyKey,
  agent: SessionAgent | '',
  model: string,
): boolean {
  return KEEPER_DUTY_BY_KEY[dutyKey].modelPresets(agent).some((preset) => preset.value === model);
}

/**
 * keeperDutyModelSelection maps the draft (agent, model) to the value the model
 * <select> should show: empty when no agent is chosen, the preset value when the
 * model matches a preset, or the sentinel 'custom' when it is a free-form model the
 * presets don't cover (so the custom input reveals).
 */
export function keeperDutyModelSelection(
  dutyKey: KeeperDutyKey,
  agent: SessionAgent | '',
  model: string,
): string {
  if (!agent) return '';
  return isKeeperDutyModelPreset(dutyKey, agent, model) ? model : 'custom';
}
