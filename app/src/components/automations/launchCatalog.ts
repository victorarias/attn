// Model and effort catalogs for automation launch configuration.
//
// These catalogs are maintained here rather than derived from the agent
// CLIs: neither codex nor claude exposes an API to enumerate its available
// models. Effort options are modeled per-model, not per-agent, because a
// model's supported effort levels (and its sensible default) vary between
// models offered by the same agent — e.g. codex's "mini" models and
// claude's "haiku" drop the highest effort tier.

export type AutomationAgent = 'codex' | 'claude';

export interface LaunchModelOption {
  id: string;
  label: string;
  efforts: readonly string[];
  defaultEffort: string;
}

export interface AgentLaunchCatalog {
  models: readonly LaunchModelOption[];
  customEfforts: readonly string[];
  customDefaultEffort: string;
}

const CODEX_STANDARD_EFFORTS = ['minimal', 'low', 'medium', 'high', 'xhigh'] as const;
const CLAUDE_STANDARD_EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max'] as const;

export const LAUNCH_CATALOG: Record<AutomationAgent, AgentLaunchCatalog> = {
  codex: {
    models: [
      { id: 'gpt-5.6-luna', label: 'gpt-5.6-luna', efforts: CODEX_STANDARD_EFFORTS, defaultEffort: 'medium' },
      { id: 'gpt-5.6-terra', label: 'gpt-5.6-terra', efforts: CODEX_STANDARD_EFFORTS, defaultEffort: 'medium' },
      { id: 'gpt-5.6-sol', label: 'gpt-5.6-sol', efforts: CODEX_STANDARD_EFFORTS, defaultEffort: 'medium' },
      { id: 'gpt-5.5', label: 'gpt-5.5', efforts: CODEX_STANDARD_EFFORTS, defaultEffort: 'medium' },
      { id: 'gpt-5.4-codex', label: 'gpt-5.4-codex', efforts: CODEX_STANDARD_EFFORTS, defaultEffort: 'medium' },
      { id: 'gpt-5.4', label: 'gpt-5.4', efforts: CODEX_STANDARD_EFFORTS, defaultEffort: 'medium' },
      {
        id: 'gpt-5.4-mini',
        label: 'gpt-5.4-mini (cheap)',
        efforts: ['minimal', 'low', 'medium', 'high'],
        defaultEffort: 'medium',
      },
    ],
    customEfforts: CODEX_STANDARD_EFFORTS,
    customDefaultEffort: 'medium',
  },
  claude: {
    models: [
      { id: 'fable', label: 'Fable', efforts: CLAUDE_STANDARD_EFFORTS, defaultEffort: 'high' },
      { id: 'opus', label: 'Opus', efforts: CLAUDE_STANDARD_EFFORTS, defaultEffort: 'high' },
      { id: 'sonnet', label: 'Sonnet', efforts: CLAUDE_STANDARD_EFFORTS, defaultEffort: 'medium' },
      {
        id: 'haiku',
        label: 'Haiku (cheap)',
        efforts: ['low', 'medium', 'high'],
        defaultEffort: 'medium',
      },
    ],
    customEfforts: CLAUDE_STANDARD_EFFORTS,
    customDefaultEffort: 'medium',
  },
};

// effortOptionsFor resolves the effort list and default for a given agent
// and model. modelId '' or any id not present in the agent's catalog falls
// back to the agent's custom entry (free-text model names typed by hand).
export function effortOptionsFor(
  agent: AutomationAgent,
  modelId: string,
): { efforts: readonly string[]; defaultEffort: string } {
  const catalog = LAUNCH_CATALOG[agent];
  const model = modelId ? catalog.models.find((candidate) => candidate.id === modelId) : undefined;
  if (!model) {
    return { efforts: catalog.customEfforts, defaultEffort: catalog.customDefaultEffort };
  }
  return { efforts: model.efforts, defaultEffort: model.defaultEffort };
}
