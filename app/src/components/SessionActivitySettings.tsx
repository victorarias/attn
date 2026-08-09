// app/src/components/SessionActivitySettings.tsx
//
// Settings › Agents › Session activity. One short present-tense line per
// session saying what each agent is doing right now, generated from the
// session's own transcript.
//
// The agent choice is deliberately required rather than defaulted. Claude and
// Codex differ enough in speed, price, and which account pays that picking one
// would be choosing how the user's money is spent, so the daemon reports an
// enabled-but-unpicked feature as an error and this pane refuses to enable it.
import { useCallback, useMemo, useState } from 'react';
import type { SessionAgent } from '../types/sessionAgent';
import { agentLabel } from '../utils/agentAvailability';

export const ACTIVITY_ENABLED_SETTING = 'activity.enabled';
export const ACTIVITY_CONFIG_SETTING = 'activity.config';
export const ACTIVITY_INTERVALS_SETTING = 'activity.intervals';
export const ACTIVITY_PRESENCE_TIER_SETTING = 'activity.presence_tier';

// The daemon clamps to this range; the inputs say so rather than silently
// accepting a number that comes back different.
const INTERVAL_MIN_SECONDS = 30;
const INTERVAL_MAX_SECONDS = 3600;

const MODEL_PRESETS: Partial<Record<SessionAgent, { value: string; label: string }[]>> = {
  claude: [
    { value: 'claude-haiku-4-5', label: 'Haiku 4.5 (Recommended)' },
    { value: 'sonnet', label: 'Sonnet (Higher quality)' },
  ],
  codex: [
    { value: 'gpt-5.6-luna', label: 'gpt-5.6-luna (Recommended — fastest, cheapest)' },
    { value: 'gpt-5.4-mini', label: 'gpt-5.4-mini' },
  ],
};

// Effort measured inert on Claude — none, low, medium and high all land within
// the same output-token band on identical input — so only Codex offers it.
const EFFORT_LEVELS: Partial<Record<SessionAgent, string[]>> = {
  codex: ['minimal', 'low', 'medium', 'high'],
};

interface ActivityConfig {
  agent: SessionAgent | '';
  model: string;
  effort: string;
}

export function parseActivityConfigSetting(raw: string | undefined): ActivityConfig {
  if (!raw?.trim()) return { agent: '', model: '', effort: '' };
  try {
    const parsed = JSON.parse(raw) as { agent?: string; model?: string; effort?: string };
    return {
      agent: (parsed.agent ?? '') as SessionAgent | '',
      model: parsed.model ?? '',
      effort: parsed.effort ?? '',
    };
  } catch {
    return { agent: '', model: '', effort: '' };
  }
}

export function parseActivityIntervalsSetting(raw: string | undefined): { watching: string; present: string } {
  if (raw?.trim()) {
    try {
      const parsed = JSON.parse(raw) as { watching?: number; present?: number };
      return {
        watching: String(parsed.watching ?? 120),
        present: String(parsed.present ?? 300),
      };
    } catch {
      /* a stored value that no longer parses shows the defaults, same as the daemon uses */
    }
  }
  return { watching: '120', present: '300' };
}

interface SessionActivitySettingsProps {
  settings: Record<string, string>;
  /** Installed agents that can run a scoped headless task. */
  agents: SessionAgent[];
  onSetSetting: (key: string, value: string) => void;
}

export function SessionActivitySettings({
  settings,
  agents,
  onSetSetting,
}: SessionActivitySettingsProps) {
  const saved = useMemo(() => parseActivityConfigSetting(settings[ACTIVITY_CONFIG_SETTING]), [settings]);
  const savedIntervals = useMemo(
    () => parseActivityIntervalsSetting(settings[ACTIVITY_INTERVALS_SETTING]),
    [settings],
  );
  const enabled = (settings[ACTIVITY_ENABLED_SETTING] || 'false') === 'true';
  const presenceTier = settings[ACTIVITY_PRESENCE_TIER_SETTING] || 'away';

  const [agent, setAgent] = useState<SessionAgent | ''>(saved.agent);
  const [model, setModel] = useState(saved.model);
  const [effort, setEffort] = useState(saved.effort);
  // Whether the model box is a free-text entry. Kept beside the value rather
  // than inferred from it, so clearing a custom model does not silently snap the
  // control back to the preset list mid-edit.
  const [customModel, setCustomModel] = useState(
    Boolean(saved.model) && !(MODEL_PRESETS[saved.agent as SessionAgent] ?? []).some((p) => p.value === saved.model),
  );
  const [watching, setWatching] = useState(savedIntervals.watching);
  const [present, setPresent] = useState(savedIntervals.present);

  const presets = agent ? MODEL_PRESETS[agent] ?? [] : [];
  const efforts = agent ? EFFORT_LEVELS[agent] ?? [] : [];

  const handleAgentChange = useCallback((next: SessionAgent | '') => {
    setAgent(next);
    // The models are per-agent, so carrying one across would save a model the
    // new agent cannot run.
    setModel('');
    setEffort('');
    setCustomModel(false);
  }, []);

  const save = useCallback(() => {
    if (!agent) return;
    const config: Record<string, string> = { agent };
    if (model.trim()) config.model = model.trim();
    if (effort.trim()) config.effort = effort.trim();
    onSetSetting(ACTIVITY_CONFIG_SETTING, JSON.stringify(config));
    onSetSetting(ACTIVITY_INTERVALS_SETTING, JSON.stringify({
      watching: Number(watching) || 120,
      present: Number(present) || 300,
    }));
  }, [agent, model, effort, watching, present, onSetSetting]);

  const toggle = useCallback(() => {
    onSetSetting(ACTIVITY_ENABLED_SETTING, enabled ? 'false' : 'true');
  }, [enabled, onSetSetting]);

  return (
    <section className="settings-block">
      <div className="settings-block-intro">
        <div className="settings-kicker">Agents</div>
        <h3>Session activity</h3>
        <p className="settings-description">
          Shows one short line per session on home saying what that agent is doing right now,
          written from the session's own transcript by a non-interactive agent. Off by default:
          it costs a little money per session per refresh and sends transcript excerpts to a
          model. Lines are only generated while you are using attn — showing home refreshes
          them fastest, being elsewhere in the app refreshes them slower, and leaving the app
          stops generation entirely.
        </p>
      </div>
      <div className="settings-block-body">
        <div className="settings-row-card">
          <div>
            <p className="settings-row-title">Generate activity lines</p>
            <p className="settings-row-copy">
              Needs an agent selected below. attn currently reads you as{' '}
              <strong data-testid="settings-activity-presence-tier">{presenceTier}</strong>
              {presenceTier === 'away' ? ' — nothing is being generated.' : '.'}
            </p>
          </div>
          <button
            type="button"
            className="settings-action"
            data-testid="settings-activity-toggle"
            onClick={toggle}
            disabled={!enabled && !saved.agent}
            title={!enabled && !saved.agent ? 'Choose and save an agent first' : undefined}
          >
            {enabled ? 'Disable' : 'Enable'}
          </button>
        </div>

        {agents.length === 0 && (
          <div className="settings-warning">No installed agent supports scoped headless tasks.</div>
        )}

        <div className="settings-field-grid two-column">
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-activity-agent">Agent</label>
            <select
              id="settings-activity-agent"
              data-testid="settings-activity-agent"
              className="settings-input"
              value={agent}
              onChange={(event) => handleAgentChange(event.target.value as SessionAgent | '')}
            >
              <option value="">Select an agent</option>
              {agents.map((option) => (
                <option key={option} value={option}>{agentLabel(option)}</option>
              ))}
            </select>
          </div>
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-activity-model">Model</label>
            <select
              id="settings-activity-model"
              data-testid="settings-activity-model"
              className="settings-input"
              value={customModel ? 'custom' : model}
              onChange={(event) => {
                const next = event.target.value;
                setCustomModel(next === 'custom');
                setModel(next === 'custom' ? '' : next);
              }}
              disabled={!agent}
            >
              <option value="">Recommended default</option>
              {presets.map((preset) => (
                <option key={preset.value} value={preset.value}>{preset.label}</option>
              ))}
              <option value="custom">Custom…</option>
            </select>
          </div>
        </div>

        {agent && customModel && (
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-activity-model-custom">Custom model</label>
            <input
              id="settings-activity-model-custom"
              data-testid="settings-activity-model-custom"
              type="text"
              className="settings-input"
              value={model}
              onChange={(event) => setModel(event.target.value)}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
          </div>
        )}

        {efforts.length > 0 && (
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-activity-effort">Reasoning effort</label>
            <select
              id="settings-activity-effort"
              data-testid="settings-activity-effort"
              className="settings-input"
              value={effort}
              onChange={(event) => setEffort(event.target.value)}
            >
              <option value="">Recommended default</option>
              {efforts.map((level) => (
                <option key={level} value={level}>{level}</option>
              ))}
            </select>
          </div>
        )}

        <div className="settings-field-grid two-column">
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-activity-watching">
              Refresh on home (seconds)
            </label>
            <input
              id="settings-activity-watching"
              data-testid="settings-activity-watching"
              type="number"
              min={INTERVAL_MIN_SECONDS}
              max={INTERVAL_MAX_SECONDS}
              className="settings-input"
              value={watching}
              onChange={(event) => setWatching(event.target.value)}
            />
          </div>
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-activity-present">
              Refresh elsewhere in the app (seconds)
            </label>
            <input
              id="settings-activity-present"
              data-testid="settings-activity-present"
              type="number"
              min={INTERVAL_MIN_SECONDS}
              max={INTERVAL_MAX_SECONDS}
              className="settings-input"
              value={present}
              onChange={(event) => setPresent(event.target.value)}
            />
          </div>
        </div>

        <div className="settings-row-inline">
          <button
            type="button"
            className="settings-action"
            data-testid="settings-activity-save"
            onClick={save}
            disabled={!agent}
          >
            Save
          </button>
        </div>
        <div className="settings-hint">
          A session that has written nothing since its last line is skipped, so blocked and
          finished agents cost nothing however long home stays open.
        </div>
      </div>
    </section>
  );
}
