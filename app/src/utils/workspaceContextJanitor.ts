import type { SessionAgent } from '../types/sessionAgent';

export interface WorkspaceContextJanitorConfig {
  agent: SessionAgent;
  model: string;
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
