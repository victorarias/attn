import type { SessionAgent } from '../types/sessionAgent';

export interface WorkspaceContextJanitorConfig {
  agent: SessionAgent;
  model: string;
}

export interface WorkspaceContextJanitorModelPreset {
  value: string;
  label: string;
}

const WORKSPACE_CONTEXT_JANITOR_MODEL_PRESETS: Partial<
  Record<SessionAgent, readonly WorkspaceContextJanitorModelPreset[]>
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

export function workspaceContextJanitorModelPresets(
  agent: SessionAgent | '',
): readonly WorkspaceContextJanitorModelPreset[] {
  if (!agent) return [];
  return WORKSPACE_CONTEXT_JANITOR_MODEL_PRESETS[agent] ?? [];
}

export function defaultWorkspaceContextJanitorModel(agent: SessionAgent | ''): string {
  return workspaceContextJanitorModelPresets(agent)[0]?.value ?? '';
}

export function isWorkspaceContextJanitorModelPreset(
  agent: SessionAgent | '',
  model: string,
): boolean {
  return workspaceContextJanitorModelPresets(agent).some((preset) => preset.value === model);
}

export function parseWorkspaceContextJanitorConfig(
  value?: string,
): WorkspaceContextJanitorConfig | null {
  const raw = value?.trim();
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Partial<WorkspaceContextJanitorConfig>;
    const agent = typeof parsed.agent === 'string' ? parsed.agent.trim().toLowerCase() : '';
    const model = typeof parsed.model === 'string' ? parsed.model.trim() : '';
    if (!agent || !model) return null;
    return { agent, model };
  } catch {
    return null;
  }
}

export function serializeWorkspaceContextJanitorConfig(
  config: WorkspaceContextJanitorConfig,
): string {
  return JSON.stringify({
    agent: config.agent.trim().toLowerCase(),
    model: config.model.trim(),
  });
}
