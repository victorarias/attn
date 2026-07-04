// Shared scenario catalog for the serial matrix and the soak runner. Both
// scripts drive the same packaged-app scenarios; keeping one list here avoids
// two copies drifting apart as scenarios are added or renamed.
export const scenarioCatalog = [
  {
    id: 'workspace-shell-lifecycle',
    label: 'Workspace shell lifecycle',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-shell-lifecycle'],
  },
  {
    id: 'workspace-creation-shortcuts',
    label: 'Workspace creation shortcuts',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-creation-shortcuts'],
  },
  {
    id: 'workspace-switching',
    label: 'Workspace switching',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-switching'],
  },
  {
    id: 'workspace-move-leaf',
    label: 'Workspace move pane between workspaces',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-move-leaf'],
  },
  {
    id: 'workspace-close-last-session-switches-back',
    label: 'Workspace close last session switches back',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-close-last-session-switches-back'],
  },
  {
    id: 'workspace-close-one-session-keeps-selection',
    label: 'Workspace close one session keeps selection',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-close-one-session-keeps-selection'],
  },
  {
    id: 'tile-only-workspace-select',
    label: 'Tile-only workspace select + render',
    command: ['pnpm', 'run', 'real-app:scenario-tile-only-workspace-select'],
  },
  {
    id: 'notebook-tile-finder',
    label: 'Notebook tile finder (native Cmd+Opt+N dock, Cmd+P re-summon)',
    command: ['pnpm', 'run', 'real-app:scenario-notebook-tile-finder'],
  },
  {
    id: 'autoclose-on-exit',
    label: 'Auto-close on clean exit, keep failed exits',
    command: ['pnpm', 'run', 'real-app:scenario-autoclose-on-exit'],
  },
  {
    id: 'diff-review',
    label: 'Diff review panel renders a real diff (@pierre/diffs)',
    command: ['pnpm', 'run', 'real-app:scenario-diff-review'],
  },
  {
    id: 'ticket-lifecycle',
    label: 'Ticket lifecycle: chief delegates, worker reports, chief reviews in the panel',
    command: ['pnpm', 'run', 'real-app:scenario-ticket-lifecycle'],
    // Bootstraps a chief + a real codex delegation + the full worker→chief
    // review loop in one app lifecycle; needs more than the default budget.
    timeoutMs: 360_000,
  },
  {
    id: 'nudge-trigger',
    label: 'Ticket nudge: paused gate holds, then the real "deliver now" button doorbells the agent',
    command: ['pnpm', 'run', 'real-app:scenario-nudge-trigger'],
    // Boots a real codex agent, drives it idle, produces unread ticket activity,
    // and clicks the live trigger button; needs more than the default budget.
    timeoutMs: 360_000,
  },
  {
    id: 'terminal-block-copy',
    label: 'OSC 133 block copy via real fish + native Cmd+C',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-block-copy'],
  },
  {
    id: 'terminal-context-menu',
    label: 'Terminal context menu via native right-click + clipboard',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-context-menu'],
  },
  {
    id: 'terminal-block-resize',
    label: 'Block geometry across fish/bash/zsh through relaunch replay + split/close-split',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-block-resize'],
    // Three shells share one launch + relaunch lifecycle; the default
    // per-scenario budget is too tight for the full sweep.
    timeoutMs: 360_000,
  },
  {
    id: 'tr205-codex',
    label: 'TR-205 remote codex',
    command: ['pnpm', 'run', 'real-app:scenario-tr205'],
  },
  {
    id: 'tr205-claude',
    label: 'TR-205 remote claude',
    command: ['pnpm', 'run', 'real-app:scenario-tr205', '--', '--remote-agent', 'claude'],
  },
  {
    id: 'tr502',
    label: 'TR-502 remote relaunch splits',
    command: ['pnpm', 'run', 'real-app:scenario-tr502'],
  },
  {
    id: 'tr504',
    label: 'TR-504 remote cleanup',
    command: ['pnpm', 'run', 'real-app:scenario-tr504'],
  },
  {
    id: 'tr402-local-codex',
    label: 'TR-402 local codex',
    command: ['pnpm', 'run', 'real-app:scenario-tr402-local-codex'],
  },
  {
    id: 'tr402-local-claude',
    label: 'TR-402 local claude',
    command: ['pnpm', 'run', 'real-app:scenario-tr402-local-claude'],
  },
  {
    id: 'tr201-local-claude',
    label: 'TR-201 local claude existing split relaunch',
    command: ['pnpm', 'run', 'real-app:scenario-tr201'],
  },
  {
    id: 'tr204-local-claude',
    label: 'TR-204 local claude relaunch formatting',
    command: ['pnpm', 'run', 'real-app:scenario-tr204'],
  },
  {
    id: 'tr301-local-claude',
    label: 'TR-301 local claude utility focus',
    command: ['pnpm', 'run', 'real-app:scenario-tr301'],
  },
  {
    id: 'tr401-local-claude',
    label: 'TR-401 local claude resize',
    command: ['pnpm', 'run', 'real-app:scenario-tr401'],
  },
  {
    id: 'tr401-local-codex',
    label: 'TR-401 local codex resize',
    command: ['pnpm', 'run', 'real-app:scenario-tr401-local-codex'],
  },
  {
    id: 'tr401-codex-initial-pane',
    label: 'TR-401 Codex fresh initial-pane resize',
    command: ['pnpm', 'run', 'real-app:scenario-tr401-codex-main'],
  },
  {
    id: 'codex-resume',
    label: 'Codex native resume id mapping',
    command: ['pnpm', 'run', 'real-app:scenario-codex-resume'],
  },
  {
    id: 'ghostty-scroll',
    label: 'Ghostty scrollback anchoring while output streams',
    command: ['pnpm', 'run', 'real-app:scenario-ghostty-scroll'],
  },
];

export function resolveScenarios(selected, catalog = scenarioCatalog) {
  if (!selected.length) {
    return catalog;
  }
  const byId = new Map(catalog.map((scenario) => [scenario.id, scenario]));
  return selected.map((id) => {
    const scenario = byId.get(id);
    if (!scenario) {
      throw new Error(`Unknown scenario id: ${id}`);
    }
    return scenario;
  });
}

export function resolveScenario(id, catalog = scenarioCatalog) {
  return resolveScenarios([id], catalog)[0];
}
