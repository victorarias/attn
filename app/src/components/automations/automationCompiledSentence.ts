import { AutomationFormValues } from './automationFormModel';

// compiledSentenceSegments renders the form's current values as a single
// plain-language sentence, in user vocabulary (no YAML/spec terms). It exists
// so the form can show "what will actually happen" as the user changes
// fields, rather than making them mentally compile the trigger/launch/location
// sections themselves.

export interface SentenceSegment {
  text: string;
  emphasis?: 'accent' | 'strong' | 'mono';
}

const AGENT_LABEL: Record<AutomationFormValues['agent'], string> = {
  codex: 'Codex',
  claude: 'Claude',
};

const KNOWN_CRON_PHRASES: Record<string, string> = {
  '* * * * *': 'every minute',
  '*/5 * * * *': 'every 5 minutes',
  '0 * * * *': 'every hour, on the hour',
  '0 9 * * *': 'every day at 09:00',
  '0 9 * * 1-5': 'weekdays at 09:00',
  '0 0 * * 0': 'Sundays at midnight',
};

function isFiveFieldCron(cron: string): boolean {
  const fields = cron.trim().split(/\s+/).filter(Boolean);
  return fields.length === 5;
}

// cronPhrase resolves a cron expression to its plain-language phrase, or null
// when the value is empty or not a 5-field expression (the form's own
// scheduled-trigger validity gate) — callers render their own "not set" copy
// for null. Exported so the cron input's live preview reuses exactly the map
// this sentence is built from, rather than drifting out of sync with it.
export function cronPhrase(cron: string): string | null {
  const trimmed = cron.trim();
  if (!isFiveFieldCron(trimmed)) return null;
  return KNOWN_CRON_PHRASES[trimmed] ?? 'on this schedule';
}

function capitalize(text: string): string {
  return text.length === 0 ? text : text[0].toUpperCase() + text.slice(1);
}

function agentSegments(values: AutomationFormValues): SentenceSegment[] {
  const segments: SentenceSegment[] = [{ text: AGENT_LABEL[values.agent], emphasis: 'strong' }];
  const model = values.model.trim();
  if (model !== '') {
    const effort = values.effort.trim();
    segments.push({ text: effort !== '' ? ` (${model} · ${effort} effort)` : ` (${model})` });
  }
  return segments;
}

function repositoryCountPhrase(count: number): string {
  return `${count} selected repositor${count === 1 ? 'y' : 'ies'}`;
}

export function compiledSentenceSegments(values: AutomationFormValues): SentenceSegment[] {
  switch (values.trigger) {
    case 'manual': {
      return [
        { text: 'When you press ' },
        { text: 'Run now', emphasis: 'accent' },
        { text: ' → ' },
        ...agentSegments(values),
        { text: ' works in ' },
        { text: values.directoryPath || '…', emphasis: 'mono' },
        { text: ' — a fresh worker each run, unattended.' },
      ];
    }
    case 'scheduled': {
      const phrase = cronPhrase(values.scheduleCron) ?? "on a schedule you haven't set yet";
      const continuityText =
        values.continuity === 'singleton'
          ? 'one ongoing worker picks it up each time'
          : 'a fresh worker each run';
      const catchUpText =
        values.catchUp === 'latest'
          ? 'the latest missed run still fires after downtime'
          : values.catchUp === 'skip'
            ? 'missed runs are skipped'
            : "missed-run behavior not chosen yet";
      return [
        { text: capitalize(phrase), emphasis: 'accent' },
        { text: ' (local time) → ' },
        ...agentSegments(values),
        { text: ' works in ' },
        { text: values.directoryPath || '…', emphasis: 'mono' },
        { text: ` — ${continuityText}, ${catchUpText}, unattended.` },
      ];
    }
    case 'github_review_requested': {
      const include = values.repositoriesInclude;
      const exclude = values.repositoriesExclude;
      const scopeText = include.length > 0 ? repositoryCountPhrase(include.length) : 'any repository you can access';
      const excludeText = exclude.length > 0 ? ` (excluding ${exclude.length})` : '';
      return [
        { text: 'When ' },
        { text: 'a PR requests your review', emphasis: 'accent' },
        { text: ` on ${scopeText}${excludeText} → ` },
        ...agentSegments(values),
        { text: ' reviews it in a ' },
        { text: 'fresh worktree at the PR head', emphasis: 'strong' },
        { text: ' — one reviewer per PR, later cycles return to it, unattended.' },
      ];
    }
  }
}

export function compiledSentenceText(values: AutomationFormValues): string {
  return compiledSentenceSegments(values)
    .map((segment) => segment.text)
    .join('');
}
