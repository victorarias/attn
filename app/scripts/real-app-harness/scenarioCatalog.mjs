export const scenarioCatalog = [
  {
    id: 'prompt-composition',
    runnerId: 'PromptComposition',
    label: 'Prompt delivery: ordinary/chief channels, peer attribution, crew wake/sleep and successor',
    command: ['node', 'scripts/real-app-harness/scenario-prompt-composition.mjs'],
    timeoutMs: 240_000,
  },
  {
    id: 'workspace-shell-lifecycle',
    runnerId: 'WORKSPACE-SHELL-LIFECYCLE',
    label: 'Workspace shell lifecycle',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-shell-lifecycle'],
  },
  {
    id: 'workspace-creation-shortcuts',
    runnerId: 'WORKSPACE-CREATION-SHORTCUTS',
    label: 'Workspace creation shortcuts',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-creation-shortcuts'],
  },
  {
    id: 'linux-shortcuts',
    runnerId: 'LINUX-SHORTCUTS',
    label: 'Linux terminal-style shortcuts through xdotool',
    command: ['pnpm', 'run', 'real-app:scenario-linux-shortcuts'],
    soakOnly: true,
  },
  {
    id: 'workspace-switching',
    runnerId: 'WORKSPACE-SWITCHING',
    label: 'Workspace switching',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-switching'],
  },
  {
    id: 'workspace-move-leaf',
    runnerId: 'WORKSPACE-MOVE-LEAF',
    label: 'Workspace move pane between workspaces',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-move-leaf'],
  },
  {
    id: 'workspace-close-last-session-switches-back',
    runnerId: 'WORKSPACE-CLOSE-LAST-SESSION-SWITCHES-BACK',
    label: 'Workspace close last session switches back',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-close-last-session-switches-back'],
  },
  {
    id: 'workspace-close-one-session-keeps-selection',
    runnerId: 'WORKSPACE-CLOSE-ONE-SESSION-KEEPS-SELECTION',
    label: 'Workspace close one session keeps selection',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-close-one-session-keeps-selection'],
  },
  {
    id: 'close-pane-nonblocking',
    runnerId: 'CLOSE-PANE-NONBLOCKING',
    label: 'Close pane does not wait for process teardown',
    command: ['pnpm', 'run', 'real-app:scenario-close-pane-nonblocking'],
  },
  {
    id: 'session-close-ledger',
    runnerId: 'SESSION-CLOSE-LEDGER',
    label: 'Closing a worktree session records it and leaves the worktree alone',
    command: ['pnpm', 'run', 'real-app:scenario-session-close-ledger'],
  },
  {
    id: 'session-reopen',
    runnerId: 'SESSION-REOPEN',
    label: 'A closed worktree session reopens under its own id, recreating a deleted worktree only when asked',
    command: ['pnpm', 'run', 'real-app:scenario-session-reopen'],
  },
  {
    id: 'tile-only-workspace-select',
    runnerId: 'TILE-ONLY-WORKSPACE-SELECT',
    label: 'Tile-only workspace select + render',
    command: ['pnpm', 'run', 'real-app:scenario-tile-only-workspace-select'],
  },
  {
    id: 'markdown-opener',
    runnerId: 'MARKDOWN-OPENER',
    label: 'Global Cmd+P markdown opener (git-enumerated fuzzy search + recents)',
    command: ['pnpm', 'run', 'real-app:scenario-markdown-opener'],
  },
  {
    id: 'notebook-tile-finder',
    runnerId: 'NOTEBOOK-TILE-FINDER',
    label: 'Notebook tile finder (native Cmd+Opt+N dock, Cmd+P re-summon)',
    command: ['pnpm', 'run', 'real-app:scenario-notebook-tile-finder'],
  },
  {
    id: 'notebook-editor-undo',
    runnerId: 'NOTEBOOK-EDITOR-UNDO',
    label: 'Notebook editor undo/redo (native Cmd+Z / Shift+Cmd+Z reach CodeMirror)',
    command: ['pnpm', 'run', 'real-app:scenario-notebook-editor-undo'],
  },
  {
    id: 'editor-workspace-root',
    runnerId: 'EDITOR-WORKSPACE-ROOT',
    label: 'Editor tile over an arbitrary workspace root (off-root gating + positive control)',
    command: ['pnpm', 'run', 'real-app:scenario-editor-workspace-root'],
  },
  {
    id: 'autoclose-on-exit',
    runnerId: 'AUTOCLOSE-ON-EXIT',
    label: 'Auto-close on clean exit, keep failed exits',
    command: ['pnpm', 'run', 'real-app:scenario-autoclose-on-exit'],
  },
  {
    id: 'present-flow',
    runnerId: 'PRESENT-FLOW',
    label: 'Present flow: waiting CLI → window → submit round → synchronous feedback',
    command: ['pnpm', 'run', 'real-app:scenario-present-flow'],
    timeoutMs: 240_000,
  },
  {
    id: 'garden-seed-handoff',
    runnerId: 'GardenSeedHandoff',
    label: 'Garden seed handoff: one session leaves a handoff and ends, the next is primed on tend',
    command: ['pnpm', 'run', 'real-app:scenario-garden-seed-handoff'],
  },
  {
    id: 'garden-plot-dispatch',
    runnerId: 'GardenPlotDispatch',
    label: 'Garden plot dispatch: a plot is planted, a delegate is dispatched at it, and the panel walks it draining',
    command: ['pnpm', 'run', 'real-app:scenario-garden-plot-dispatch'],
    timeoutMs: 240_000,
  },
  {
    id: 'garden-seed-tile-navigation',
    runnerId: 'GardenSeedTileNavigation',
    label: 'Garden seed tile navigation: a plot walks in, a native Escape unwinds it, Reveal hands the place to the Garden',
    command: ['pnpm', 'run', 'real-app:scenario-garden-seed-tile-navigation'],
  },
  {
    id: 'garden-delegation-reporting',
    runnerId: 'GardenDelegationReporting',
    label: 'Garden delegation reporting: a delegation reports on its seed — log notes, artifacts, steering',
    command: ['pnpm', 'run', 'real-app:scenario-garden-delegation-reporting'],
    timeoutMs: 240_000,
  },
  {
    id: 'garden-seed-nudges',
    runnerId: 'GardenSeedNudges',
    label: 'Garden seed nudges: generic inbox doorbells carry a ringing note and harvest through durable reads',
    command: ['pnpm', 'run', 'real-app:scenario-garden-seed-nudges'],
    timeoutMs: 240_000,
  },
  {
    id: 'garden-seed-read-receipts',
    runnerId: 'GardenSeedReadReceipts',
    label: 'Garden seed read receipts: inbox reads rearm generic doorbells without prompt-submit hooks',
    command: ['pnpm', 'run', 'real-app:scenario-garden-seed-read-receipts'],
    timeoutMs: 240_000,
  },
  {
    id: 'peer-message-read-receipts',
    runnerId: 'PeerMessageReadReceipts',
    label: 'Agent inbox: a mixed burst shares one doorbell and an unread item gets a cooldown reminder',
    command: ['pnpm', 'run', 'real-app:scenario-peer-message-read-receipts'],
    timeoutMs: 240_000,
  },
  {
    id: 'garden-seed-reopen',
    runnerId: 'GardenSeedReopen',
    label: 'Garden continuation: a closed tender resumes exactly, then hands the same seed to a new agent',
    command: ['pnpm', 'run', 'real-app:scenario-garden-seed-reopen'],
    timeoutMs: 240_000,
  },
  {
    id: 'garden-seed-header',
    runnerId: 'GardenSeedHeader',
    label: 'Garden header: five lifecycle states, latest note, outcome, and seed navigation',
    command: ['node', 'scripts/real-app-harness/scenario-garden-seed-header.mjs'],
    timeoutMs: 180_000,
  },
  {
    id: 'delegate-workspace-placement',
    runnerId: 'DELEGATE-WORKSPACE-PLACEMENT',
    label: 'Delegate placement: --workspace places the pane, --no-worktree keeps the source checkout',
    command: ['pnpm', 'run', 'real-app:scenario-delegate-workspace-placement'],
    timeoutMs: 240_000,
  },
  {
    id: 'nudge-trigger',
    runnerId: 'NUDGE-TRIGGER',
    label: 'Legacy ticket nudge: a missing-hook inbox doorbell cannot block a later busy-to-idle wake',
    command: ['pnpm', 'run', 'real-app:scenario-nudge-trigger'],
    timeoutMs: 360_000,
  },
  {
    id: 'countdown-cancel',
    runnerId: 'COUNTDOWN-CANCEL',
    label: 'Countdown cancel: a real Cmd+. stops the auto-settle and nudge countdowns on screen',
    command: ['pnpm', 'run', 'real-app:scenario-countdown-cancel'],
    timeoutMs: 300_000,
  },
  {
    id: 'settle-typing-hold',
    runnerId: 'SETTLE-TYPING-HOLD',
    label: 'Settle typing hold: typing to an agent freezes its settling countdown, and going quiet hands back a whole one',
    command: ['pnpm', 'run', 'real-app:scenario-settle-typing-hold'],
    timeoutMs: 300_000,
  },
  {
    id: 'agent-queue',
    runnerId: 'AGENT-QUEUE',
    label: 'Agent queue: a turn opens on a state, and closes only when the user settles or snoozes it',
    command: ['pnpm', 'run', 'real-app:scenario-agent-queue'],
    timeoutMs: 400_000,
  },
  {
    id: 'automation-lifecycle',
    runnerId: 'AUTOMATION-LIFECYCLE',
    label: 'Automation lifecycle: edit-rebind, delete-resurrect, cleanup-dirty-safe',
    command: ['pnpm', 'run', 'real-app:scenario-automation-lifecycle'],
    timeoutMs: 600_000,
  },
  {
    id: 'terminal-block-copy',
    skipOn: { linux: 'Cmd+C reaches the terminal through the macOS menu accelerator' },
    runnerId: 'TERMINAL-BLOCK-COPY',
    label: 'OSC 133 block copy via real fish + native Cmd+C',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-block-copy'],
  },
  {
    id: 'terminal-scrollback-colors',
    runnerId: 'TERMINAL-SCROLLBACK-COLORS',
    label: 'Terminal scrollback keeps indexed and truecolor cell colors',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-scrollback-colors'],
  },
  {
    id: 'terminal-annotations',
    runnerId: 'TERMINAL-ANNOTATIONS',
    label: 'Annotate a live claude turn; it survives the next turn and an app relaunch',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-annotations'],
    timeoutMs: 600_000,
  },
  {
    id: 'terminal-context-menu',
    runnerId: 'TERMINAL-CONTEXT-MENU',
    label: 'Terminal context menu via native right-click + clipboard',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-context-menu'],
  },
  {
    id: 'terminal-input',
    runnerId: 'TERMINAL-INPUT',
    label: 'Terminal input via packaged browser events, shortcuts, paste, and zoomed grid',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-input'],
  },
  {
    id: 'pty-host-setting',
    runnerId: 'PTY-HOST-SETTING',
    label: 'Experimental shared PTY opt-in preserves existing terminals',
    command: ['node', 'scripts/real-app-harness/scenario-pty-host-setting.mjs'],
  },
  {
    id: 'terminal-osc8-link',
    runnerId: 'TERMINAL-OSC8-LINK',
    label: 'OSC 8 hyperlink Cmd+click via native click + local HTTP probe',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-osc8-link'],
  },
  {
    id: 'terminal-md-link',
    runnerId: 'TERMINAL-MD-LINK',
    label: 'Markdown path Cmd+click docks a session-bound markdown tile',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-md-link'],
  },
  {
    id: 'terminal-seed-preview',
    runnerId: 'TERMINAL-SEED-PREVIEW',
    label: 'Known terminal seed ID hover preview and icon-only tile action',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-seed-preview'],
  },
  {
    id: 'session-usage',
    runnerId: 'SESSION-USAGE',
    label: 'Session usage combines native subagents, keeps partial costs, and opens from the Action menu',
    command: ['pnpm', 'run', 'real-app:scenario-session-usage'],
  },
  {
    id: 'terminal-block-resize',
    runnerId: 'BLOCK-RESIZE',
    label: 'Block geometry through relaunch replay + split/close-split',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-block-resize'],
    timeoutMs: 360_000,
  },
  {
    id: 'tr205-probe-codex',
    skipOn: { linux: { reason: 'needs a provisioned SSH machine; set ATTN_HARNESS_REMOTE_SSH_TARGET to its target to run it', unlessEnv: 'ATTN_HARNESS_REMOTE_SSH_TARGET' } },
    runnerId: 'TR-205',
    label: 'TR-205 remote probe (codex vocabulary)',
    command: ['pnpm', 'run', 'real-app:scenario-tr205', '--', '--remote-agent', 'probe:codex'],
    freshWorldAfter: true,
  },
  {
    id: 'tr205-probe-claude',
    skipOn: { linux: { reason: 'needs a provisioned SSH machine; set ATTN_HARNESS_REMOTE_SSH_TARGET to its target to run it', unlessEnv: 'ATTN_HARNESS_REMOTE_SSH_TARGET' } },
    runnerId: 'TR-205',
    label: 'TR-205 remote probe (claude vocabulary)',
    command: ['pnpm', 'run', 'real-app:scenario-tr205', '--', '--remote-agent', 'probe:claude'],
    freshWorldAfter: true,
  },
  {
    id: 'tr502',
    skipOn: { linux: { reason: 'needs a provisioned SSH machine; set ATTN_HARNESS_REMOTE_SSH_TARGET to its target to run it', unlessEnv: 'ATTN_HARNESS_REMOTE_SSH_TARGET' } },
    runnerId: 'TR-502',
    label: 'TR-502 remote relaunch splits',
    command: ['pnpm', 'run', 'real-app:scenario-tr502'],
    freshWorldAfter: true,
  },
  {
    id: 'tr504',
    skipOn: { linux: { reason: 'needs a provisioned SSH machine; set ATTN_HARNESS_REMOTE_SSH_TARGET to its target to run it', unlessEnv: 'ATTN_HARNESS_REMOTE_SSH_TARGET' } },
    runnerId: 'TR-504',
    label: 'TR-504 remote cleanup',
    command: ['pnpm', 'run', 'real-app:scenario-tr504'],
    freshWorldAfter: true,
  },
  {
    id: 'tr402-local-codex',
    runnerId: 'TR-402',
    label: 'TR-402 local codex',
    command: ['pnpm', 'run', 'real-app:scenario-tr402-local-codex'],
  },
  {
    id: 'tr402-local-claude',
    runnerId: 'TR-402',
    label: 'TR-402 local claude',
    command: ['pnpm', 'run', 'real-app:scenario-tr402-local-claude'],
  },
  {
    id: 'tr201-local-claude',
    runnerId: 'TR-201',
    label: 'TR-201 local claude existing split relaunch',
    command: ['pnpm', 'run', 'real-app:scenario-tr201'],
  },
  {
    id: 'tr204-local-claude',
    runnerId: 'TR-204',
    label: 'TR-204 local claude relaunch formatting',
    command: ['pnpm', 'run', 'real-app:scenario-tr204'],
  },
  {
    id: 'tr301-local-claude',
    runnerId: 'TR-301',
    label: 'TR-301 local claude utility focus',
    command: ['pnpm', 'run', 'real-app:scenario-tr301'],
  },
  {
    id: 'tr401-local-claude',
    runnerId: 'TR-401',
    label: 'TR-401 local claude resize',
    command: ['pnpm', 'run', 'real-app:scenario-tr401'],
  },
  {
    id: 'tr401-local-codex',
    runnerId: 'TR-401',
    label: 'TR-401 local codex resize',
    command: ['pnpm', 'run', 'real-app:scenario-tr401-local-codex'],
  },
  {
    id: 'tr401-codex-initial-pane',
    runnerId: 'TR-401-CODEX-MAIN',
    label: 'TR-401 Codex fresh initial-pane resize',
    command: ['pnpm', 'run', 'real-app:scenario-tr401-codex-main'],
  },
  {
    id: 'codex-resume',
    runnerId: 'TR-CODEX-RESUME',
    label: 'Codex native resume id mapping',
    command: ['pnpm', 'run', 'real-app:scenario-codex-resume'],
  },
  {
    id: 'recoverable-auto-revive',
    runnerId: 'RECOVERABLE-AUTO-REVIVE',
    label: 'Recoverable Claude session auto-revives after daemon restart',
    command: ['pnpm', 'run', 'real-app:scenario-recoverable-auto-revive'],
    timeoutMs: 360_000,
  },
  {
    id: 'crash-recovery',
    runnerId: 'CRASH-REC',
    label: 'A machine crash keeps every session it can bring back and reaps the rest',
    command: ['pnpm', 'run', 'real-app:scenario-crash-recovery'],
    timeoutMs: 360_000,
  },
  {
    id: 'snapshot-scrollback-restore',
    runnerId: 'SNAPSHOT-SCROLLBACK-RESTORE',
    label: 'Deep scrollback survives an app relaunch restore',
    command: ['pnpm', 'run', 'real-app:scenario-snapshot-scrollback-restore'],
  },
  {
    id: 'ghostty-scroll',
    runnerId: 'GHOSTTY-SCROLLBACK-ANCHOR',
    label: 'Ghostty scrollback anchoring while output streams',
    command: ['pnpm', 'run', 'real-app:scenario-ghostty-scroll'],
  },
  {
    id: 'present-submit-closes-window',
    runnerId: 'PRESENT-SUBMIT-CLOSES-WINDOW',
    label: 'Present submit closes the real presentation window',
    command: ['pnpm', 'run', 'real-app:scenario-present-submit-closes-window'],
  },
  {
    id: 'nisse-conversation',
    skipOn: { linux: 'the attn-nisse host exits seconds after spawn in some Linux runs (s-k5mf6n); nisse is being deprecated, so its Linux witness is off until its fate is decided' },
    runnerId: 'PI-HOST-CONVERSATION',
    allowRealAgents: ['pi'],
    label: 'Conversation session: nisse round trip, second prompt after settle, no orphans on close',
    command: ['pnpm', 'run', 'real-app:scenario-nisse-conversation'],
    // Needs the attn-pi plugin installed (`attn plugin install-bundled attn-pi`). The model
    // is a loopback stub; `pi` is allowed only for the plugin's `pi --version` health probe.
    timeoutMs: 300_000,
  },
  {
    id: 'nisse-nudge',
    skipOn: { linux: 'the attn-nisse host exits seconds after spawn in some Linux runs (s-k5mf6n); nisse is being deprecated, so its Linux witness is off until its fate is decided' },
    runnerId: 'PI-HOST-NUDGE',
    allowRealAgents: ['pi'],
    label: 'Conversation session: steer mid-run, nudge an idle session, state and turn',
    command: ['pnpm', 'run', 'real-app:scenario-nisse-nudge'],
    // Same prereqs as nisse-conversation.
    timeoutMs: 420_000,
  },
  {
    id: 'nisse-tools',
    skipOn: { linux: 'the attn-nisse host exits seconds after spawn in some Linux runs (s-k5mf6n); nisse is being deprecated, so its Linux witness is off until its fate is decided' },
    runnerId: 'PI-HOST-TOOLS',
    allowRealAgents: ['pi'],
    label: 'Conversation session: tool cards, on-demand detail, full output, patch as a diff, queue cancel',
    command: ['pnpm', 'run', 'real-app:scenario-nisse-tools'],
    // Same prereqs as nisse-conversation.
    timeoutMs: 600_000,
  },
  {
    id: 'nisse-revive',
    skipOn: { linux: 'the attn-nisse host exits seconds after spawn in some Linux runs (s-k5mf6n); nisse is being deprecated, so its Linux witness is off until its fate is decided' },
    runnerId: 'PI-HOST-REVIVE',
    allowRealAgents: ['pi'],
    label: 'Conversation session: kill -9 to recoverable, reload with history, snapshot on a cold client',
    command: ['pnpm', 'run', 'real-app:scenario-nisse-revive'],
    // Same prereqs as nisse-conversation.
    timeoutMs: 600_000,
  },
  {
    id: 'nisse-delegate',
    skipOn: { linux: 'the attn-nisse host exits seconds after spawn in some Linux runs (s-k5mf6n); nisse is being deprecated, so its Linux witness is off until its fate is decided' },
    runnerId: 'PI-HOST-DELEGATE',
    allowRealAgents: ['pi'],
    label: 'Delegation to a conversation agent: brief as the first message, the agent reports on its own ticket, a brief survives a crash before the first word',
    command: ['pnpm', 'run', 'real-app:scenario-nisse-delegate'],
    // Same prereqs as nisse-conversation.
    timeoutMs: 900_000,
  },
  {
    id: 'nisse-history',
    skipOn: { linux: 'the attn-nisse host exits seconds after spawn in some Linux runs (s-k5mf6n); nisse is being deprecated, so its Linux witness is off until its fate is decided' },
    runnerId: 'PI-HOST-HISTORY',
    allowRealAgents: ['pi'],
    label: 'Conversation session: resume an existing conversation file, page a long transcript, switch model mid-session',
    command: ['pnpm', 'run', 'real-app:scenario-nisse-history'],
    // Same prereqs as nisse-conversation.
    timeoutMs: 600_000,
  },
  {
    id: 'nisse-markdown-stream',
    runnerId: 'NISSE-MARKDOWN-STREAM',
    allowRealAgents: ['pi'],
    label: 'Conversation session: a recorded reply replayed into the pane renders as markdown while it streams',
    command: ['pnpm', 'run', 'real-app:scenario-nisse-markdown-stream'],
    // Needs attn-pi installed, like the other nisse scenarios, but calls no model:
    // the reply is a recording, so the run is deterministic and free.
    timeoutMs: 300_000,
  },
  {
    id: 'app-reconcile',
    runnerId: 'APP-RECONCILE',
    label: 'App reconcile: version move rebuilds, a real trim gap disables loudly, an interrupted rebuild repairs',
    command: ['pnpm', 'run', 'real-app:scenario-app-reconcile'],
    timeoutMs: 600_000,
  },
  {
    id: 'pi-security',
    runnerId: 'PI-SECURITY',
    label: 'Pi sandbox and credential filtering',
    command: ['pnpm', 'run', 'real-app:scenario-pi-security'],
    allowRealAgents: ['pi'],
    timeoutMs: 360_000,
  },
  {
    id: 'pi-automode',
    runnerId: 'PI-AUTOMODE',
    allowRealAgents: ['pi'],
    label: 'pi auto mode: envelope invisibility, a denial and its surfaces, a conversational grant, the circuit breaker',
    command: ['pnpm', 'run', 'real-app:scenario-pi-automode'],
    // Needs `pi` on PATH and the attn-pi plugin installed, but no credentials and
    // no network: the model and the classifier are both a loopback stub.
    timeoutMs: 900_000,
  },
  {
    id: 'automode-environment',
    runnerId: 'AutoModeEnvironment',
    label: 'Auto mode environment: a slot written from the pane and from the CLI, and what an unfilled one says',
    command: ['pnpm', 'run', 'real-app:scenario-automode-environment'],
    timeoutMs: 300_000,
  },
  {
    id: 'automode-no-model',
    runnerId: 'AutoModeNoModel',
    label: 'Auto mode with no model: off until one is named, on when a proposal is promoted',
    command: ['pnpm', 'run', 'real-app:scenario-automode-no-model'],
    timeoutMs: 300_000,
  },
  {
    id: 'focus-probe',
    runnerId: 'FOCUS-PROBE',
    label: 'Focus probe (no focus steal on background session create)',
    command: ['pnpm', 'run', 'real-app:focus-probe'],
    // Not part of the serial matrix sweep — only runnable directly (run-soak).
    soakOnly: true,
  },
];

export function resolveScenarios(selected, catalog = scenarioCatalog) {
  const matrixCatalog = catalog.filter((scenario) => !scenario.soakOnly);
  if (!selected.length) {
    return matrixCatalog;
  }
  const byId = new Map(matrixCatalog.map((scenario) => [scenario.id, scenario]));
  return selected.map((id) => {
    const scenario = byId.get(id);
    if (!scenario) {
      throw new Error(`Unknown scenario id: ${id}`);
    }
    return scenario;
  });
}

export function scenariosAllowingRealAgents(catalog = scenarioCatalog) {
  return catalog.filter((scenario) => scenario.allowRealAgents !== undefined);
}

export function allowRealAgentsForRunner(runnerId, catalog = scenarioCatalog) {
  const entries = typeof runnerId === 'string' && runnerId
    ? catalog.filter((scenario) => scenario.runnerId === runnerId)
    : [];
  if (entries.length === 0) {
    throw new Error([
      `agent tripwire: runner ${JSON.stringify(runnerId)} has no scenarioCatalog.mjs entry, so nothing declares whether it may run a real agent.`,
      `declare it: add a catalog entry carrying runnerId ${JSON.stringify(runnerId)}, or pass allowRealAgents to createScenarioRunner —`,
      '  false to arm the tripwire, true for every binary, or an array naming the agent binaries the scenario needs.',
    ].join('\n'));
  }
  if (entries.some((scenario) => scenario.allowRealAgents === true)) {
    return true;
  }
  const named = entries.flatMap((scenario) => (Array.isArray(scenario.allowRealAgents) ? scenario.allowRealAgents : []));
  return named.length > 0 ? [...new Set(named)] : undefined;
}

export function resolveScenario(id, catalog = scenarioCatalog) {
  const scenario = catalog.find((entry) => entry.id === id);
  if (!scenario) {
    throw new Error(`Unknown scenario id: ${id}`);
  }
  return scenario;
}
