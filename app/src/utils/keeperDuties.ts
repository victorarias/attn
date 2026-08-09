import type { SessionAgent } from '../types/sessionAgent';
import {
  workspaceContextKeeperModelPresets,
  type WorkspaceContextKeeperConfig,
  type WorkspaceContextKeeperModelPreset,
} from './workspaceContextKeeper';

// The keeper's three background duties share one {agent, model} config shape and
// differ only in settings key, model presets, and what a blank config means. The
// single source the Settings UI renders one row per duty from.

export type KeeperConfig = WorkspaceContextKeeperConfig;
export type KeeperModelPreset = WorkspaceContextKeeperModelPreset;

export {
  parseWorkspaceContextKeeperConfig as parseKeeperConfig,
  serializeWorkspaceContextKeeperConfig as serializeKeeperConfig,
} from './workspaceContextKeeper';

export type KeeperDutyKey = 'summarize' | 'narrate' | 'compact';

export interface KeeperDutyDescriptor {
  key: KeeperDutyKey;
  settingKey: string;
  /** Runtime on/off switch, independent of the agent/model config. */
  enabledSettingKey?: string;
  title: string;
  description: string;
  testIdPrefix: string;
  /**
   * A blank config means disabled for an opt-in duty, and the built-in tier
   * default for the rest — which is what decides between offering a "Disabled"
   * agent option and offering a "Use default" reset.
   */
  optInOnly: boolean;
  /** Row-hint label for the built-in default; empty for opt-in duties. */
  defaultLabel: string;
  /** Per-agent model presets; the FIRST entry is the recommended default. */
  modelPresets: (agent: SessionAgent | '') => readonly KeeperModelPreset[];
}

// Summarize is the cheap tier, narrate the strong one; compaction reuses the
// workspaceContextKeeper presets unchanged.
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
    enabledSettingKey: 'notebook.summarize_session.enabled',
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
    enabledSettingKey: 'notebook.narrate_workspace.enabled',
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
 * The value the model <select> shows: the preset, or the 'custom' sentinel that
 * reveals the free-form input for a model no preset covers.
 */
export function keeperDutyModelSelection(
  dutyKey: KeeperDutyKey,
  agent: SessionAgent | '',
  model: string,
): string {
  if (!agent) return '';
  return isKeeperDutyModelPreset(dutyKey, agent, model) ? model : 'custom';
}
