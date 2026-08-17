// pi auto mode: the three results the auto mode settings section waits on.
//
// Its own module rather than a case in the hook's switch, for the same reason
// daemonBusEvents.ts is one — the domain is self-contained. Reached from the
// switch's `default` chain.
//
// Design: docs/plans/2026-08-16-pi-auto-mode.md.

import type {
  AutoModeConfigInfo,
  AutoModeDenialInfo,
  AutoModeProposalInfo,
} from '../types/generated';
import { type PendingRequests, settlePendingRequest } from './daemonPendingRequests';

export type { AutoModeConfigInfo, AutoModeDenialInfo, AutoModeProposalInfo };

/** The promoted policy plus everything waiting on a human. */
export interface AutoModeState {
  config: AutoModeConfigInfo;
  /** Pending only — the daemon resolves promoted and discarded ones away. */
  proposals: AutoModeProposalInfo[];
  denials: AutoModeDenialInfo[];
}

/** What a promote answers with: the resolved proposal and the config it produced. */
export interface AutoModePromotion {
  proposal: AutoModeProposalInfo | null;
  config: AutoModeConfigInfo | null;
}

// A loosely typed view of the wire event: these arrive as parsed JSON, so every
// field is checked rather than asserted.
interface AutoModeDaemonEvent {
  event?: string;
  success?: boolean;
  error?: string;
  [key: string]: unknown;
}

const str = (value: unknown): string => (typeof value === 'string' ? value : '');
const list = <T,>(value: unknown): T[] => (Array.isArray(value) ? (value as T[]) : []);

const emptyConfig = (): AutoModeConfigInfo => ({
  enabled_default: false,
  environment: [],
  allow: [],
  hard_deny: [],
  classifier_model: '',
  escalation_model: '',
});

const toConfig = (value: unknown): AutoModeConfigInfo => {
  if (typeof value !== 'object' || value === null) return emptyConfig();
  const raw = value as Record<string, unknown>;
  return {
    enabled_default: raw.enabled_default === true,
    environment: list<string>(raw.environment),
    allow: list<string>(raw.allow),
    hard_deny: list<string>(raw.hard_deny),
    classifier_model: str(raw.classifier_model),
    escalation_model: str(raw.escalation_model),
  };
};

const toState = (event: AutoModeDaemonEvent): AutoModeState => ({
  config: toConfig(event.config),
  proposals: list<AutoModeProposalInfo>(event.proposals),
  denials: list<AutoModeDenialInfo>(event.denials),
});

const toPromotion = (event: AutoModeDaemonEvent): AutoModePromotion => ({
  proposal: (event.proposal as AutoModeProposalInfo | undefined) ?? null,
  config: event.config === undefined ? null : toConfig(event.config),
});

/**
 * Settles an auto mode result, or returns false for an event this module does
 * not own so the caller can keep looking.
 */
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
