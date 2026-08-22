import type {
  AutoModeConfigInfo,
  AutoModeDenialInfo,
  AutoModeProposalInfo,
} from '../types/generated';
import { type PendingRequests, settlePendingRequest } from './daemonPendingRequests';

export type { AutoModeConfigInfo, AutoModeDenialInfo, AutoModeProposalInfo };

export interface AutoModeState {
  config: AutoModeConfigInfo;

  proposals: AutoModeProposalInfo[];
  denials: AutoModeDenialInfo[];
}

export interface AutoModePatternEdit {
  config: AutoModeConfigInfo;
}

export interface AutoModePromotion {
  proposal: AutoModeProposalInfo | null;
  config: AutoModeConfigInfo | null;
}

interface AutoModeDaemonEvent {
  event?: string;
  success?: boolean;
  error?: string;
  [key: string]: unknown;
}

const list = <T,>(value: unknown): T[] => (Array.isArray(value) ? (value as T[]) : []);

const emptyConfig = (): AutoModeConfigInfo => ({
  enabled_default: false,
  environment: [],
  allow: [],
  hard_deny: [],
  shipped_hard_deny: [],
  classifier_models: [],
  escalation_models: [],
});

const toConfig = (value: unknown): AutoModeConfigInfo => {
  if (typeof value !== 'object' || value === null) return emptyConfig();
  const raw = value as Record<string, unknown>;
  return {
    enabled_default: raw.enabled_default === true,
    environment: list<string>(raw.environment),
    allow: list<string>(raw.allow),
    hard_deny: list<string>(raw.hard_deny),
    shipped_hard_deny: list<string>(raw.shipped_hard_deny),
    classifier_models: list<string>(raw.classifier_models),
    escalation_models: list<string>(raw.escalation_models),
  };
};

const toState = (event: AutoModeDaemonEvent): AutoModeState => ({
  config: toConfig(event.config),
  proposals: list<AutoModeProposalInfo>(event.proposals),
  denials: list<AutoModeDenialInfo>(event.denials),
});

const toPatternEdit = (event: AutoModeDaemonEvent): AutoModePatternEdit | undefined =>
  event.config === undefined ? undefined : { config: toConfig(event.config) };

const toPromotion = (event: AutoModeDaemonEvent): AutoModePromotion => ({
  proposal: (event.proposal as AutoModeProposalInfo | undefined) ?? null,
  config: event.config === undefined ? null : toConfig(event.config),
});

export function handleAutoModeDaemonEvent(
  event: AutoModeDaemonEvent,
  pending: PendingRequests,
): boolean {
  switch (event.event) {
    case 'automode_state_result':
      settlePendingRequest(
        pending,
        'automode_get',
        event,
        toState,
        'Reading auto mode failed',
      );
      return true;
    case 'automode_promote_result':
      settlePendingRequest(
        pending,
        'automode_promote',
        event,
        toPromotion,
        'Promoting the proposal failed',
      );
      return true;

    case 'automode_pattern_result': {
      const settled = settlePendingRequest(
        pending,
        'automode_pattern_add',
        event,
        toPatternEdit,
        'Adding the pattern failed',
      );
      if (!settled) {
        settlePendingRequest(
          pending,
          'automode_pattern_remove',
          event,
          toPatternEdit,
          'Removing the pattern failed',
        );
      }
      return true;
    }
    case 'automode_discard_result':
      settlePendingRequest(
        pending,
        'automode_discard',
        event,
        toPromotion,
        'Discarding the proposal failed',
      );
      return true;
    default:
      return false;
  }
}
