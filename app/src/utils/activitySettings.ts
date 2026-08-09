// app/src/utils/activitySettings.ts
//
// The stored shape of the session-activity settings, and the readings taken off
// it. Kept out of SessionActivitySettings.tsx because App reads the staleness
// window without rendering the pane, and a component file that also exports
// plain functions costs Fast Refresh the ability to preserve state.
import type { SessionAgent } from '../types/sessionAgent';

export const ACTIVITY_ENABLED_SETTING = 'activity.enabled';
export const ACTIVITY_CONFIG_SETTING = 'activity.config';
export const ACTIVITY_INTERVALS_SETTING = 'activity.intervals';

// The daemon clamps to this range; the inputs say so rather than silently
// accepting a number that comes back different.
export const INTERVAL_MIN_SECONDS = 30;
export const INTERVAL_MAX_SECONDS = 3600;

export interface ActivityConfig {
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

/**
 * How old an activity line may get before a row stops presenting it as current.
 *
 * Three times the slowest configured interval, and configured rather than
 * constant because the intervals are the user's to set: a 30-minute cadence
 * measured against a fixed 15-minute window would dim every line the instant it
 * was written, which is the opposite of what dimming is for. Three times,
 * because a line older than a few missed ticks was not generated during a slow
 * tick — it was generated before a gap.
 */
export function activityStaleMs(settings: Record<string, string>): number {
  const intervals = parseActivityIntervalsSetting(settings[ACTIVITY_INTERVALS_SETTING]);
  const seconds = Math.max(
    Number(intervals.watching) || 0,
    Number(intervals.present) || 0,
    INTERVAL_MIN_SECONDS,
  );
  return seconds * 3 * 1000;
}
