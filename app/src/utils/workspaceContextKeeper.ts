import type { SessionAgent } from '../types/sessionAgent';

export interface WorkspaceContextKeeperConfig {
  agent: SessionAgent;
  model: string;
}

export interface WorkspaceContextKeeperModelPreset {
  value: string;
  label: string;
}

const WORKSPACE_CONTEXT_KEEPER_MODEL_PRESETS: Partial<
  Record<SessionAgent, readonly WorkspaceContextKeeperModelPreset[]>
> = {
  codex: [
    { value: 'gpt-5.4', label: 'gpt-5.4 (Recommended)' },
    { value: 'gpt-5.4-mini', label: 'gpt-5.4-mini (Lower cost)' },
  ],
  claude: [
    { value: 'opus', label: 'Opus (Recommended)' },
    { value: 'sonnet', label: 'Sonnet (Faster)' },
  ],
};

export function workspaceContextKeeperModelPresets(
  agent: SessionAgent | '',
): readonly WorkspaceContextKeeperModelPreset[] {
  if (!agent) return [];
  return WORKSPACE_CONTEXT_KEEPER_MODEL_PRESETS[agent] ?? [];
}

export function defaultWorkspaceContextKeeperModel(agent: SessionAgent | ''): string {
  return workspaceContextKeeperModelPresets(agent)[0]?.value ?? '';
}

export function isWorkspaceContextKeeperModelPreset(
  agent: SessionAgent | '',
  model: string,
): boolean {
  return workspaceContextKeeperModelPresets(agent).some((preset) => preset.value === model);
}

export function parseWorkspaceContextKeeperConfig(
  value?: string,
): WorkspaceContextKeeperConfig | null {
  const raw = value?.trim();
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Partial<WorkspaceContextKeeperConfig>;
    const agent = typeof parsed.agent === 'string' ? parsed.agent.trim().toLowerCase() : '';
    const model = typeof parsed.model === 'string' ? parsed.model.trim() : '';
    if (!agent || !model) return null;
    return { agent, model };
  } catch {
    return null;
  }
}

export function serializeWorkspaceContextKeeperConfig(
  config: WorkspaceContextKeeperConfig,
): string {
  return JSON.stringify({
    agent: config.agent.trim().toLowerCase(),
    model: config.model.trim(),
  });
}
